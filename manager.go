package fastconf

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/internal/fcerr"
	"github.com/fastabc/fastconf/internal/watcher"
)

// Manager owns lifecycle and reads; reload_queue.go serializes writes.

// Manager is the strongly-typed, lock-free configuration manager.
//
// Typical usage:
//
//	cfg, err := fastconf.New[MyConfig](ctx,
//	    fastconf.WithDir("conf.d"),
//	    fastconf.WithProfile(fastconf.Profile{Env: "APP_PROFILE"}),
//	    fastconf.WithProvider(env.NewEnv("APP_")),
//	    fastconf.WithWatch(fastconf.Watch{}),
//	)
//	defer cfg.Close()
//	app := cfg.Get()
//
// Internally Manager serializes the write path (one reload goroutine)
// while keeping the read path completely lock-free.
type Manager[T any] struct {
	state      atomic.Pointer[State[T]]
	opts       options
	stages     []stage[T]
	layerCache layerCache
	// metaKeys caches the combined _meta.yaml + WithMergeKeys table per
	// _meta.yaml content; owned by the single writer like layerCache.
	metaKeys metaKeysCache
	// lastInputs is the input fingerprint of the published state, zero
	// when unknown; owned by the single writer.
	lastInputs [32]byte
	gen        atomic.Uint64
	closeOnce  sync.Once
	closeDone  chan struct{}
	// lifetime is canceled exactly once, by Shutdown (or by a failed New /
	// finished Load); its Done channel is the only close signal.
	lifetimeCancel context.CancelFunc
	lifetime       context.Context

	// Subscriber callbacks are copied under the lock, then run without it.
	subscriberMu  sync.RWMutex
	subscribers   map[uint64]subscriber[T]
	subscriberSeq atomic.Uint64

	// Background goroutines spawned by startWatcher / startProviderWatchers.
	bgWG        sync.WaitGroup
	fileWatcher *watcher.Watcher
	// scannedDirs holds the directories the last reload scanned (writer-owned).
	scannedDirs []string

	// Serialized external reload trigger; watcher → reloadCh → reload goroutine.
	reloadCh chan reloadRequest

	// errsCh is the drop-on-full ring fed by reloadLoop after every failed
	// reload attempt; reloadLoop is its only writer. Consumers iterate via
	// m.Errors(); closed during Close() once reloadLoop has returned.
	errsCh chan fcerr.ReloadError

	// Optional in-memory history ring + watch-pause toggle.
	history     *ring[State[T]]
	historyMu   sync.Mutex
	watchPaused atomic.Bool

	// Per-provider last revision, passed to Provider.Watch on resubscribe.
	resume *resumeState

	// Tenant tag used by policy evaluation and committed events.
	tenant string

	// typedHookPlan holds the precomputed type-paired tree of typed
	// decoder hooks built once at construction. nil when the option set
	// disabled both defaults and extras.
	typedHookPlan *codec.TypedHookPlan

	// lastUnknownField is the last unknown-key message logged under
	// UnknownWarn, so a persistent typo logs once rather than every reload.
	lastUnknownField atomic.Value
	// Custom encoders/decoders may consult external state on each reload.
	customEncoding bool
}

// New constructs a Manager and runs the first reload synchronously.
// On failure no goroutine is started. ctx bounds that initial reload only;
// canceling it later does not stop watchers, which run until
// Close or Shutdown. Values carried by ctx remain visible to background work.
//
// Once construction succeeds, read with Get, react with Subscribe and
// Errors, preview future changes with Plan, and recover retained snapshots
// through History when WithHistory was configured.
func New[T any](ctx context.Context, opts ...Option) (*Manager[T], error) {
	m, err := newManager[T](ctx, opts)
	if err != nil {
		return nil, err
	}
	if err := m.reload(ctx, "initial", ""); err != nil {
		m.lifetimeCancel()
		return nil, err
	}
	if err := m.startBackground(); err != nil {
		_ = m.Close()
		return nil, err
	}
	return m, nil
}

// Load performs the initial pipeline synchronously without starting the
// manager reload loop, file watcher, provider watchers.
// The returned state is detached from manager lifecycle ownership and remains
// valid for read-only inspection.
//
// Load resolves options exactly like New, including file discovery via
// WithDir / WithFS / WithProfile.
func Load[T any](ctx context.Context, opts ...Option) (*State[T], error) {
	m, err := newManager[T](ctx, opts)
	if err != nil {
		return nil, err
	}
	defer m.lifetimeCancel()
	if err := m.reload(ctx, "load", ""); err != nil {
		return nil, err
	}
	return m.Snapshot(), nil
}

// newManager resolves options and prepares shared state without loading sources
// or starting workers. New and Load differ only in their lifecycle ownership.
func newManager[T any](ctx context.Context, opts []Option) (*Manager[T], error) {
	o, err := resolveOptions(opts)
	if err != nil {
		return nil, err
	}
	if !o.YAMLBridge {
		warnIfYAMLOnlyTags[T](o.Logger)
	}
	// ctx bounds initialisation only: background work keeps its values
	// (trace ids, loggers) but ends solely via Close/Shutdown.
	lifetime, cancel := context.WithCancel(context.WithoutCancel(ctx))
	return &Manager[T]{
		opts:           o,
		lifetime:       lifetime,
		lifetimeCancel: cancel,
		stages:         defaultStages[T](),
		closeDone:      make(chan struct{}),
		subscribers:    map[uint64]subscriber[T]{},
		reloadCh:       make(chan reloadRequest, reloadChanCap),
		errsCh:         make(chan fcerr.ReloadError, fcerr.ErrorChanCap),
		history:        newRing[State[T]](o.HistoryCap),
		resume:         newResumeState(),
		tenant:         o.Tenant,
		typedHookPlan:  buildTypedHookPlan[T](&o),
		customEncoding: hasCustomEncoding(reflect.TypeFor[T](), map[reflect.Type]bool{}),
	}, nil
}

func (m *Manager[T]) startBackground() error {
	m.bgWG.Add(1)
	go m.reloadLoop()
	if m.opts.Watch {
		if err := m.startWatcher(m.lifetime); err != nil {
			return err
		}
	}
	// Provider Watch is its own opt-in (a nil channel means "no watch"),
	// so it does not depend on the file-watcher switch.
	m.startProviderWatchers(m.lifetime)
	return nil
}

// Get returns a pointer to the current snapshot's value. Zero
// allocation, O(1), lock-free. The returned value MUST be treated as
// read-only.
func (m *Manager[T]) Get() *T {
	if m == nil {
		return nil
	}
	if s := m.state.Load(); s != nil {
		return s.Value()
	}
	return nil
}

// Snapshot returns the full immutable State[T] snapshot used for
// diagnostics and fingerprint comparisons.
func (m *Manager[T]) Snapshot() *State[T] {
	if m == nil {
		return nil
	}
	return m.state.Load()
}

// Close shuts the Manager down gracefully. Idempotent. After Close
// returns, the channel from Errors() is closed; consumers iterating with
// `for re := range m.Errors()` exit cleanly.
func (m *Manager[T]) Close() error {
	return m.Shutdown(context.Background())
}

// Shutdown cancels owned background work and waits for it to finish, honoring
// ctx while the close continues in the background after a deadline.
// Repeated calls share the close task. Once it finishes, Shutdown returns nil
// even if ctx is already canceled. Callbacks that ignore cancellation can
// retain their worker and snapshots after a timed-out Shutdown; Close waits
// until those callbacks return.
func (m *Manager[T]) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// Canceling lifetime signals every background goroutine — reloadLoop,
	// fsnotify watcher and provider watchers —
	// to exit. bgWG.Wait then blocks until they all return.
	m.closeOnce.Do(func() {
		m.lifetimeCancel()
		go m.finishClose()
	})
	select {
	case <-m.closeDone:
		return nil
	default:
	}
	select {
	case <-m.closeDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager[T]) finishClose() {
	m.bgWG.Wait()
	// reloadLoop, the only errsCh writer, has returned.
	close(m.errsCh)
	close(m.closeDone)
}
