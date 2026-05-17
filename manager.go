package fastconf

// Manager core: lifecycle + read API. Pipeline machinery lives in
// pipeline.go + pipeline_stages.go.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/fastabc/fastconf/pkg/decoder"
	"github.com/fastabc/fastconf/pkg/profile"
)

// Manager is the strongly-typed, lock-free configuration manager.
//
// Typical usage:
//
//	cfg, err := fastconf.New[MyConfig](ctx,
//	    fastconf.WithDir("conf.d"),
//	    fastconf.WithProfileEnv("APP_PROFILE"),
//	    fastconf.WithProvider(provider.NewEnv("APP_")),
//	    fastconf.WithWatch(true),
//	)
//	defer cfg.Close()
//	app := cfg.Get()
//
// Internally Manager serializes the write path (one reload goroutine)
// while keeping the read path completely lock-free.
type Manager[T any] struct {
	state     atomic.Pointer[State[T]]
	opts      options
	gen       atomic.Uint64
	closeOnce sync.Once
	closed    chan struct{}

	// Subscriber table: Subscribe[T,M] registers callbacks here; fireWatches
	// dispatches them on every committed state.
	watchMu  sync.Mutex
	watches  map[uint64]*subscriber[T]
	watchSeq atomic.Uint64

	// Background goroutines spawned by startWatcher / startProviderWatchers.
	bgWG sync.WaitGroup

	// Serialized external reload trigger; watcher → reloadCh → reload goroutine.
	reloadCh chan reloadRequest

	// errsCh is the drop-on-full ring fed by reloadLoop after every failed
	// reload attempt. Consumers iterate via m.Errors(); closed during Close().
	errsCh chan ReloadError

	// Optional in-memory history ring + watch-pause toggle.
	history     *ringBuffer[T]
	historyMu   sync.Mutex
	watchPaused atomic.Bool

	// Per-provider resume revision tracker (Resumable WatchFrom).
	resume *resumeState

	// Cached tenant tag — resolved once during New() so policy evaluation on
	// the reload hot path does not re-scan auditSinks via type assertion.
	tenant string

	// typedHookPlan holds the precomputed type-paired tree of typed
	// decoder hooks built once at construction. nil when the option set
	// disabled both defaults and extras.
	typedHookPlan *decoder.TypedHookPlan

	// lastMergeKeys is the strategic-merge keys table observed in the
	// most recent _meta.yaml load. atomic.Pointer to a map[string]string
	// so runMerge can read without locking.
	lastMergeKeys atomic.Pointer[map[string]string]

	// hashCache is the most recent (mergedJSON-sha → state-hash) pair.
	// Populated in commit() after a successful swap; consulted there
	// before re-marshalling *T to skip duplicate work on idempotent reloads.
	hashCache atomic.Pointer[hashCacheEntry]

	// diffReporterWorkers owns one bounded-queue goroutine per registered
	// DiffReporter so fan-out cannot grow goroutines unboundedly under
	// high reload churn. Populated in New (after the first successful
	// reload) and torn down in Close.
	diffReporterWorkers []*diffReporterWorker
}

// New constructs a Manager and runs the first reload synchronously.
// On failure no goroutine is started.
//
// Once construction succeeds, read with Get, react with Subscribe and
// Errors, preview future changes with Plan, and recover retained snapshots
// through Replay when WithHistory was configured.
func New[T any](ctx context.Context, opts ...Option) (*Manager[T], error) {
	o := defaultOptions()
	for _, fn := range opts {
		fn(&o)
	}
	// Resolve any pending WithDotEnvAuto prefixes now that the final
	// o.dir value is known, regardless of option order.
	o.applyDeferredDotEnvAuto()
	// Resolve every WithProviderByName entry against the final registry
	// state (Manager-local first, then process-wide default). Done after
	// every Option has applied so WithProviderRegistry can appear in any
	// order relative to WithProviderByName.
	o.resolveProvidersByName()
	// Re-derive the fluent flog.Logger once all options (including any
	// WithLogger) have applied, so internal call sites use the
	// final backend.
	o.refreshLog()
	if len(o.deferredErrs) > 0 {
		// Surface every deferred error from Option closures
		// (e.g. WithProviderByName lookup failure) before allocating
		// any state. Each error is emitted individually so operators
		// don't have to read the join chain to spot subsequent failures.
		for _, e := range o.deferredErrs {
			o.log.Error().Err(e).Msg("fastconf: deferred option error")
		}
		return nil, errors.Join(o.deferredErrs...)
	}
	m := &Manager[T]{
		opts:     o,
		closed:   make(chan struct{}),
		watches:  map[uint64]*subscriber[T]{},
		reloadCh: make(chan reloadRequest, 16),
		errsCh:   make(chan ReloadError, errorChanCap),
		history:  newRing[T](o.historyCap),
		resume:   newResumeState(),
	}
	for _, s := range o.auditSinks {
		if t, ok := s.(tenantAuditSink); ok {
			m.tenant = t.tenant
			break
		}
	}
	{
		// Build the typed hook plan once. Defaults are included unless
		// WithoutDefaultTypedHooks was set.
		hooks := []decoder.TypedHook{}
		if !o.typedHooksOff {
			hooks = append(hooks, decoder.DefaultTypedHooks()...)
		}
		hooks = append(hooks, o.typedHooks...)
		if len(hooks) > 0 {
			var zero T
			m.typedHookPlan = decoder.BuildTypedHookPlan(reflect.TypeOf(zero), hooks)
		}
	}
	// Validate user-supplied profile expression at startup so syntax
	// errors fail loudly instead of silently matching nothing per overlay.
	if o.profileExpr != "" {
		if _, err := profile.Compile(o.profileExpr); err != nil {
			return nil, fmt.Errorf("%w: WithProfileExpr: %v", ErrDecode, err)
		}
	}
	if err := m.reload(ctx, "initial"); err != nil {
		return nil, err
	}
	// Spawn diff-reporter workers BEFORE the reload loop so the first
	// background-triggered commit can already enqueue. The first reload
	// above did not have a prev state, so no diff was emitted.
	m.startDiffReporterWorkers()
	m.bgWG.Add(1)
	go m.reloadLoop()
	if o.watch {
		if err := m.startWatcher(ctx); err != nil {
			_ = m.Close()
			return nil, err
		}
		m.startProviderWatchers(ctx)
	}
	return m, nil
}

// Get returns a pointer to the current snapshot's value. Zero
// allocation, O(1), lock-free. The returned value MUST be treated as
// read-only.
func (m *Manager[T]) Get() *T {
	if s := m.state.Load(); s != nil {
		return s.Value
	}
	return nil
}

// Snapshot returns the full immutable State[T] snapshot used for
// diagnostics and fingerprint comparisons.
func (m *Manager[T]) Snapshot() *State[T] { return m.state.Load() }

// Reload triggers a synchronous reload. On failure the previous state
// is preserved.
//
// Options:
//   - WithSourceOverride(map) injects a one-shot in-memory layer at the
//     top of the priority stack for this reload only. The map is consumed;
//     do not mutate it after the call.
//   - WithReloadReason(s) overrides the default "manual" reason tag used
//     for audit / metrics / logging.
func (m *Manager[T]) Reload(ctx context.Context, opts ...ReloadOption) error {
	cfg := reloadConfig{reason: "manual"}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.override == nil {
		return m.requestReload(ctx, cfg.reason)
	}
	select {
	case <-m.closed:
		return ErrClosed
	default:
	}
	extra := stagedLayer{
		src: SourceRef{
			Path:     "override://once",
			Kind:     LayerProvider,
			Priority: 60 + 1000, // PriorityCLI + 1000
		},
		data: cfg.override,
	}
	reason := cfg.reason
	if reason == "manual" {
		reason = "override"
	}
	req := reloadRequest{
		ctx:    ctx,
		reason: reason,
		doneCh: make(chan error, 1),
		applyFn: func(pipeCtx context.Context) error {
			return m.reloadWithExtra(pipeCtx, reason, extra)
		},
	}
	select {
	case m.reloadCh <- req:
	case <-m.closed:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-req.doneCh:
		return err
	case <-m.closed:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ReloadOption tunes a single Reload invocation.
type ReloadOption func(*reloadConfig)

type reloadConfig struct {
	reason   string
	override map[string]any
}

// WithSourceOverride attaches a one-shot in-memory layer to this reload,
// merged above CLI flags. The override map is CONSUMED by the manager;
// callers MUST NOT mutate map keys, sub-maps, or slice contents after the
// call. The layer is not remembered: a subsequent Reload reverts to the
// natural state.
//
// Use cases: targeted integration tests, ad-hoc operator overrides in
// fastconfctl, "rehearse a change without writing a file". Never use this
// from production hot paths.
func WithSourceOverride(override map[string]any) ReloadOption {
	return func(c *reloadConfig) { c.override = override }
}

// WithReloadReason overrides the default "manual" reason tag stamped on
// the audit / metric / log lines this reload emits.
func WithReloadReason(reason string) ReloadOption {
	return func(c *reloadConfig) {
		if reason != "" {
			c.reason = reason
		}
	}
}

// Close shuts the Manager down gracefully. Idempotent. After Close
// returns, the channel from Errors() is closed; consumers iterating with
// `for re := range m.Errors()` exit cleanly.
func (m *Manager[T]) Close() error {
	// Closing m.closed signals every background goroutine — reloadLoop,
	// fsnotify watcher, provider watchers, AND diff-reporter workers —
	// to exit. bgWG.Wait then blocks until they all return.
	m.closeOnce.Do(func() { close(m.closed) })
	m.bgWG.Wait()
	// Background goroutines have stopped publishing — safe to close.
	close(m.errsCh)
	return nil
}

// Errors returns a buffered channel that publishes one ReloadError per
// failed reload attempt. The channel has a fixed capacity; if the
// consumer cannot keep up, the oldest pending error is dropped so the
// reload loop never blocks. Closed by Close.
//
// Note: the synchronous error returned by Reload(ctx, ...) (and Plan()
// failures) is also published here, so a consumer can centralise error
// handling without checking both paths.
func (m *Manager[T]) Errors() <-chan ReloadError { return m.errsCh }

// ---------------------------------------------------------------------
// Single-writer reload loop.
// ---------------------------------------------------------------------

// reloadRequest is the unit consumed by reloadLoop.
//
// ctx carries the caller's pipeline context: it is propagated all the way
// through assemble + provider.Load + commit so a Reload(ctx)/Plan(ctx)
// cancellation actually aborts slow providers, secret resolvers, and
// transformers. Triggers without a caller (fsnotify, provider watchers)
// leave ctx nil; reloadLoop substitutes context.Background() in that case.
type reloadRequest struct {
	ctx     context.Context
	reason  string
	key     string                       // optional: parent dir for fs-driven reloads (audit dim)
	applyFn func(context.Context) error // if non-nil, called instead of m.reload (e.g. rollback)
	doneCh  chan error
}

// requestReload posts a reload request to the single-writer goroutine
// and waits for the result. Returns ErrClosed if the manager has been
// closed, or ctx.Err() if the caller's context expires first.
//
// The caller's ctx is attached to the request so the pipeline itself
// (not just the wait) can be cancelled.
func (m *Manager[T]) requestReload(ctx context.Context, reason string) error {
	return m.requestReloadWithKey(ctx, reason, "")
}

// requestReloadWithKey is the variant used by the file-system watcher,
// where "key" is the parent directory whose burst triggered this reload.
// The key is surfaced in ReloadCause for audit fan-out.
func (m *Manager[T]) requestReloadWithKey(ctx context.Context, reason, key string) error {
	select {
	case <-m.closed:
		return ErrClosed
	default:
	}
	req := reloadRequest{ctx: ctx, reason: reason, key: key, doneCh: make(chan error, 1)}
	select {
	case m.reloadCh <- req:
	case <-m.closed:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-req.doneCh:
		return err
	case <-m.closed:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// rejectPendingReloads drains any already-enqueued reload requests and
// signals their waiters with ErrClosed so Close never strands callers.
func (m *Manager[T]) rejectPendingReloads() {
	for {
		select {
		case req := <-m.reloadCh:
			if req.doneCh != nil {
				req.doneCh <- ErrClosed
			}
		default:
			return
		}
	}
}

// reloadLoop is the single writer goroutine. It serializes every
// reload request so that no two reload pipelines ever interleave.
func (m *Manager[T]) reloadLoop() {
	defer m.bgWG.Done()
	for {
		select {
		case <-m.closed:
			m.rejectPendingReloads()
			return
		default:
		}
		select {
		case <-m.closed:
			m.rejectPendingReloads()
			return
		case req := <-m.reloadCh:
			// Use the caller's pipeline context when supplied so a
			// Reload(ctx) cancellation actually aborts the pipeline.
			// Triggers without a caller (fsnotify, provider watcher)
			// leave req.ctx nil — fall back to Background.
			pipeCtx := req.ctx
			if pipeCtx == nil {
				pipeCtx = context.Background()
			}
			var err error
			if req.applyFn != nil {
				err = req.applyFn(pipeCtx)
			} else {
				err = m.reloadWithKey(pipeCtx, req.reason, req.key)
			}
			if err != nil {
				m.publishReloadError(req.reason, err)
			}
			if req.doneCh != nil {
				req.doneCh <- err
			}
		}
	}
}

// reloadWithExtra performs the full reload pipeline with an additional
// in-memory layer injected at the top. Failures preserve the previous
// state, identical to the regular reload path.
func (m *Manager[T]) reloadWithExtra(ctx context.Context, reason string, extra stagedLayer) error {
	staged, appendSlices, err := m.assemble(ctx)
	if err != nil {
		return fmt.Errorf("reload-with-source assemble: %w", err)
	}
	staged = append(staged, extra)
	return m.commit(ctx, staged, appendSlices, reason)
}
