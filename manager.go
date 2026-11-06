package fastconf

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/internal/fcerr"
	"github.com/fastabc/fastconf/internal/secret"
	"github.com/fastabc/fastconf/internal/watcher"
)

// Manager publishes immutable typed snapshots with lock-free reads and one writer.
// Call Close or Shutdown to release workers. See ExampleNew for construction.
type Manager[T any] struct {
	state      atomic.Pointer[State[T]]
	opts       options
	stages     []stage[T]
	layerCache layerCache
	// metaKeys caches the combined _meta.yaml + WithMergeKeys table per
	// _meta.yaml content; owned by the single writer like layerCache.
	metaKeys metaKeysCache
	// publishedFingerprint is the input fingerprint of the published state, zero
	// when unknown; owned by the single writer.
	publishedFingerprint [32]byte
	gen                  atomic.Uint64
	closeOnce            sync.Once
	closeDone            chan struct{}
	// lifetime is canceled exactly once, by Shutdown (or by a failed New /
	// finished Load); its Done channel is the only close signal.
	lifetimeCancel context.CancelFunc
	lifetime       context.Context

	// Subscriber callbacks are copied under the lock, then run without it.
	subscriberMu  sync.RWMutex
	subscribers   map[uint64]subscriber[T]
	subscriberSeq atomic.Uint64

	// Background goroutines spawned by startFileWatcher / startProviderWatchers.
	bgWG           sync.WaitGroup
	fileWatcher    *watcher.Watcher
	dirReloadSlots sync.Map // directory key → *dirReloadSlot
	// scannedDirs holds the directories the last reload scanned (writer-owned).
	scannedDirs []string

	// Serialized external reload trigger; watcher → reloadCh → reload goroutine.
	reloadCh chan reloadRequest
	// A single pending refresh survives provider notification overflow.
	overflowResyncCh chan struct{}

	// Writer-owned, evict-oldest error queue; closed after the reload loop exits.
	errorsCh chan fcerr.ReloadError

	// Optional in-memory history ring + watch-pause toggle.
	history      *ring[State[T]]
	historyMu    sync.Mutex
	eventsPaused atomic.Bool

	// Per-provider last revision, passed to Watcher.Watch on resubscribe.
	providerRevisions *revisionTable

	// Tenant tag used by policy evaluation and committed events.
	tenant string

	// Immutable hook metadata, nil when all hooks are disabled.
	typedHookPlan *codec.TypedHookPlan
	redactionPlan *secret.Plan

	// lastUnknownField is the last unknown-key message logged under
	// UnknownWarn, so a persistent typo logs once rather than every reload.
	lastUnknownField atomic.Value
	// Custom encoders/decoders may consult external state on each reload.
	customEncoding bool
}

// New loads synchronously, then starts the writer and configured watchers.
// ctx bounds initialization only; background work retains its values until Close
// or Shutdown. A failed initial load starts no workers.
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

// Load resolves options and runs New's initial pipeline without starting workers.
// The returned State remains valid for read-only inspection without a Close call.
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
	if err := checkTypedOptions[T](&o); err != nil {
		return nil, err
	}
	if !o.YAMLBridge {
		warnIfYAMLOnlyTags[T](o.Logger)
	}
	// ctx bounds initialisation only: background work keeps its values
	// (trace ids, loggers) but ends solely via Close/Shutdown.
	lifetime, cancel := context.WithCancel(context.WithoutCancel(ctx))
	return &Manager[T]{
		opts:              o,
		lifetime:          lifetime,
		lifetimeCancel:    cancel,
		stages:            defaultStages[T](),
		closeDone:         make(chan struct{}),
		subscribers:       map[uint64]subscriber[T]{},
		reloadCh:          make(chan reloadRequest, reloadChanCap),
		overflowResyncCh:  make(chan struct{}, 1),
		errorsCh:          make(chan fcerr.ReloadError, errorsChanCap),
		history:           newRing[State[T]](o.HistoryCap),
		providerRevisions: newRevisionTable(),
		tenant:            o.Tenant,
		typedHookPlan:     buildTypedHookPlan[T](&o),
		redactionPlan:     secret.Compile(reflect.TypeFor[T](), o.SecretPaths),
		customEncoding:    hasCustomEncoding(reflect.TypeFor[T]()),
	}, nil
}

func (m *Manager[T]) startBackground() error {
	m.bgWG.Add(1)
	go m.reloadLoop()
	if m.opts.FileWatch {
		if err := m.startFileWatcher(m.lifetime); err != nil {
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
	// reloadLoop, the only errorsCh writer, has returned.
	close(m.errorsCh)
	close(m.closeDone)
}
