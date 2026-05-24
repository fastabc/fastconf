package manager

// Manager core: lifecycle + read API. Pipeline machinery lives in
// pipeline.go + pipeline_stages.go.
//
// Channel capacity policy:
//
//   - ReloadChanCap (this file): bounded queue for external reload
//     requests fanned in from fsnotify, provider watchers, manual
//     Reload(ctx) calls. Drop-on-full: a saturated queue means the
//     reload loop is already behind, so the operator wants the system
//     to coalesce rather than block the caller. Drops surface via the
//     EventDropped metric.
//
//   - fcerr.ErrorChanCap (cross-package): ring fed by reloadLoop after
//     every failed reload. Same drop-on-full semantics — the consumer
//     drains via m.Errors(), and lagging consumers lose the oldest
//     pending error rather than backpressure reload.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/fastabc/fastconf/internal/diffreport"
	"github.com/fastabc/fastconf/internal/fcerr"
	iopts "github.com/fastabc/fastconf/internal/options"
	istate "github.com/fastabc/fastconf/internal/state"
	"github.com/fastabc/fastconf/pkg/decoder"
	"github.com/fastabc/fastconf/pkg/profile"
)

// ReloadChanCap is the buffered capacity of the reload request channel
// (M.reloadCh). When the queue is full, additional reload triggers from
// fsnotify or provider watchers are dropped on the floor and recorded
// via the EventDropped metric so the reload loop never blocks under a
// burst.
const ReloadChanCap = 16

// Manager is the strongly-typed, lock-free configuration manager.
//
// Typical usage:
//
//	cfg, err := fastconf.New[MyConfig](ctx,
//	    fastconf.WithDir("conf.d"),
//	    fastconf.WithProfile(fastconf.ProfileOptions{EnvVar: "APP_PROFILE"}),
//	    fastconf.WithProvider(provider.NewEnv("APP_")),
//	    fastconf.WithWatch(fastconf.WatchOptions{Enabled: true}),
//	)
//	defer cfg.Close()
//	app := cfg.Get()
//
// Internally Manager serializes the write path (one reload goroutine)
// while keeping the read path completely lock-free.
type M[T any] struct {
	state     atomic.Pointer[istate.State[T]]
	opts      iopts.Options
	gen       atomic.Uint64
	closeOnce sync.Once
	closed    chan struct{}

	// Subscriber table: Subscribe[T,M] registers callbacks here; fireWatches
	// dispatches them on every committed state. RWMutex so reload (read
	// path, fireWatches) cannot be serialized by Subscribe / cancel
	// (write path) under high subscriber churn.
	watchMu  sync.RWMutex
	watches  map[uint64]*subscriber[T]
	watchSeq atomic.Uint64

	// Background goroutines spawned by startWatcher / startProviderWatchers.
	bgWG sync.WaitGroup

	// Serialized external reload trigger; watcher → reloadCh → reload goroutine.
	reloadCh chan reloadRequest

	// errsCh is the drop-on-full ring fed by reloadLoop after every failed
	// reload attempt. Consumers iterate via m.Errors(); closed during Close().
	errsCh chan fcerr.ReloadError

	// Optional in-memory history ring + watch-pause toggle.
	history     *istate.Ring[istate.State[T]]
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

	// diffReportPool owns one bounded-queue goroutine per registered
	// DiffReporter so fan-out cannot grow goroutines unboundedly under
	// high reload churn. Populated in New (after the first successful
	// reload) and torn down in Close. nil when no reporters installed.
	diffReportPool *diffreport.Pool[istate.DiffEvent]
}

// New constructs a Manager and runs the first reload synchronously.
// On failure no goroutine is started.
//
// Once construction succeeds, read with Get, react with Subscribe and
// Errors, preview future changes with Plan, and recover retained snapshots
// through Replay when WithHistory was configured.
func New[T any](ctx context.Context, opts ...iopts.Option) (*M[T], error) {
	o := iopts.Default()
	for _, fn := range opts {
		fn(&o)
	}
	// Resolve any pending WithDotEnvAuto prefixes now that the final
	// o.Dir value is known, regardless of option order.
	o.ApplyDeferredDotEnvAuto()
	// Resolve every WithProviderByName entry against the final registry
	// state (Manager-local first, then process-wide default). Done after
	// every iopts.Option has applied so WithProviderRegistry can appear in any
	// order relative to WithProviderByName.
	o.ResolveProvidersByName()
	// Re-derive the fluent flog.Logger once all iopts.Options (including any
	// WithLogger) have applied, so internal call sites use the
	// final backend.
	o.RefreshLog()
	if len(o.DeferredErrs) > 0 {
		// Surface every deferred error from iopts.Option closures
		// (e.g. WithProviderByName lookup failure) before allocating
		// any state. Each error is emitted individually so operators
		// don't have to read the join chain to spot subsequent failures.
		for _, e := range o.DeferredErrs {
			o.Log.Error().Err(e).Msg("fastconf: deferred option error")
		}
		return nil, errors.Join(o.DeferredErrs...)
	}
	m := &M[T]{
		opts:     o,
		closed:   make(chan struct{}),
		watches:  map[uint64]*subscriber[T]{},
		reloadCh: make(chan reloadRequest, ReloadChanCap),
		errsCh:   make(chan fcerr.ReloadError, fcerr.ErrorChanCap),
		history:  istate.NewRing[istate.State[T]](o.HistoryCap),
		resume:   newResumeState(),
	}
	m.tenant = o.Tenant
	{
		// Build the typed hook plan once. Defaults are included unless
		// WithoutDefaultTypedHooks was set.
		hooks := []decoder.TypedHook{}
		if !o.TypedHooksOff {
			hooks = append(hooks, decoder.DefaultTypedHooks()...)
		}
		hooks = append(hooks, o.TypedHooks...)
		if len(hooks) > 0 {
			var zero T
			m.typedHookPlan = decoder.BuildTypedHookPlan(reflect.TypeOf(zero), hooks)
		}
	}
	// Validate user-supplied profile expression at startup so syntax
	// errors fail loudly instead of silently matching nothing per overlay.
	if o.ProfileExpr != "" {
		if _, err := profile.Compile(o.ProfileExpr); err != nil {
			return nil, fmt.Errorf("%w: WithProfile.Expr: %v", fcerr.ErrDecode, err)
		}
	}
	// The default BridgeJSON silently ignores yaml struct tags, so a
	// struct that only carries `yaml:` tags decodes every field to its
	// zero value. Surface that asymmetry once at New() unless the
	// caller explicitly opted into BridgeYAML.
	if o.CodecBridge == iopts.BridgeJSON {
		warnIfYAMLOnlyTags[T](o.Log)
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
	if o.Watch {
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
func (m *M[T]) Get() *T {
	if s := m.state.Load(); s != nil {
		return s.Value()
	}
	return nil
}

// Snapshot returns the full immutable istate.State[T] snapshot used for
// diagnostics and fingerprint comparisons.
func (m *M[T]) Snapshot() *istate.State[T] { return m.state.Load() }

// Close shuts the Manager down gracefully. Idempotent. After Close
// returns, the channel from Errors() is closed; consumers iterating with
// `for re := range m.Errors()` exit cleanly.
func (m *M[T]) Close() error {
	// Closing m.closed signals every background goroutine — reloadLoop,
	// fsnotify watcher, provider watchers, AND diff-reporter workers —
	// to exit. bgWG.Wait then blocks until they all return.
	m.closeOnce.Do(func() { close(m.closed) })
	m.bgWG.Wait()
	// Background goroutines have stopped publishing — safe to close.
	close(m.errsCh)
	return nil
}

// Errors returns a buffered channel that publishes one fcerr.ReloadError per
// failed reload attempt. The channel has a fixed capacity; if the
// consumer cannot keep up, the oldest pending error is dropped so the
// reload loop never blocks. Closed by Close.
//
// Note: the synchronous error returned by Reload(ctx, ...) (and Plan()
// failures) is also published here, so a consumer can centralise error
// handling without checking both paths.
func (m *M[T]) Errors() <-chan fcerr.ReloadError { return m.errsCh }
