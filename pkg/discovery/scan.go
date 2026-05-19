// Package discovery scans a configuration root and produces a stream of
// priority-ordered layers (base, overlays, extra overlay axes).
package discovery

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/profile"
)

// Kind describes the merge semantics of a discovered layer. It maps
// one-to-one to fastconf.LayerKind; internal packages cannot import
// the public package without creating a cycle, hence the redefinition.
type Kind uint8

const (
	KindUnknown Kind = iota
	KindMerge
	KindPatch
)

// Layer is a descriptor emitted by the discovery stage. Bytes is read
// fully into memory (config files are typically less than a few MB).
type Layer struct {
	Path     string
	Kind     Kind
	Profile  string
	Priority int
	Codec    string // "yaml" | "json" | "toml"; third-party codecs registered via fastconf.RegisterCodecExt show up here too (consulted through CodecExtFunc).
	Bytes    []byte
}

// ExtraOverlay describes an additional directory to include as file layers
// after the main base and overlay dirs. It is used by the multi-axis overlay
// feature (see ScanOptions.ExtraOverlays).
type ExtraOverlay struct {
	Dir      string // path relative to the scan root, e.g. "hosts/ua"
	Profile  string // label for provenance reporting, e.g. "host:ua"
	Priority int    // base priority for layers in this directory
}

// ScanOptions controls scan behaviour. The single-profile use case
// goes through Profiles with one element; there is no separate
// scalar field.
type ScanOptions struct {
	BaseDir       string         // default "base"
	OverlayDir    string         // default "overlays"
	Profiles      []string       // Active profile set; empty = base-only, one element = single-profile, more = multi-axis expression matching.
	MatchAnd      string         // Optional global expression AND-ed with each overlay's match.
	PatchSuffixes []string       // default [".patch.yaml", ".patch.json"]
	Strict        bool           // when true, an unrecognised extension errors instead of being skipped
	FS            fs.FS          // optional virtual filesystem for tests
	ExtraOverlays []ExtraOverlay // Additional axis directories (multi-axis overlay feature).
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

// LayerSeq is a callback stream of discovered layers. It keeps early-stop
// behavior without importing Go 1.23's iterator package.
type LayerSeq func(yield func(Layer, error) bool)

// Scan walks root and yields layers in base→overlay priority order,
// lexicographic within each tier.
//
// The function returns a callback stream instead of []Layer so that:
//   - callers can early-stop (e.g. on a per-layer decode error);
//   - large directory trees do not balloon peak memory;
//   - Go 1.22 callers avoid the Go 1.23 iter/range-over-func dependency.
func Scan(root string, opt ScanOptions) LayerSeq {
	opt.defaults()

	return func(yield func(Layer, error) bool) {
		// 1) base layers (BandFileBase..BandFileBase+999)
		baseLayers, err := collect(opt.FS, root, opt.BaseDir, "", contracts.BandFileBase, opt)
		if err != nil {
			yield(Layer{}, err)
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
		//    Missing directories are silently skipped so callers do not
		//    need to pre-check existence.
		for _, extra := range opt.ExtraOverlays {
			extraLayers, err := collect(opt.FS, root, extra.Dir, extra.Profile, extra.Priority, opt)
			if err != nil {
				yield(Layer{}, err)
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

// collectOverlaysByExpression iterates every direct subdir under
// OverlayDir, evaluates an optional `_meta.yaml.match` expression against
// the active profile set, and concatenates the layers from matching
// directories in lexical order. Directories without a _meta.yaml fall
// back to "match if subdir name is in active set" so simple use cases
// still work without writing meta files.
func collectOverlaysByExpression(fsys fs.FS, root string, opt ScanOptions) ([]Layer, error) {
	dir := path.Join(root, opt.OverlayDir)
	entries, err := readDir(fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("discovery: read overlays %q: %w", dir, err)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	active := profile.NewSet(opt.Profiles...)
	var globalExpr string
	if strings.TrimSpace(opt.MatchAnd) != "" {
		// Validate eagerly so a typo fails the scan, not the first overlay hit.
		if _, err := profile.Eval(opt.MatchAnd, active); err != nil {
			return nil, fmt.Errorf("discovery: MatchAnd: %w", err)
		}
		globalExpr = opt.MatchAnd
	}
	var out []Layer
	priorityBase := contracts.BandFileOverlay
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
			scoped := profile.NewSet(opt.Profiles...)
			scoped[e.Name()] = struct{}{}
			ok, err := profile.Eval(globalExpr, scoped)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		layers, err := collect(fsys, root, sub, e.Name(), priorityBase, opt)
		if err != nil {
			return nil, err
		}
		out = append(out, layers...)
		priorityBase += 100
	}
	return out, nil
}

// overlayMeta is the per-overlay-directory _meta.yaml subset the
// discovery scanner consumes. Other fields are reserved for forward compatibility.
type overlayMeta struct {
	Match string `yaml:"match"`
}

func overlayMatches(fsys fs.FS, root, sub, name string, active profile.Set) (bool, error) {
	metaPath := path.Join(root, sub, "_meta.yaml")
	data, err := readFile(fsys, metaPath)
	if err != nil {
		// No per-overlay meta — fall back to "name == active member".
		return active.Has(name), nil
	}
	var m overlayMeta
	if err := yaml.Unmarshal(data, &m); err != nil {
		return false, fmt.Errorf("discovery: parse %s: %w", metaPath, err)
	}
	if strings.TrimSpace(m.Match) == "" {
		return active.Has(name), nil
	}
	ok, err := profile.Eval(m.Match, active)
	if err != nil {
		return false, fmt.Errorf("discovery: %s: match: %w", metaPath, err)
	}
	return ok, nil
}

func collect(fsys fs.FS, root, sub, profile string, base int, opt ScanOptions) ([]Layer, error) {
	dir := path.Join(root, sub)
	entries, err := readDir(fsys, dir)
	if err != nil {
		// base must exist; overlays may be missing (user has not configured that profile).
		if errors.Is(err, fs.ErrNotExist) && profile != "" {
			return nil, nil
		}
		return nil, fmt.Errorf("discovery: read dir %q: %w", dir, err)
	}

	// Stable lexicographic sort.
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	out := make([]Layer, 0, len(entries))
	for i, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), "_") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		full := path.Join(dir, e.Name())
		kind, codec, ok := classify(e.Name(), opt.PatchSuffixes)
		if !ok {
			if opt.Strict {
				return nil, fmt.Errorf("discovery: unknown extension on %q (strict mode)", e.Name())
			}
			continue
		}
		data, err := readFile(fsys, full)
		if err != nil {
			return nil, fmt.Errorf("discovery: read %q: %w", full, err)
		}
		out = append(out, Layer{
			Path:     full,
			Kind:     kind,
			Profile:  profile,
			Priority: base + i, // stable lexicographic priority offset
			Codec:    codec,
			Bytes:    data,
		})
	}
	return out, nil
}

// classify infers (Kind, codec) from a file name's extension.
// Patch suffixes are matched before plain suffixes (".patch.yaml" wins
// over ".yaml").
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

// CodecExtFunc, when non-nil, is consulted before the built-in extension
// table. It is wired by the top-level fastconf package to the decoder
// registry so that third-party RegisterCodecExt calls reach discovery
// without internal/discovery taking a sibling-package import dependency.
var CodecExtFunc func(ext string) string

func codecOf(ext string) string {
	if CodecExtFunc != nil {
		if name := CodecExtFunc(ext); name != "" {
			return name
		}
	}
	switch strings.TrimPrefix(strings.ToLower(ext), ".") {
	case "yaml", "yml":
		return "yaml"
	case "json":
		return "json"
	case "toml":
		return "toml"
	}
	return ""
}

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
