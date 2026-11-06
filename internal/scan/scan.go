// Package scan scans a configuration root and produces a stream of priority-ordered layers (base,
// overlays, extra overlay axes), and evaluates the profile expressions that select overlays.
package scan

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/contracts"
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

// Layer describes a discovered regular file of at most MaxFileBytes.
type Layer struct {
	Path     string
	Kind     Kind
	Profile  string
	Priority int
	Codec    string // registered codec name
	Bytes    []byte
}

// AxisOverlay describes an additional directory to include as file layers after the main base and
// overlay dirs. It is used by the multi-axis overlay feature (see Options.AxisOverlays).
type AxisOverlay struct {
	Dir      string // path relative to the scan root, e.g. "hosts/ua"
	Profile  string // label for provenance reporting, e.g. "host:ua"
	Priority int    // base priority for layers in this directory
}

// Options controls scan behaviour. The single-profile use case goes through Profiles with one
// element; there is no separate scalar field.
type Options struct {
	BaseDir       string        // default "base"
	OverlayDir    string        // default "overlays"
	Profiles      []string      // Active profile set; empty = base-only, one element = single-profile, more = multi-axis expression matching.
	GlobalMatch   string        // Optional global expression AND-ed with each overlay's match.
	PatchSuffixes []string      // default [".patch.yaml", ".patch.json"]
	Strict        bool          // require base; reject unknown extensions and non-regular layer entries
	FS            fs.FS         // optional virtual filesystem for tests
	AxisOverlays  []AxisOverlay // Additional axis directories (multi-axis overlay feature).
	// OnDir, when non-nil, is called with every contributing directory (base, matched overlays,
	// extra overlays) before it is read, even when it is empty or missing, so a watcher can
	// subscribe before the contents are observed.
	OnDir func(dir string)
}

func (o *Options) defaults() {
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

// Scan yields layers in base→overlay priority order, lexicographic within each tier.
// The iterator permits early stopping on discovery or decode errors.
func Scan(root string, opt Options) iter.Seq2[Layer, error] {
	opt.defaults()

	return func(yield func(Layer, error) bool) {
		// 1) base layers (BandFileBase..BandFileBase+999)
		baseLayers, err := collectDir(opt.FS, root, opt.BaseDir, "", BandFileBase, opt)
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
		// Active profiles select direct overlay directories by metadata expression or name.
		if len(opt.Profiles) > 0 {
			overlayLayers, err := collectProfileOverlays(opt.FS, root, opt)
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

		// Additional axes follow profiles; priority wins, declaration order breaks ties.
		extras := slices.Clone(opt.AxisOverlays)
		slices.SortStableFunc(extras, func(a, b AxisOverlay) int { return cmp.Compare(a.Priority, b.Priority) })
		for _, extra := range extras {
			if extra.Priority < BandAxis || extra.Priority > bandFileEnd-overlayStride {
				yield(Layer{}, fmt.Errorf("scan: extra overlay %q exceeds the file priority band", extra.Dir))
				return
			}
			extraLayers, err := collectDir(opt.FS, root, extra.Dir, extra.Profile, extra.Priority, opt)
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

// collectProfileOverlays selects lexical direct subdirectories by metadata match
// or, without metadata, by membership in the active profile set.
func collectProfileOverlays(fsys fs.FS, root string, opt Options) ([]Layer, error) {
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
	active := NewProfileSet(opt.Profiles...)
	var globalExpr string
	if strings.TrimSpace(opt.GlobalMatch) != "" {
		// Validate eagerly so a typo fails the scan, not the first overlay hit.
		if _, err := Eval(opt.GlobalMatch, active); err != nil {
			return nil, fmt.Errorf("scan: GlobalMatch: %w", err)
		}
		globalExpr = opt.GlobalMatch
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
			// Include the overlay name so the global filter can exclude that directory.
			scoped := NewProfileSet(opt.Profiles...)
			scoped[e.Name()] = struct{}{}
			ok, err := Eval(globalExpr, scoped)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		if priorityBase >= BandAxis {
			return nil, fmt.Errorf("scan: too many matched overlays; max %d (priority band width)", (BandAxis-BandFileOverlay)/overlayStride)
		}
		layers, err := collectDir(fsys, root, sub, e.Name(), priorityBase, opt)
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

// overlayStride reserves 100 priorities per overlay directory, preventing band overlap.
const overlayStride = 100

// Layer priority bands assigned by Scan. Base files occupy BandFileBase+i, matched overlay
// directories BandFileOverlay+dir*100+i, and multi-axis overlays start at BandAxis by
// convention. Every file band sits below the framework's generator and provider bands.
const (
	BandFileBase    = 1000
	BandFileOverlay = 2000
	BandAxis        = 3000
	bandFileEnd     = 7000
)

// MaxAxes is the number of axis windows below the generator band.
const MaxAxes = (bandFileEnd - BandAxis) / overlayStride

// baseBandWidth is the priority window for the base directory: its files occupy
// BandFileBase..BandFileBase+baseBandWidth-1 and must not reach the overlay band.
const baseBandWidth = BandFileOverlay - BandFileBase

// overlayMeta is the per-overlay-directory _meta.yaml subset the scanner consumes. Other fields
// are reserved for forward compatibility.
type overlayMeta struct {
	Match string `yaml:"match"`
}

func overlayMatches(fsys fs.FS, root, sub, name string, active ProfileSet) (bool, error) {
	metaPath := path.Join(root, sub, "_meta.yaml")
	data, _, err := readRegularFile(fsys, metaPath, true)
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

func collectDir(fsys fs.FS, root, sub, profile string, base int, opt Options) ([]Layer, error) {
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
		data, ok, err := readRegularFile(fsys, full, opt.Strict)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
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
			return KindPatch, codec.LookupExt(ext), true
		}
	}
	ext := filepath.Ext(lname)
	if c := codec.LookupExt(ext); c != "" {
		return KindMerge, c, true
	}
	return KindUnknown, "", false
}

func readDir(fsys fs.FS, dir string) ([]fs.DirEntry, error) {
	if fsys != nil {
		return fs.ReadDir(fsys, dir)
	}
	return os.ReadDir(dir)
}

// MaxFileBytes bounds each discovered file, matching LoadFile and remote providers.
const MaxFileBytes = contracts.DefaultMaxBodyBytes

// readRegularFile follows symlinks so Kubernetes ConfigMap entries remain loadable.
func readRegularFile(fsys fs.FS, p string, strict bool) ([]byte, bool, error) {
	info, err := statFile(fsys, p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, err
		}
		return nil, false, fmt.Errorf("scan: stat %q: %w", p, err)
	}
	if !info.Mode().IsRegular() {
		if strict {
			return nil, false, fmt.Errorf("scan: %q is not a regular file (strict mode)", p)
		}
		return nil, false, nil
	}
	f, err := openFile(fsys, p)
	if err != nil {
		return nil, false, fmt.Errorf("scan: read %q: %w", p, err)
	}
	defer func() { _ = f.Close() }()
	data, err := readBounded(f, info.Size())
	if err != nil {
		return nil, false, fmt.Errorf("scan: read %q: %w", p, err)
	}
	return data, true, nil
}

func statFile(fsys fs.FS, p string) (fs.FileInfo, error) {
	if fsys != nil {
		return fs.Stat(fsys, p)
	}
	return os.Stat(p)
}

func openFile(fsys fs.FS, p string) (fs.File, error) {
	if fsys != nil {
		return fsys.Open(p)
	}
	return os.Open(p)
}

// readBounded preallocates from size, but reads growing files completely up to MaxFileBytes+1.
func readBounded(f fs.File, size int64) ([]byte, error) {
	if size < 0 || size > MaxFileBytes {
		return contracts.ReadAllMax(f, MaxFileBytes)
	}
	data := make([]byte, size+1)
	n := 0
	var err error
	for n < len(data) {
		var read int
		read, err = f.Read(data[n:])
		n += read
		if err != nil {
			break
		}
	}
	if err != nil && err != io.EOF {
		return nil, err
	}
	data = data[:n]
	if err != io.EOF {
		tail, err := contracts.ReadAllMax(io.LimitReader(f, MaxFileBytes-int64(n)+1), MaxFileBytes)
		if err != nil {
			return nil, err
		}
		data = append(data, tail...)
	}
	if int64(len(data)) > MaxFileBytes {
		return nil, fmt.Errorf("%w: limit=%d bytes", contracts.ErrTooLarge, MaxFileBytes)
	}
	return data, nil
}
