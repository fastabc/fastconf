// Package scan scans a configuration root and produces a stream of priority-ordered layers (base,
// overlays, extra overlay axes), and evaluates the profile expressions that select overlays.
package scan

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fastabc/fastconf/codec"
	"gopkg.in/yaml.v3"
)

// Kind describes the merge semantics of a discovered layer. It maps one-to-one to
// fastconf.LayerKind; internal packages cannot import the public package without creating a cycle,
// hence the redefinition.
type Kind uint8

const (
	KindUnknown Kind = iota
	KindMerge
	KindPatch
)

// Layer is a descriptor emitted by the discovery stage. Bytes is read fully into memory (config
// files are typically less than a few MB).
type Layer struct {
	Path     string
	Kind     Kind
	Profile  string
	Priority int
	Codec    string // "yaml" | "json" | "toml"; third-party codecs registered via codec.RegisterExt show up here too.
	Bytes    []byte
}

// ExtraOverlay describes an additional directory to include as file layers after the main base and
// overlay dirs. It is used by the multi-axis overlay feature (see ScanOptions.ExtraOverlays).
type ExtraOverlay struct {
	Dir      string // path relative to the scan root, e.g. "hosts/ua"
	Profile  string // label for provenance reporting, e.g. "host:ua"
	Priority int    // base priority for layers in this directory
}

// ScanOptions controls scan behaviour. The single-profile use case goes through Profiles with one
// element; there is no separate scalar field.
type ScanOptions struct {
	BaseDir       string         // default "base"
	OverlayDir    string         // default "overlays"
	Profiles      []string       // Active profile set; empty = base-only, one element = single-profile, more = multi-axis expression matching.
	MatchAnd      string         // Optional global expression AND-ed with each overlay's match.
	PatchSuffixes []string       // default [".patch.yaml", ".patch.json"]
	Strict        bool           // when true, an unrecognised extension errors instead of being skipped
	FS            fs.FS          // optional virtual filesystem for tests
	ExtraOverlays []ExtraOverlay // Additional axis directories (multi-axis overlay feature).
	// OnDir, when non-nil, is called with every contributing directory (base, matched overlays,
	// extra overlays) before it is read, even when it is empty or missing, so a watcher can
	// subscribe before the contents are observed.
	OnDir func(dir string)
}

func (o *ScanOptions) defaults() {
	if o.BaseDir == "" {
		o.BaseDir = "base"
	}
	if o.OverlayDir == "" {
		o.OverlayDir = "overlays"
	}
	if len(o.PatchSuffixes) == 0 {
		o.PatchSuffixes = []string{".patch.yaml", ".patch.json"}
	}
}

// LayerSeq is the iterator of discovered layers returned by Scan.
type LayerSeq = iter.Seq2[Layer, error]

// Scan walks root and yields layers in base→overlay priority order,
// lexicographic within each tier.
//
// The function returns an iterator instead of []Layer so that callers can
// early-stop (e.g. on a per-layer decode error) and large directory trees
// do not balloon peak memory.
func Scan(root string, opt ScanOptions) LayerSeq {
	opt.defaults()

	return func(yield func(Layer, error) bool) {
		// 1) base layers (BandFileBase..BandFileBase+999)
		baseLayers, err := collect(opt.FS, root, opt.BaseDir, "", BandFileBase, opt)
		if err != nil {
			yield(Layer{}, err)
			return
		}
		// Base file priorities are BandFileBase+0..+n; they must stay
		// inside the base band so they never overflow into the overlay
		// band (BandFileOverlay) and reorder relative to overlays.
		if len(baseLayers) > baseBandWidth {
			yield(Layer{}, fmt.Errorf(
				"scan: base dir %q holds %d layers; max %d (priority band width)",
				opt.BaseDir, len(baseLayers), baseBandWidth))
			return
		}
		for _, l := range baseLayers {
			if !yield(l, nil) {
				return
			}
		}

		// 2) overlay layers (BandFileOverlay..BandFileOverlay+999)
		// When Profiles is non-empty we walk every direct subdirectory of
		// OverlayDir, read its optional _meta.yaml (with the `match:`
		// expression), and include the directory iff the expression
		// evaluates true against the active set. The single-profile case
		// is just a Profiles with one element: an overlays/<profile>/
		// without a _meta.yaml is auto-included.
		if len(opt.Profiles) > 0 {
			overlayLayers, err := collectOverlaysByExpression(opt.FS, root, opt)
			if err != nil {
				yield(Layer{}, err)
				return
			}
			for _, l := range overlayLayers {
				if !yield(l, nil) {
					return
				}
			}
		}

		// 3) Extra overlay layers from multi-axis configuration (priority
		//    supplied by caller, typically BandExtraOverlay or above).
		//    Axes merge in Priority order; declaration order only breaks
		//    ties. Missing directories are silently skipped so callers do
		//    not need to pre-check existence.
		extras := slices.Clone(opt.ExtraOverlays)
		slices.SortStableFunc(extras, func(a, b ExtraOverlay) int { return cmp.Compare(a.Priority, b.Priority) })
		for _, extra := range extras {
			if extra.Priority < BandExtraOverlay || extra.Priority > bandFileEnd-overlayStride {
				yield(Layer{}, fmt.Errorf("scan: extra overlay %q exceeds the file priority band", extra.Dir))
				return
			}
			extraLayers, err := collect(opt.FS, root, extra.Dir, extra.Profile, extra.Priority, opt)
			if err != nil {
				yield(Layer{}, err)
				return
			}
			if len(extraLayers) > overlayStride {
				yield(Layer{}, fmt.Errorf("scan: extra overlay %q holds %d layers; max %d per directory (priority stride)", extra.Dir, len(extraLayers), overlayStride))
				return
			}
			for _, l := range extraLayers {
				if !yield(l, nil) {
					return
				}
			}
		}
	}
}

// collectOverlaysByExpression iterates every direct subdir under OverlayDir, evaluates an optional
// `_meta.yaml.match` expression against the active profile set, and concatenates the layers from
// matching directories in lexical order. Directories without a _meta.yaml fall back to "match if
// subdir name is in active set" so simple use cases still work without writing meta files.
func collectOverlaysByExpression(fsys fs.FS, root string, opt ScanOptions) ([]Layer, error) {
	dir := path.Join(root, opt.OverlayDir)
	if opt.OnDir != nil {
		opt.OnDir(dir)
	}
	entries, err := readDir(fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan: read overlays %q: %w", dir, err)
	}
	slices.SortStableFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	active := NewSet(opt.Profiles...)
	var globalExpr string
	if strings.TrimSpace(opt.MatchAnd) != "" {
		// Validate eagerly so a typo fails the scan, not the first overlay hit.
		if _, err := Eval(opt.MatchAnd, active); err != nil {
			return nil, fmt.Errorf("scan: MatchAnd: %w", err)
		}
		globalExpr = opt.MatchAnd
	}
	var out []Layer
	priorityBase := BandFileOverlay
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "_") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		sub := path.Join(opt.OverlayDir, e.Name())
		matched, err := overlayMatches(fsys, root, sub, e.Name(), active)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		if globalExpr != "" {
			// Augment the active set with the overlay's own name so
			// expressions like "!canary" can suppress directories whose
			// name (or match field) brings them in. This makes the
			// global filter compositional with per-overlay matchers.
			scoped := NewSet(opt.Profiles...)
			scoped[e.Name()] = struct{}{}
			ok, err := Eval(globalExpr, scoped)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		if priorityBase >= BandExtraOverlay {
			return nil, fmt.Errorf("scan: too many matched overlays; max %d (priority band width)", (BandExtraOverlay-BandFileOverlay)/overlayStride)
		}
		layers, err := collect(fsys, root, sub, e.Name(), priorityBase, opt)
		if err != nil {
			return nil, err
		}
		// File offsets must remain inside the directory's priority window.
		if len(layers) > overlayStride {
			return nil, fmt.Errorf(
				"scan: overlay %q holds %d layers; max %d per directory (priority stride)",
				sub, len(layers), overlayStride)
		}
		out = append(out, layers...)
		priorityBase += overlayStride
	}
	return out, nil
}

// overlayStride is the per-overlay-directory priority window. A directory may hold at most this
// many config files so its file-priority offsets never overflow into the next matched overlay's
// band.
const overlayStride = 100

// Layer priority bands assigned by Scan. Base files occupy BandFileBase+i, matched overlay
// directories BandFileOverlay+dir*100+i, and multi-axis overlays start at BandExtraOverlay by
// convention. Every file band sits below the framework's generator and provider bands.
const (
	BandFileBase     = 1000
	BandFileOverlay  = 2000
	BandExtraOverlay = 3000
	bandFileEnd      = 7000
)

// MaxAxes is the number of axis windows below the generator band.
const MaxAxes = (bandFileEnd - BandExtraOverlay) / overlayStride

// baseBandWidth is the priority window for the base directory: its files occupy
// BandFileBase..BandFileBase+baseBandWidth-1 and must not reach the overlay band.
const baseBandWidth = BandFileOverlay - BandFileBase

// overlayMeta is the per-overlay-directory _meta.yaml subset the scanner consumes. Other fields
// are reserved for forward compatibility.
type overlayMeta struct {
	Match string `yaml:"match"`
}

func overlayMatches(fsys fs.FS, root, sub, name string, active Set) (bool, error) {
	metaPath := path.Join(root, sub, "_meta.yaml")
	data, err := readFile(fsys, metaPath)
	if err != nil {
		// No per-overlay meta — fall back to "name == active member".
		if errors.Is(err, fs.ErrNotExist) {
			return active.Has(name), nil
		}
		// A meta file that exists but is unreadable changes match
		// semantics; surface it instead of silently degrading to name
		// matching.
		return false, fmt.Errorf("scan: read %s: %w", metaPath, err)
	}
	var m overlayMeta
	if err := yaml.Unmarshal(data, &m); err != nil {
		return false, fmt.Errorf("scan: parse %s: %w", metaPath, err)
	}
	if strings.TrimSpace(m.Match) == "" {
		return active.Has(name), nil
	}
	ok, err := Eval(m.Match, active)
	if err != nil {
		return false, fmt.Errorf("scan: %s: match: %w", metaPath, err)
	}
	return ok, nil
}

func collect(fsys fs.FS, root, sub, profile string, base int, opt ScanOptions) ([]Layer, error) {
	dir := path.Join(root, sub)
	if opt.OnDir != nil {
		opt.OnDir(dir)
	}
	entries, err := readDir(fsys, dir)
	if err != nil {
		// A missing directory contributes no layers. Strict mode still
		// requires the base directory so a mistyped WithDir fails loudly.
		if errors.Is(err, fs.ErrNotExist) && (profile != "" || !opt.Strict) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan: read dir %q: %w", dir, err)
	}

	// Stable lexicographic sort.
	slices.SortStableFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })

	out := make([]Layer, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), "_") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		full := path.Join(dir, e.Name())
		kind, codec, ok := classify(e.Name(), opt.PatchSuffixes)
		if !ok {
			if opt.Strict {
				return nil, fmt.Errorf("scan: unknown extension on %q (strict mode)", e.Name())
			}
			continue
		}
		data, err := readFile(fsys, full)
		if err != nil {
			return nil, fmt.Errorf("scan: read %q: %w", full, err)
		}
		out = append(out, Layer{
			Path:     full,
			Kind:     kind,
			Profile:  profile,
			Priority: base + len(out), // stable lexicographic priority offset
			Codec:    codec,
			Bytes:    data,
		})
	}
	return out, nil
}

// classify infers (Kind, codec) from a file name's extension. Patch suffixes are matched before
// plain suffixes (".patch.yaml" wins over ".yaml").
func classify(name string, patchSuffixes []string) (Kind, string, bool) {
	lname := strings.ToLower(name)
	for _, sfx := range patchSuffixes {
		if strings.HasSuffix(lname, sfx) {
			// Codec extension is the patch suffix minus ".patch".
			ext := strings.TrimPrefix(sfx, ".patch")
			return KindPatch, codecOf(ext), true
		}
	}
	ext := filepath.Ext(lname)
	if c := codecOf(ext); c != "" {
		return KindMerge, c, true
	}
	return KindUnknown, "", false
}

func codecOf(ext string) string { return codec.LookupExt(ext) }

func readDir(fsys fs.FS, dir string) ([]fs.DirEntry, error) {
	if fsys != nil {
		return fs.ReadDir(fsys, dir)
	}
	return os.ReadDir(dir)
}

func readFile(fsys fs.FS, p string) ([]byte, error) {
	if fsys != nil {
		return fs.ReadFile(fsys, p)
	}
	return os.ReadFile(p)
}
