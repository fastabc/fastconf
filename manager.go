package fastconf

import (
	"context"
	"fmt"

	"github.com/fastabc/fastconf/internal/fcerr"
	imanager "github.com/fastabc/fastconf/internal/manager"
	istate "github.com/fastabc/fastconf/internal/state"
	itenant "github.com/fastabc/fastconf/internal/tenant"
	"github.com/fastabc/fastconf/policy"
)

// Manager is the strongly-typed, lock-free configuration manager.
type Manager[T any] struct {
	inner *imanager.M[T]
}

// New constructs a Manager and runs the first reload synchronously.
//
// For one-line initialisation in main / init see [MustNew].
func New[T any](ctx context.Context, opts ...Option) (*Manager[T], error) {
	m, err := imanager.New[T](ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &Manager[T]{inner: m}, nil
}

// MustNew is the panic variant of [New]. It is intended for top-level
// program initialisation (main / init), where the only sensible
// response to a configuration-load failure is to abort startup with a
// loud, deterministic panic:
//
//	var Config = fastconf.MustNew[AppConfig](context.Background(),
//	    fastconf.WithDir("conf.d"),
//	    fastconf.WithProvider(provider.NewEnv("APP_")),
//	)
//
// Long-running servers / daemons should continue to use [New] so they
// can decide whether to fall back to built-in defaults or keep serving
// the previous snapshot. MustNew deliberately omits MustGet /
// MustReload variants:
//
//   - [Manager.Get] on a successfully constructed manager never
//     returns nil — New runs the initial reload before returning, so
//     the snapshot is always populated.
//   - [Manager.Reload] failures are runtime events; panicking on a
//     network blip would violate the framework's failure-safe contract.
//   - [Extract] is nil-safe by design and cannot fail.
//
// The panic message wraps the underlying error so `recover` / panic
// reporters surface the original cause.
func MustNew[T any](ctx context.Context, opts ...Option) *Manager[T] {
	m, err := New[T](ctx, opts...)
	if err != nil {
		panic(fmt.Errorf("fastconf.MustNew: %w", err))
	}
	return m
}

func (m *Manager[T]) Get() *T {
	if m == nil || m.inner == nil {
		return nil
	}
	return m.inner.Get()
}

func (m *Manager[T]) Snapshot() *State[T] {
	if m == nil || m.inner == nil {
		return nil
	}
	return wrapState(m.inner.Snapshot())
}

func (m *Manager[T]) Close() error {
	if m == nil || m.inner == nil {
		return nil
	}
	return m.inner.Close()
}

func (m *Manager[T]) Errors() <-chan ReloadError {
	if m == nil || m.inner == nil {
		ch := make(chan ReloadError)
		close(ch)
		return ch
	}
	return m.inner.Errors()
}

func (m *Manager[T]) Reload(ctx context.Context, opts ...ReloadOption) error {
	if m == nil || m.inner == nil {
		return fcerr.ErrClosed
	}
	return m.inner.Reload(ctx, opts...)
}

func (m *Manager[T]) Plan() *PlanBuilder[T] {
	if m == nil || m.inner == nil {
		return &PlanBuilder[T]{}
	}
	return &PlanBuilder[T]{inner: m.inner.Plan()}
}

func (m *Manager[T]) Replay() *Replay[T] {
	if m == nil || m.inner == nil {
		return &Replay[T]{}
	}
	return &Replay[T]{inner: m.inner.Replay()}
}

func (m *Manager[T]) Watcher() *Watcher[T] {
	if m == nil || m.inner == nil {
		return &Watcher[T]{}
	}
	return &Watcher[T]{inner: m.inner.Watcher()}
}

type ReloadOption = imanager.ReloadOption

func WithSourceOverride(override map[string]any) ReloadOption {
	return imanager.WithSourceOverride(override)
}

func WithReloadReason(reason string) ReloadOption {
	return imanager.WithReloadReason(reason)
}

type ValidatorReport = imanager.ValidatorReport

type PlanResult[T any] struct {
	Proposed   *State[T]
	Diff       []DiffEntry
	Validators []ValidatorReport
	Policies   []policy.Violation
}

type PlanBuilder[T any] struct {
	inner *imanager.PlanBuilder[T]
}

func (b *PlanBuilder[T]) WithHostname(host string) *PlanBuilder[T] {
	if b != nil && b.inner != nil {
		b.inner = b.inner.WithHostname(host)
	}
	return b
}

func (b *PlanBuilder[T]) Run(ctx context.Context) (*PlanResult[T], error) {
	if b == nil || b.inner == nil {
		return nil, fmt.Errorf("fastconf: nil manager")
	}
	res, err := b.inner.Run(ctx)
	if err != nil {
		return nil, err
	}
	return &PlanResult[T]{
		Proposed:   wrapState(res.Proposed),
		Diff:       res.Diff,
		Validators: res.Validators,
		Policies:   res.Policies,
	}, nil
}

type Replay[T any] struct {
	inner *imanager.Replay[T]
}

func (r *Replay[T]) List() []*State[T] {
	if r == nil || r.inner == nil {
		return nil
	}
	return wrapStates(r.inner.List())
}

func (r *Replay[T]) Rollback(target *State[T]) error {
	if r == nil || r.inner == nil {
		return imanager.ErrHistoryDisabled
	}
	return r.inner.Rollback(unwrapState(target))
}

type Watcher[T any] struct {
	inner *imanager.Watcher[T]
}

func (w *Watcher[T]) Pause() {
	if w != nil && w.inner != nil {
		w.inner.Pause()
	}
}

func (w *Watcher[T]) Resume() {
	if w != nil && w.inner != nil {
		w.inner.Resume()
	}
}

func (w *Watcher[T]) Paused() bool {
	return w != nil && w.inner != nil && w.inner.Paused()
}

var ErrUnknownGeneration = imanager.ErrUnknownGeneration
var ErrHistoryDisabled = imanager.ErrHistoryDisabled

func Subscribe[T any, M any](m *Manager[T], extract func(*T) *M, fn func(old, new *M)) (cancel func()) {
	if m == nil || m.inner == nil {
		return func() {}
	}
	return imanager.Subscribe[T, M](m.inner, extract, fn)
}

type TenantManager[T any] struct {
	inner *itenant.Manager[T]
}

func NewTenantManager[T any]() *TenantManager[T] {
	return &TenantManager[T]{inner: itenant.New[T]()}
}

func (tm *TenantManager[T]) Add(ctx context.Context, id string, opts ...Option) (*Manager[T], error) {
	if tm == nil || tm.inner == nil {
		return nil, fmt.Errorf("fastconf: nil TenantManager")
	}
	m, err := tm.inner.Add(ctx, id, opts...)
	if err != nil {
		return nil, err
	}
	return &Manager[T]{inner: m}, nil
}

func (tm *TenantManager[T]) Get(id string) (*Manager[T], error) {
	if tm == nil || tm.inner == nil {
		return nil, fmt.Errorf("%w: %q", itenant.ErrUnknownTenant, id)
	}
	m, err := tm.inner.Get(id)
	if err != nil {
		return nil, err
	}
	return &Manager[T]{inner: m}, nil
}

func (tm *TenantManager[T]) Has(id string) bool {
	return tm != nil && tm.inner != nil && tm.inner.Has(id)
}

func (tm *TenantManager[T]) Remove(id string) error {
	if tm == nil || tm.inner == nil {
		return fmt.Errorf("%w: %q", itenant.ErrUnknownTenant, id)
	}
	return tm.inner.Remove(id)
}

func (tm *TenantManager[T]) Tenants() []string {
	if tm == nil || tm.inner == nil {
		return nil
	}
	return tm.inner.Tenants()
}

func (tm *TenantManager[T]) Close() error {
	if tm == nil || tm.inner == nil {
		return nil
	}
	return tm.inner.Close()
}

func wrapStates[T any](states []*istate.State[T]) []*State[T] {
	if states == nil {
		return nil
	}
	out := make([]*State[T], len(states))
	for i, s := range states {
		out[i] = wrapState(s)
	}
	return out
}

var ErrTenantExists = itenant.ErrTenantExists
var ErrUnknownTenant = itenant.ErrUnknownTenant
