package scan

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf/contracts"
)

func TestLoadMeta_NormalizesRoot(t *testing.T) {
	fsys := fstest.MapFS{
		"_meta.yaml":        {Data: []byte("spec: {strict: true}\n")},
		"conf.d/_meta.yaml": {Data: []byte("spec: {strict: true}\n")},
	}
	for _, root := range []string{"", ".", "conf.d/", "./conf.d"} {
		t.Run(root, func(t *testing.T) {
			b, err := LoadMeta(fsys, root)
			if err != nil || string(b) != "spec: {strict: true}\n" {
				t.Fatalf("root %q: meta=%q err=%v", root, b, err)
			}
		})
	}
}

// An overlay directory that exceeds its priority stride would bleed into
// the next overlay's band and interleave by name; the scan must fail loud.
func TestScan_OverlayStrideOverflow(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00-a.yaml": &fstest.MapFile{Data: []byte("k: 1")},
	}
	for i := range overlayStride + 1 {
		fs[fmt.Sprintf("conf.d/overlays/prod/%03d.yaml", i)] = &fstest.MapFile{Data: []byte("k: 1")}
	}
	var seenErr error
	Scan("conf.d", ScanOptions{Profiles: []string{"prod"}, FS: fs})(func(_ Layer, err error) bool {
		if err != nil {
			seenErr = err
			return false
		}
		return true
	})
	if seenErr == nil {
		t.Fatalf("expected stride-overflow error for a %d-file overlay", overlayStride+1)
	}
}

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
	cases := map[string]string{
		".yaml": "yaml",
		".yml":  "yaml",
		".json": "json",
		".toml": "toml",
		".ini":  "ini",
		".INI":  "ini",
		".conf": "",
		".hcl":  "",
		"":      "",
	}
	for ext, want := range cases {
		if got := codecOf(ext); got != want {
			t.Errorf("codecOf(%q) = %q, want %q", ext, got, want)
		}
	}
}

// TestScan_ProfileExpressionOverlayMatching exercises
// collectOverlaysByExpression + overlayMatches via the public Scan.
func TestScan_ProfileExpressionOverlayMatching(t *testing.T) {
	// Two overlay dirs: "canary" and "prod".
	// canary has a _meta.yaml with match: "canary".
	// prod has no _meta.yaml, so it matches if its name is in Profiles.
	fs := fstest.MapFS{
		"conf.d/base/00.yaml":               &fstest.MapFile{Data: []byte("k: base")},
		"conf.d/overlays/canary/_meta.yaml": &fstest.MapFile{Data: []byte("match: canary\n")},
		"conf.d/overlays/canary/10.yaml":    &fstest.MapFile{Data: []byte("k: canary")},
		"conf.d/overlays/prod/10.yaml":      &fstest.MapFile{Data: []byte("k: prod")},
	}

	for _, tc := range []struct {
		name           string
		profiles, want []string
	}{
		{"canary_active", []string{"canary"}, []string{"", "canary"}},
		{"prod_active", []string{"prod"}, []string{"", "prod"}},
		{"no_profiles_no_overlay", nil, []string{""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			layers, err := collectLayers("conf.d", ScanOptions{Profiles: tc.profiles, FS: fs})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, layer := range layers {
				got = append(got, layer.Profile)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("profiles=%v want=%v", got, tc.want)
			}
		})
	}
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

func TestScan_PriorityWindowBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, dir      string
		base, capacity int
		opt            ScanOptions
	}{
		{"base", "base", BandFileBase, baseBandWidth, ScanOptions{}},
		{"profile", "overlays/prod", BandFileOverlay, overlayStride, ScanOptions{Profiles: []string{"prod"}}},
		{"axis", "region/eu", BandExtraOverlay, overlayStride, ScanOptions{ExtraOverlays: []ExtraOverlay{{Dir: "region/eu", Profile: "region:eu", Priority: BandExtraOverlay}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := fstest.MapFS{}
			for i := range tc.capacity {
				fs[fmt.Sprintf("conf.d/%s/%04d.yaml", tc.dir, i)] = &fstest.MapFile{Data: []byte("k: value")}
			}
			// Ignored entries must consume no priority offsets.
			for i := range 1100 {
				fs[fmt.Sprintf("conf.d/%s/.ignored%04d", tc.dir, i)] = &fstest.MapFile{}
			}
			fs[fmt.Sprintf("conf.d/%s/0000.txt", tc.dir)] = &fstest.MapFile{}
			fs[fmt.Sprintf("conf.d/%s/0000-dir/nested.yaml", tc.dir)] = &fstest.MapFile{}
			tc.opt.FS = fs
			var priorities []int
			for layer, err := range Scan("conf.d", tc.opt) {
				if err != nil {
					t.Fatal(err)
				}
				priorities = append(priorities, layer.Priority)
			}
			if len(priorities) != tc.capacity || priorities[0] != tc.base || priorities[len(priorities)-1] != tc.base+tc.capacity-1 {
				t.Fatalf("unexpected priority bounds: %v", priorities)
			}
			fs[fmt.Sprintf("conf.d/%s/extra.yaml", tc.dir)] = &fstest.MapFile{Data: []byte("k: overflow")}
			var scanErr error
			for _, err := range Scan("conf.d", tc.opt) {
				if err != nil {
					scanErr = err
					break
				}
			}
			if scanErr == nil {
				t.Fatal("overflow accepted")
			}
		})
	}
}

func TestScan_MatchedOverlayCountBound(t *testing.T) {
	fs := fstest.MapFS{}
	opt := ScanOptions{FS: fs}
	for i := range 11 {
		name := fmt.Sprintf("profile%02d", i)
		opt.Profiles = append(opt.Profiles, name)
		fs["conf.d/overlays/"+name+"/00.yaml"] = &fstest.MapFile{Data: []byte("k: value")}
		var scanErr error
		for layer, err := range Scan("conf.d", opt) {
			if err != nil {
				scanErr = err
				break
			}
			if layer.Priority >= BandExtraOverlay {
				t.Fatal("profile escaped its band")
			}
		}
		if (scanErr != nil) != (i == 10) {
			t.Fatalf("%d matched profiles: %v", i+1, scanErr)
		}
	}
}

func collectLayers(root string, opt ScanOptions) ([]Layer, error) {
	var layers []Layer
	for l, err := range Scan(root, opt) {
		if err != nil {
			return nil, err
		}
		layers = append(layers, l)
	}
	return layers, nil
}

func TestScan_FileSizeLimit(t *testing.T) {
	for _, n := range []int{int(contracts.DefaultMaxBodyBytes), int(contracts.DefaultMaxBodyBytes) + 1} {
		fsys := fstest.MapFS{"conf.d/base/a.yaml": {Data: bytes.Repeat([]byte("#"), n)}}
		_, err := collectLayers("conf.d", ScanOptions{FS: fsys})
		if n == int(contracts.DefaultMaxBodyBytes) && err != nil {
			t.Fatal(err)
		}
		if n > int(contracts.DefaultMaxBodyBytes) && !errors.Is(err, contracts.ErrTooLarge) {
			t.Fatalf("size %d: %v", n, err)
		}
	}
}

func TestMeta_RejectsSpecialAndOversizedFiles(t *testing.T) {
	for _, mode := range []fs.FileMode{fs.ModeNamedPipe, 0} {
		data := []byte("match: prod")
		if mode == 0 {
			data = bytes.Repeat([]byte("#"), int(contracts.DefaultMaxBodyBytes)+1)
		}
		for _, overlay := range []bool{false, true} {
			fsys := fstest.MapFS{"conf.d/_meta.yaml": {Data: data, Mode: mode}}
			var err error
			if overlay {
				fsys["conf.d/overlays/prod/_meta.yaml"] = fsys["conf.d/_meta.yaml"]
				_, err = overlayMatches(fsys, "conf.d", "overlays/prod", "prod", NewSet("prod"))
			} else {
				_, err = LoadMeta(fsys, "conf.d")
			}
			if err == nil {
				t.Fatalf("mode=%v overlay=%v: accepted invalid metadata", mode, overlay)
			}
			if mode == 0 && !errors.Is(err, contracts.ErrTooLarge) {
				t.Fatalf("oversized metadata: %v", err)
			}
		}
	}
}

type hintedInfo struct {
	fs.FileInfo
	size int64
}

func (i hintedInfo) Size() int64 { return i.size }

type hintedFS struct {
	fstest.MapFS
	size int64
}

func (f hintedFS) Stat(name string) (fs.FileInfo, error) {
	info, err := f.MapFS.Stat(name)
	if err != nil {
		return nil, err
	}
	return hintedInfo{FileInfo: info, size: f.size}, nil
}

func TestScan_FileSizeHintDoesNotTruncateOrBypassLimit(t *testing.T) {
	for _, tc := range []struct {
		name string
		hint int64
		size int
	}{
		{"grew", 1, 100},
		{"shrank", 100, 1},
		{"unknown", -1, 100},
		{"grew to limit", 1, int(MaxFileBytes)},
		{"grew past limit", 1, int(MaxFileBytes) + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := bytes.Repeat([]byte("#"), tc.size)
			fsys := hintedFS{MapFS: fstest.MapFS{"conf.d/base/a.yaml": {Data: data}}, size: tc.hint}
			layers, err := collectLayers("conf.d", ScanOptions{FS: fsys})
			if tc.size > int(MaxFileBytes) {
				if !errors.Is(err, contracts.ErrTooLarge) {
					t.Fatalf("limit error=%v", err)
				}
				return
			}
			if err != nil || len(layers) != 1 || !bytes.Equal(layers[0].Bytes, data) {
				t.Fatalf("size hint truncated input: layers=%d err=%v", len(layers), err)
			}
		})
	}
}

type brokenReadFile struct {
	fs.File
	err error
}

func (f brokenReadFile) Read(b []byte) (int, error) { return copy(b, "a:"), f.err }

func TestReadLayerDataPreservesReadErrors(t *testing.T) {
	for _, want := range []error{io.ErrUnexpectedEOF, fs.ErrPermission} {
		if _, err := readLayerData(brokenReadFile{err: want}, 10); !errors.Is(err, want) {
			t.Fatalf("read error=%v want=%v", err, want)
		}
	}
}
