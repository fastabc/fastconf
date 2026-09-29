package fastconf

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/providers/source"
)

func TestLoadRunsOneShotWithoutConfigDirectory(t *testing.T) {
	s, err := Load[struct {
		Port int `json:"port"`
	}](context.Background(), WithProvider(source.NewBytes("inline", "json", []byte(`{"port": 8080}`))))
	if err != nil {
		t.Fatal(err)
	}
	if s == nil || s.Value().Port != 8080 {
		t.Fatalf("snapshot = %#v", s)
	}
}

// TestLoadHonorsFileDiscoveryOptions verifies that Load resolves the same option
// set as New, including WithFS / WithDir / WithProfile.
func TestLoadHonorsFileDiscoveryOptions(t *testing.T) {
	fs := fstest.MapFS{
		"cfg/base/00.yaml":          &fstest.MapFile{Data: []byte("port: 1\n")},
		"cfg/overlays/prod/00.yaml": &fstest.MapFile{Data: []byte("port: 2\n")},
	}
	s, err := Load[struct {
		Port int `json:"port"`
	}](context.Background(), WithFS(fs), WithDir("cfg"), WithProfile(Profile{Names: []string{"prod"}}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Value().Port != 2 {
		t.Fatalf("port = %d; want 2 from the prod overlay", s.Value().Port)
	}
}

func TestLoadReturnsNoSnapshotOnFailure(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		data string
		want error
	}{
		{"source-parse", context.Background(), `{`, ErrProvider},
		{"decode", context.Background(), `{"port":{}}`, ErrDecode},
		{"validation", context.Background(), `{"port":0}`, ErrInvalid},
		{"canceled", canceled, `{"port":8080}`, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Load[struct {
				Port int `json:"port" fc:"min=1"`
			}](tc.ctx, WithProvider(source.NewBytes("inline", "json", []byte(tc.data))))
			if s != nil || !errors.Is(err, tc.want) {
				t.Fatalf("Load = %v, %v; want nil, %v", s, err, tc.want)
			}
		})
	}
}

func TestShutdownRetriesKeepGoroutinesBounded(t *testing.T) {
	m := newBenchManager(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		_ = m.Close()
	})
	cancelSub := Subscribe(m, func(c *benchCfg) *int { return &c.A }, func(_, _ *int) {
		close(entered)
		<-release
	})
	defer cancelSub()
	reloaded := make(chan error, 1)
	go func() {
		reloaded <- m.Reload(context.Background(), WithOverride(map[string]any{"a": 2}))
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("subscriber did not start")
	}
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := m.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown with blocked subscriber = %v", err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	for i := 0; i < 128; i++ {
		if err := m.Shutdown(canceled); !errors.Is(err, context.Canceled) {
			t.Fatalf("Shutdown retry %d = %v", i, err)
		}
	}
	// Allow unrelated runtime workers, but not a new worker for every retry.
	if growth := runtime.NumGoroutine() - before; growth > 8 {
		t.Fatalf("128 shutdown retries added %d goroutines", growth)
	}
	select {
	case _, ok := <-m.Errors():
		if !ok {
			t.Fatal("Errors closed before the subscriber finished")
		}
	default:
	}
	releaseOnce.Do(func() { close(release) })
	finish, cancelFinish := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFinish()
	if err := m.Shutdown(finish); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-m.Errors(); ok {
		t.Fatal("Errors remains open after shutdown completed")
	}
	select {
	case err := <-reloaded:
		if err != nil && !errors.Is(err, ErrClosed) {
			t.Fatalf("in-flight Reload = %v", err)
		}
	case <-finish.Done():
		t.Fatal("in-flight Reload did not finish")
	}
}

func TestShutdownCompletedIgnoresCanceledContext(t *testing.T) {
	m := newBenchManager(t)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 32; i++ {
		if err := m.Shutdown(ctx); err != nil {
			t.Fatalf("completed Shutdown retry %d = %v", i, err)
		}
	}
}

func TestErrFastConfHierarchy(t *testing.T) {
	pe := &PolicyError{}
	if !errors.Is(pe, ErrInvalid) {
		t.Fatal("PolicyError must satisfy Is(ErrInvalid)")
	}
	if !errors.Is(pe, ErrFastConf) {
		t.Fatal("PolicyError must satisfy Is(ErrFastConf)")
	}
}

// TestCoreSentinelsChainToErrFastConf verifies the error table: eight
// reload sentinels plus the two History misuse errors, all under the
// umbrella.
func TestCoreSentinelsChainToErrFastConf(t *testing.T) {
	sentinels := map[string]error{
		"ErrNoSources": ErrNoSources,
		"ErrDecode":    ErrDecode,
		"ErrMerge":     ErrMerge,
		"ErrTransform": ErrTransform,
		"ErrProvider":  ErrProvider,
		"ErrInvalid":   ErrInvalid,
		"ErrClosed":    ErrClosed,
		"ErrTooLarge":  ErrTooLarge,
		// F7: every exported sentinel sits under the umbrella.
		"ErrHistoryDisabled":   ErrHistoryDisabled,
		"ErrUnknownGeneration": ErrUnknownGeneration,
	}
	for name, e := range sentinels {
		if !errors.Is(e, ErrFastConf) {
			t.Errorf("%s does not satisfy Is(ErrFastConf)", name)
		}
	}
	if contracts.ErrConfigTooLarge != ErrTooLarge {
		t.Error("contracts.ErrConfigTooLarge must be ErrTooLarge")
	}
}

func TestErrMergeCoversPatchFailure(t *testing.T) {
	_, err := New[map[string]any](context.Background(), WithFS(fstest.MapFS{
		"conf.d/base/00.yaml":       &fstest.MapFile{Data: []byte("a: 1\n")},
		"conf.d/base/10.patch.json": &fstest.MapFile{Data: []byte(`[{"op":"remove","path":"/missing"}]`)},
	}))
	if !errors.Is(err, ErrMerge) {
		t.Fatalf("want ErrMerge for a failing patch, got %v", err)
	}
}

var errFailingProvider = errors.New("provider boom")

type failingProvider struct{}

func (failingProvider) Name() string { return "failing" }
func (failingProvider) Load(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{}, errFailingProvider
}
func (failingProvider) Watch(context.Context, string) (<-chan contracts.Event, error) {
	return nil, nil
}

func TestErrProviderClassification(t *testing.T) {
	_, err := New[map[string]any](context.Background(),
		WithFS(emptyFS()),
		WithProvider(failingProvider{}),
	)
	if !errors.Is(err, ErrProvider) {
		t.Fatalf("want ErrProvider, got %v", err)
	}
	if errors.Is(err, ErrDecode) {
		t.Fatalf("provider load failure must not classify as ErrDecode: %v", err)
	}
}

var errFailingGenerator = errors.New("generator boom")

type failingGenerator struct{}

func (failingGenerator) Name() string { return "failing" }
func (failingGenerator) Generate(context.Context) ([]contracts.RawLayer, error) {
	return nil, errFailingGenerator
}

func TestGeneratorFailureIsErrProvider(t *testing.T) {
	_, err := New[map[string]any](context.Background(),
		WithFS(emptyFS()),
		WithGenerator(failingGenerator{}),
	)
	if !errors.Is(err, ErrProvider) {
		t.Fatalf("want ErrProvider for a generator failure, got %v", err)
	}
	if errors.Is(err, ErrDecode) {
		t.Fatalf("generator failure must not classify as ErrDecode: %v", err)
	}
}

type finalHashConfig struct {
	Value int `json:"value"`
}
type finalHashDecoded finalHashConfig

var finalHashVersion atomic.Int64

func (c *finalHashDecoded) UnmarshalJSON([]byte) error {
	c.Value = int(finalHashVersion.Load())
	return nil
}

func TestHashTracksFinalStructAndMap(t *testing.T) {
	t.Run("struct-defaults", func(t *testing.T) {
		version := 1
		checkFinalHash(t, WithValidate(func(c *finalHashConfig) error { c.Value = version; return nil }), func() { version = 2 }, func(c *finalHashConfig) bool { return c.Value == 2 })
	})
	t.Run("map-defaults", func(t *testing.T) {
		version := 1
		checkFinalHash(t, WithValidate(func(c *map[string]int) error { (*c)["value"] = version; return nil }), func() { version = 2 }, func(c *map[string]int) bool { return (*c)["value"] == 2 })
	})
	t.Run("custom-decoder", func(t *testing.T) {
		finalHashVersion.Store(1)
		checkFinalHash(t, WithValidate(func(*finalHashDecoded) error { return nil }), func() { finalHashVersion.Store(2) }, func(c *finalHashDecoded) bool { return c.Value == 2 })
	})
}

func checkFinalHash[T any](t *testing.T, option Option, advance func(), valid func(*T) bool) {
	t.Helper()
	m, err := New[T](context.Background(), WithFS(emptyFS()), WithProvider(source.NewBytes("input", "json", []byte(`{}`))), option)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	before := m.Snapshot()
	advance()
	plan, err := m.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !valid(plan.Proposed.Value()) {
		t.Fatal("preview value incorrect")
	}
	if m.Snapshot() != before {
		t.Fatal("preview published")
	}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := m.Snapshot()
	if !valid(after.Value()) || after.Generation() != before.Generation()+1 {
		t.Fatal("final computed value was not published")
	}
	b, err := json.Marshal(after.Value())
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(b)
	if after.Hash() != want || plan.Proposed.Hash() != want {
		t.Fatal("hash does not describe final value")
	}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot() != after {
		t.Fatal("same final value published twice")
	}
	if err := m.Reload(context.Background(), WithOverride(map[string]any{"ignored": true})); err == nil {
		// Struct decoders ignore unknown input; map[int] deliberately rejects bool.
		if m.Snapshot() != after {
			t.Fatal("ignored input changed final hash")
		}
	}
}

type optionalMetaChild struct {
	Port int `json:"port" fc:"min=1,default=9"`
}
type optionalMetaConfig struct {
	Child *optionalMetaChild `json:"child"`
}
type requiredMetaConfig struct {
	Child *optionalMetaChild `json:"child" fc:"required"`
}

func TestOptionalMetadataParentStaysNil(t *testing.T) {
	m, err := New[optionalMetaConfig](context.Background(), WithFS(emptyFS()), WithProvider(source.NewBytes("input", "json", []byte(`{}`))))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	if m.Get().Child != nil {
		t.Fatal("default instantiated absent optional parent")
	}
	before := m.Snapshot()
	if err := m.Reload(context.Background(), WithOverride(map[string]any{"child": map[string]any{"port": -1}})); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid existing child accepted")
	}
	if m.Snapshot() != before {
		t.Fatal("failed reload published")
	}
	if err := m.Reload(context.Background(), WithOverride(map[string]any{"child": map[string]any{}})); err != nil {
		t.Fatal(err)
	}
	if m.Get().Child == nil || m.Get().Child.Port != 9 {
		t.Fatal("existing parent did not receive default")
	}
}

func TestRequiredMetadataParentRejectsNil(t *testing.T) {
	_, err := New[requiredMetaConfig](context.Background(), WithFS(emptyFS()), WithProvider(source.NewBytes("input", "json", []byte(`{}`))))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("required parent error=%v", err)
	}
}

func TestMetadataPointerTarget(t *testing.T) {
	m, err := New[*optionalMetaConfig](context.Background(), WithFS(emptyFS()), WithProvider(source.NewBytes("input", "json", []byte(`{}`))))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	if *m.Get() == nil || (*m.Get()).Child != nil {
		t.Fatal("pointer target changed")
	}
}

type failingWatchProvider struct{ attempted chan struct{} }

func (p failingWatchProvider) Name() string { return "failing" }
func (p failingWatchProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: 0}
}
func (p failingWatchProvider) Load(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{}, nil
}
func (p failingWatchProvider) Watch(context.Context, string) (<-chan contracts.Event, error) {
	p.attempted <- struct{}{}
	return nil, errors.New("offline")
}

func TestProviderWatch_CancellationStopsBackoff(t *testing.T) {
	m, err := newManager[map[string]any](context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.lifetimeCancel()
	p := failingWatchProvider{make(chan struct{}, 1)}
	done := make(chan struct{})
	m.bgWG.Add(1)
	go func() { m.runProviderWatcher(m.lifetime, p); close(done) }()
	<-p.attempted
	// Give Watch time to return and enter its 250ms minimum backoff.
	time.Sleep(20 * time.Millisecond)
	m.lifetimeCancel()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("provider watcher stayed in backoff after cancellation")
	}
}
