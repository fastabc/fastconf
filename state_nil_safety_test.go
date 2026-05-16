package fastconf_test

// P2.4: every boundary method on *State[T] must tolerate a nil receiver
// the same way Introspect / MarshalYAML / Sub already do. This file
// pins that contract down so future additions cannot silently regress.

import (
	"errors"
	"testing"

	"github.com/fastabc/fastconf"
)

type nilCfg struct {
	Name string `json:"name"`
}

func TestState_NilSafety(t *testing.T) {
	var s *fastconf.State[nilCfg] // intentionally nil

	t.Run("Redacted", func(t *testing.T) {
		if got := s.Redacted(); got != nil {
			t.Errorf("Redacted on nil: want nil, got %v", got)
		}
	})

	t.Run("Redact", func(t *testing.T) {
		if got := s.Redact(fastconf.DefaultSecretRedactor); got != nil {
			t.Errorf("Redact on nil: want nil, got %v", got)
		}
	})

	t.Run("FeatureRules", func(t *testing.T) {
		if got := s.FeatureRules(); got != nil {
			t.Errorf("FeatureRules on nil: want nil, got %v", got)
		}
	})

	t.Run("Origins", func(t *testing.T) {
		if got := s.Origins(); got != nil {
			t.Errorf("Origins on nil: want nil, got %v", got)
		}
	})

	t.Run("Explain", func(t *testing.T) {
		if got := s.Explain("any.path"); got != nil {
			t.Errorf("Explain on nil: want nil, got %v", got)
		}
	})

	t.Run("Lookup", func(t *testing.T) {
		if got := s.Lookup("any.path"); got != nil {
			t.Errorf("Lookup on nil: want nil, got %v", got)
		}
	})

	t.Run("LookupStrict", func(t *testing.T) {
		got, err := s.LookupStrict("any.path")
		if got != nil {
			t.Errorf("LookupStrict on nil: want nil slice, got %v", got)
		}
		if !errors.Is(err, fastconf.ErrNoOrigin) {
			t.Errorf("LookupStrict on nil: want ErrNoOrigin, got %v", err)
		}
	})

	t.Run("Diff", func(t *testing.T) {
		// nil vs nil → no differences
		if got := s.Diff(nil); len(got) != 0 {
			t.Errorf("nil.Diff(nil): want empty, got %v", got)
		}
		// nil vs nil receiver on either side must not panic
		other := s
		_ = other.Diff(s)
	})

	t.Run("Introspect", func(t *testing.T) {
		// Introspect on a nil State returns a nil *Introspection
		// (documented behaviour). Calling Keys/Settings/At on that nil
		// holder must NOT panic and must return empty.
		ins := s.Introspect()
		if got := ins.Keys(); len(got) != 0 {
			t.Errorf("Introspect.Keys on nil state: want empty, got %v", got)
		}
		if got := ins.Settings(); len(got) != 0 {
			t.Errorf("Introspect.Settings on nil state: want empty, got %v", got)
		}
		if got := ins.At("foo"); len(got) != 0 {
			t.Errorf("Introspect.At on nil state: want empty, got %v", got)
		}
	})

	t.Run("MarshalYAML", func(t *testing.T) {
		b, err := s.MarshalYAML(nil)
		if err != nil {
			t.Fatalf("MarshalYAML on nil: unexpected error %v", err)
		}
		if string(b) != "{}\n" {
			t.Errorf("MarshalYAML on nil: want \"{}\\n\", got %q", b)
		}
		// Same with a redactor — must not panic.
		if _, err := s.MarshalYAML(fastconf.DefaultSecretRedactor); err != nil {
			t.Errorf("MarshalYAML(redactor) on nil: unexpected error %v", err)
		}
	})
}
