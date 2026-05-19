package decoder_test

import (
	"context"
	"reflect"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
)

type typedCfg struct {
	Timeout time.Duration `json:"timeout"`
	Server  struct {
		ReadTimeout time.Duration `json:"readTimeout"`
	} `json:"server"`
}

func TestTypedHook_DurationFromYAML(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(`
timeout: 30s
server:
  readTimeout: 1500ms
`)},
	}
	mgr, err := fastconf.New[typedCfg](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	cfg := mgr.Get()
	if cfg.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", cfg.Timeout)
	}
	if cfg.Server.ReadTimeout != 1500*time.Millisecond {
		t.Errorf("ReadTimeout = %v, want 1500ms", cfg.Server.ReadTimeout)
	}
}

// Custom hook: parse a string into an int that the JSON decoder will
// natively accept for the rune-typed Mood field below.
type moodHook struct{}

type Mood int

func (moodHook) Match(t reflect.Type) bool {
	return t == reflect.TypeOf(Mood(0))
}

func (moodHook) Convert(raw any) (any, error) {
	switch v := raw.(type) {
	case string:
		switch v {
		case "happy":
			return 1, nil
		case "sad":
			return -1, nil
		default:
			return 0, nil
		}
	default:
		return raw, nil
	}
}

type withMoodCfg struct {
	Mood Mood `json:"mood"`
}

func TestWithTypedHook_Custom(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("mood: happy\n")},
	}
	mgr, err := fastconf.New[withMoodCfg](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
		fastconf.WithTypedHook(moodHook{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	if mgr.Get().Mood != 1 {
		t.Errorf("Mood = %v, want 1", mgr.Get().Mood)
	}
}

func TestWithoutDefaultTypedHooks_BreaksDuration(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("timeout: 30s\n")},
	}
	// Without the Duration hook the string "30s" cannot be unmarshalled
	// into time.Duration — verify the decode failure surfaces.
	_, err := fastconf.New[typedCfg](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
		fastconf.WithoutDefaultTypedHooks(),
	)
	if err == nil {
		t.Error("expected decode error when defaults disabled")
	}
}

// Regression: YAML "30s" decoded into a time.Duration field silently
// dropped to 0 because encoding/json refuses the string→Duration
// conversion. Typed hooks bridge the gap; lock the behavior in so a
// future refactor cannot regress the contract.
type durationFromYAMLCfg struct {
	Timeout time.Duration `json:"timeout"`
}

func TestTypedHook_DurationFromYAMLString(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("timeout: 250ms\n")},
	}
	mgr, err := fastconf.New[durationFromYAMLCfg](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	if got := mgr.Get().Timeout; got != 250*time.Millisecond {
		t.Errorf("string→Duration regression: Timeout = %v, want 250ms", got)
	}
}
