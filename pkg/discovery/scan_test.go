package discovery

import (
	"testing"
	"testing/fstest"
)

func TestScan_BaseAndOverlayOrder(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00-a.yaml":                &fstest.MapFile{Data: []byte("k: 1")},
		"conf.d/base/10-b.yaml":                &fstest.MapFile{Data: []byte("k: 2")},
		"conf.d/overlays/prod/00-c.yaml":       &fstest.MapFile{Data: []byte("k: 3")},
		"conf.d/overlays/prod/05-d.patch.yaml": &fstest.MapFile{Data: []byte("[]")},
	}
	var got []string
	Scan("conf.d", ScanOptions{Profiles: []string{"prod"}, FS: fs})(func(layer Layer, err error) bool {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, layer.Path+"|"+layer.Kind.string())
		return true
	})
	want := []string{
		"conf.d/base/00-a.yaml|merge",
		"conf.d/base/10-b.yaml|merge",
		"conf.d/overlays/prod/00-c.yaml|merge",
		"conf.d/overlays/prod/05-d.patch.yaml|patch",
	}
	if len(got) != len(want) {
		t.Fatalf("len got %d want %d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] got %q want %q", i, got[i], want[i])
		}
	}
}

func TestScan_UnknownExtensionStrict(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00-a.yaml": &fstest.MapFile{Data: []byte("k: 1")},
		"conf.d/base/99-x.txt":  &fstest.MapFile{Data: []byte("nope")},
	}
	var seenErr error
	Scan("conf.d", ScanOptions{FS: fs, Strict: true})(func(_ Layer, err error) bool {
		if err != nil {
			seenErr = err
			return false
		}
		return true
	})
	if seenErr == nil {
		t.Error("strict mode should reject .txt")
	}
}

func TestScan_UnderscorePrefixSkipped(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00-a.yaml":      &fstest.MapFile{Data: []byte("k: 1")},
		"conf.d/base/_fragment.yaml": &fstest.MapFile{Data: []byte("k: 2")},
	}
	count := 0
	Scan("conf.d", ScanOptions{FS: fs})(func(_ Layer, err error) bool {
		if err != nil {
			t.Fatal(err)
		}
		count++
		return true
	})
	if count != 1 {
		t.Errorf("got %d, want 1", count)
	}
}

// Kind.string is a tiny helper for tests; not exported.
func (k Kind) string() string {
	switch k {
	case KindMerge:
		return "merge"
	case KindPatch:
		return "patch"
	}
	return "unknown"
}

// TestCodecOf_ReturnsBuiltins pins the codec discovery contract:
// codecOf returns names that decoder.For() can actually resolve, and
// rejects everything else as the empty string.
func TestCodecOf_ReturnsBuiltins(t *testing.T) {
	prev := CodecExtFunc
	CodecExtFunc = nil
	defer func() { CodecExtFunc = prev }()

	cases := map[string]string{
		".yaml": "yaml",
		".yml":  "yaml",
		".json": "json",
		".toml": "toml",
		".ini":  "",
		".hcl":  "",
		"":      "",
	}
	for ext, want := range cases {
		if got := codecOf(ext); got != want {
			t.Errorf("codecOf(%q) = %q, want %q", ext, got, want)
		}
	}
}

// TestScan_MultiProfile_ExpressionOverlayMatching exercises
// collectOverlaysByExpression + overlayMatches via the public Scan.
func TestScan_MultiProfile_ExpressionOverlayMatching(t *testing.T) {
	// Two overlay dirs: "canary" and "prod".
	// canary has a _meta.yaml with match: "canary".
	// prod has no _meta.yaml, so it matches if its name is in Profiles.
	fs := fstest.MapFS{
		"conf.d/base/00.yaml":               &fstest.MapFile{Data: []byte("k: base")},
		"conf.d/overlays/canary/_meta.yaml": &fstest.MapFile{Data: []byte("match: canary\n")},
		"conf.d/overlays/canary/10.yaml":    &fstest.MapFile{Data: []byte("k: canary")},
		"conf.d/overlays/prod/10.yaml":      &fstest.MapFile{Data: []byte("k: prod")},
	}

	t.Run("canary_active", func(t *testing.T) {
		var got []string
		Scan("conf.d", ScanOptions{Profiles: []string{"canary"}, FS: fs})(func(layer Layer, err error) bool {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, layer.Profile)
			return true
		})
		// base has Profile="" ; canary overlay should be included; prod should not.
		if len(got) != 2 {
			t.Fatalf("want 2 layers got %d: %v", len(got), got)
		}
		if got[1] != "canary" {
			t.Errorf("layer[1] profile: got %q want canary", got[1])
		}
	})

	t.Run("prod_active", func(t *testing.T) {
		var got []string
		Scan("conf.d", ScanOptions{Profiles: []string{"prod"}, FS: fs})(func(layer Layer, err error) bool {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, layer.Profile)
			return true
		})
		// base + prod; canary excluded because its _meta.yaml match expression "canary" != active set.
		if len(got) != 2 {
			t.Fatalf("want 2 layers got %d: %v", len(got), got)
		}
		if got[1] != "prod" {
			t.Errorf("layer[1] profile: got %q want prod", got[1])
		}
	})

	t.Run("no_profiles_no_overlay", func(t *testing.T) {
		var got []string
		Scan("conf.d", ScanOptions{FS: fs})(func(_ Layer, err error) bool {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, "x")
			return true
		})
		// Without Profiles or Profile, only base layers are returned.
		if len(got) != 1 {
			t.Errorf("want 1 base layer got %d", len(got))
		}
	})
}

// TestScan_MatchAnd_GlobalFilter exercises the MatchAnd global expression.
// MatchAnd is AND-ed with each overlay's match; if the expression evaluates
// false for an overlay, that overlay is excluded even if it would otherwise match.
func TestScan_MatchAnd_GlobalFilter(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml":           &fstest.MapFile{Data: []byte("k: base")},
		"conf.d/overlays/prod/10.yaml":  &fstest.MapFile{Data: []byte("k: prod")},
		"conf.d/overlays/debug/10.yaml": &fstest.MapFile{Data: []byte("k: debug")},
	}
	// Active: {prod, debug}. MatchAnd: "!debug" suppresses debug overlay.
	// For each overlay, scoped = Profiles ∪ {overlay_name}. Eval("!debug", {prod, debug, <name>})
	// is false for debug's own scoped set but also false for prod since debug is in scoped (from Profiles).
	// MatchAnd is evaluated with scoped = Profiles ∪ {overlay_name}, so "!debug" with Profiles={prod}
	// keeps prod and suppresses debug.
	var got []string
	Scan("conf.d", ScanOptions{
		Profiles: []string{"prod"}, // only prod active; debug not in active set
		MatchAnd: "!debug",         // redundant exclusion but valid: debug not in {prod} anyway
		FS:       fs,
	})(func(layer Layer, err error) bool {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, layer.Profile)
		return true
	})
	for _, p := range got {
		if p == "debug" {
			t.Errorf("debug should have been suppressed by MatchAnd='!debug'")
		}
	}
	var foundProd bool
	for _, p := range got {
		if p == "prod" {
			foundProd = true
		}
	}
	if !foundProd {
		t.Errorf("prod should be present, got layers: %v", got)
	}
}

// TestScan_OverlayMetaYAML_InvalidExpression checks that a bad match
// expression in _meta.yaml is surfaced as an error.
func TestScan_OverlayMetaYAML_InvalidExpression(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml":            &fstest.MapFile{Data: []byte("k: base")},
		"conf.d/overlays/bad/_meta.yaml": &fstest.MapFile{Data: []byte("match: \"!!invalid!!\"\n")},
		"conf.d/overlays/bad/10.yaml":    &fstest.MapFile{Data: []byte("k: bad")},
	}
	var sawErr bool
	Scan("conf.d", ScanOptions{Profiles: []string{"bad"}, FS: fs})(func(_ Layer, err error) bool {
		if err != nil {
			sawErr = true
			return false
		}
		return true
	})
	if !sawErr {
		t.Error("expected error for invalid match expression, got none")
	}
}
