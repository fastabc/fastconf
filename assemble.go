package fastconf

import (
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"slices"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/internal/fcerr"
	"github.com/fastabc/fastconf/internal/scan"
	"gopkg.in/yaml.v3"
)

// Diagnostic rank offsets for non-file layers. Precedence is determined by
// layerClass, then in-class priority; these offsets do not bound user priorities.
const (
	rankOffsetGenerator = 7000
	rankOffsetProvider  = 8000
	rankOffsetOverride  = 9000

	rankSecretResolved = rankOffsetOverride + 500
)

// layerClass fixes the precedence between source categories. A priority
// only orders layers within one class, so no priority value can lift a
// provider above an override or a generator above a provider.
type layerClass uint8

const (
	classFile layerClass = iota
	classGenerator
	classProvider
	classOverride
)

func compareLayers(a, b stagedLayer) int {
	return cmp.Or(cmp.Compare(a.class, b.class), cmp.Compare(a.classPriority, b.classPriority))
}

// Assemble: build []stagedLayer from File / Generator / Provider layers.

// stagedLayer is the unit produced by assemble() and consumed by commit().
// Exactly one of data (owned), cachedLayer (borrowed file data) or patch is set.
// Layers sort by class, then classPriority, then declaration order; src.Priority is
// the diagnostic rank (source offset plus local priority).
type stagedLayer struct {
	src           SourceRef
	class         layerClass
	classPriority int
	data          map[string]any
	cachedLayer   *decodedLayer
	patch         []byte
	// fileSum is the sha256 of a file layer's bytes; zero for layers that do
	// not come from a file (providers, generators, overrides).
	fileSum [32]byte
}

type assemblyResult struct {
	staged       []stagedLayer
	strict       bool
	appendSlices bool
	mergeKeys    map[string]string
	// fingerprint identifies the inputs when every layer is a file and
	// nothing else can change the result (see inputFingerprint); zero
	// otherwise.
	fingerprint [32]byte
	// scannedDirs lists the scanned file directories, including empty or
	// missing ones; it is filled even when assembly fails part-way.
	scannedDirs []string
}

type rootMetaSettings struct {
	metaSum      [32]byte
	appendSlices bool
	mergeKeys    map[string]string
}

// assemble loads and orders layers without publication, updating writer-owned caches.
// hostnameOverride pins hostname-derived axes for Plan; empty uses the OS hostname.
// Extra override layers sort by class/priority with declaration order breaking ties.
func (m *Manager[T]) assemble(ctx context.Context, hostnameOverride string, extra ...stagedLayer) (assemblyResult, error) {
	if err := ctx.Err(); err != nil {
		return assemblyResult{}, err
	}
	generation := codec.Generation()

	var meta rootMetaSettings
	var staged []stagedLayer
	var scannedDirs []string
	strict := m.opts.Strict
	if !m.opts.SkipDiscovery {
		scanOpt, discovered, err := m.resolveDiscovery(hostnameOverride)
		if err != nil {
			return assemblyResult{}, discoveryError(err)
		}
		meta = discovered
		strict = scanOpt.Strict
		scanOpt.OnDir = func(dir string) { scannedDirs = append(scannedDirs, dir) }
		staged, err = m.assembleFileLayers(scanOpt)
		if err != nil {
			return assemblyResult{scannedDirs: scannedDirs}, discoveryError(err)
		}
	}
	generatorLayers, err := m.assembleGeneratorLayers(ctx)
	if err != nil {
		return assemblyResult{scannedDirs: scannedDirs}, err
	}
	staged = append(staged, generatorLayers...)
	providerLayers, err := m.assembleProviderLayers(ctx)
	if err != nil {
		return assemblyResult{scannedDirs: scannedDirs}, err
	}
	staged = append(staged, providerLayers...)
	staged = append(staged, extra...)
	if len(staged) == 0 {
		return assemblyResult{scannedDirs: scannedDirs}, fcerr.ErrNoSources
	}
	slices.SortStableFunc(staged, compareLayers)
	return assemblyResult{
		staged:       staged,
		strict:       strict,
		appendSlices: meta.appendSlices,
		mergeKeys:    meta.mergeKeys,
		fingerprint:  m.inputFingerprint(staged, meta.metaSum, generation),
		scannedDirs:  scannedDirs,
	}, nil
}

func (m *Manager[T]) resolveDiscovery(hostnameOverride string) (scan.Options, rootMetaSettings, error) {
	scanOpt := scan.Options{
		Strict: m.opts.Strict,
		FS:     m.opts.FS,
	}
	var metaOut rootMetaSettings
	metaBytes, err := scan.LoadMeta(m.opts.FS, m.opts.Dir)
	if err != nil {
		return scanOpt, metaOut, discoveryError(err)
	}
	metaOut.metaSum = sha256.Sum256(metaBytes)
	var meta scan.MetaFile
	if len(metaBytes) > 0 {
		if err := yaml.Unmarshal(metaBytes, &meta); err != nil {
			return scanOpt, metaOut, fmt.Errorf("%w: _meta.yaml: %v", fcerr.ErrDecode, err)
		}
		meta.ApplyTo(&scanOpt)
		metaOut.appendSlices = meta.Spec.AppendSlices
	}
	metaOut.mergeKeys = m.metaKeys.getOrBuild(metaBytes, func() map[string]string {
		// Programmatic entries (WithMergeKeys) win on conflict.
		keys := maps.Clone(meta.Spec.MergeKeys)
		if keys == nil && len(m.opts.MergeKeys) > 0 {
			keys = make(map[string]string, len(m.opts.MergeKeys))
		}
		maps.Copy(keys, m.opts.MergeKeys)
		return keys
	})
	// Compose the active profile set; the global match expression applies
	// to whichever set was resolved.
	if names := m.opts.activeProfiles(meta.Spec.ProfileEnv, meta.Spec.DefaultProfile); len(names) > 0 {
		scanOpt.Profiles = slices.Clone(names)
		scanOpt.GlobalMatch = m.opts.ProfileMatch
	}

	// Resolve multi-axis overlays. Plan callers on CI runners can pin
	// the hostname via WithPlanHostname so
	// that the resulting diff is against the target environment, not the
	// runner.
	hostFn := os.Hostname
	if hostnameOverride != "" {
		override := hostnameOverride
		hostFn = func() (string, error) { return override, nil }
	}
	extras, axisErrs := scan.ResolveAxes(m.opts.Axes, hostFn)
	for _, e := range axisErrs {
		m.opts.Logger.LogAttrs(context.Background(), slog.LevelWarn, "fastconf: hostname resolution failed; axis skipped", slog.String("axis", e.Axis), slog.Any("err", e.Err))
	}
	scanOpt.AxisOverlays = append(scanOpt.AxisOverlays, extras...)
	return scanOpt, metaOut, nil
}

func (m *Manager[T]) assembleFileLayers(scanOpt scan.Options) ([]stagedLayer, error) {
	staged := make([]stagedLayer, 0, 8)
	for layer, err := range scan.Scan(m.opts.Dir, scanOpt) {
		if err != nil {
			return nil, err
		}
		src := SourceRef{
			Path:     layer.Path,
			Kind:     mapLayerKind(layer.Kind),
			Profile:  layer.Profile,
			Priority: layer.Priority,
			Codec:    layer.Codec,
		}
		if layer.Kind == scan.KindPatch {
			raw, derr := codec.DecodeAny(layer.Codec, layer.Bytes)
			if derr != nil {
				return nil, fmt.Errorf("%w: %s: %w", fcerr.ErrDecode, layer.Path, derr)
			}
			patchBytes, perr := confmap.PatchBytesFromAny(raw)
			if perr != nil {
				return nil, fmt.Errorf("%w: %s: %w", fcerr.ErrMerge, layer.Path, perr)
			}
			staged = append(staged, stagedLayer{src: src, classPriority: src.Priority, patch: patchBytes, fileSum: sha256.Sum256(layer.Bytes)})
			continue
		}
		generation := codec.Generation()
		dec, derr := codec.For(layer.Codec)
		if derr != nil {
			return nil, fmt.Errorf("%w: %w", fcerr.ErrDecode, derr)
		}
		sum := sha256.Sum256(layer.Bytes)
		raw, derr := m.layerCache.getOrDecode(layer, sum, dec, generation)
		if derr != nil {
			return nil, fmt.Errorf("%w: %s: %w", fcerr.ErrDecode, layer.Path, derr)
		}
		staged = append(staged, stagedLayer{src: src, classPriority: src.Priority, cachedLayer: raw, fileSum: sum})
	}
	return staged, nil
}

func (m *Manager[T]) assembleGeneratorLayers(ctx context.Context) ([]stagedLayer, error) {
	staged := make([]stagedLayer, 0, len(m.opts.Generators))
	for _, g := range m.opts.Generators {
		srcs, err := g.Generate(ctx)
		if err != nil {
			return nil, fmt.Errorf("%w: generator %q: %w", fcerr.ErrProvider, g.Name(), err)
		}
		for _, gs := range srcs {
			dec, derr := codec.For(gs.Codec)
			if derr != nil {
				return nil, fmt.Errorf("%w: generator %q codec %q: %w", fcerr.ErrDecode, g.Name(), gs.Codec, derr)
			}
			raw, derr := dec.Decode(gs.Data)
			if derr != nil {
				return nil, fmt.Errorf("%w: generator %q: %w", fcerr.ErrDecode, g.Name(), derr)
			}
			staged = append(staged, stagedLayer{
				src: SourceRef{
					Path:     "gen://" + g.Name() + "/" + gs.Name,
					Kind:     LayerGenerator,
					Priority: rankOffsetGenerator + gs.Priority,
					Codec:    gs.Codec,
				},
				class:         classGenerator,
				classPriority: gs.Priority,
				data:          raw,
			})
		}
	}
	return staged, nil
}

func (m *Manager[T]) assembleProviderLayers(ctx context.Context) ([]stagedLayer, error) {
	if len(m.opts.Providers) == 0 {
		return nil, nil
	}
	staged := make([]stagedLayer, 0, len(m.opts.Providers))
	for _, e := range m.opts.Providers {
		p := e.Provider
		snap, err := p.Load(ctx)
		if err != nil {
			// Preserve ctx cancellation as-is so callers can
			// errors.Is(err, context.Canceled / DeadlineExceeded)
			// after a Reload(ctx) timeout instead of wading through
			// fcerr.ErrProvider wrapping.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: provider %q: %w", fcerr.ErrProvider, p.Name(), err)
		}
		if snap.Map == nil {
			continue
		}
		// The provider keeps ownership of its map (contracts.Provider.Load),
		// but merge aliases subtrees into the merged tree and later stages
		// mutate it in place. Without the clone those writes (including
		// resolved secret plaintext) would reach provider-owned state.
		snap.Map = confmap.DeepClone(snap.Map)
		if snap.Stale {
			m.opts.Logger.LogAttrs(context.Background(), slog.LevelWarn, "fastconf provider snapshot stale", slog.String("provider", p.Name()), slog.String("revision", snap.Revision))
		}
		staged = append(staged, stagedLayer{
			src: SourceRef{
				Path:     "provider://" + p.Name(),
				Kind:     LayerProvider,
				Priority: rankOffsetProvider + e.Info.Priority,
				Revision: snap.Revision,
				Stale:    snap.Stale,
			},
			class:         classProvider,
			classPriority: e.Info.Priority,
			data:          snap.Map,
		})
	}
	return staged, nil
}

// discoveryError classifies file discovery failures without replacing existing sentinels.
func discoveryError(err error) error {
	if errors.Is(err, fcerr.ErrFastConf) {
		return err
	}
	return fmt.Errorf("%w: %w", fcerr.ErrDecode, err)
}

// mapLayerKind translates an internal scan.Kind into the public
// LayerKind enum reported via SourceRef.
func mapLayerKind(k scan.Kind) LayerKind {
	switch k {
	case scan.KindMerge:
		return LayerMerge
	case scan.KindPatch:
		return LayerPatch
	default:
		return LayerUnknown
	}
}
