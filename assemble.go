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

// Framework-assigned rank bands for non-file layers. File layers use the
// scan.Band* ranges (1000–6999); these always win over them.
const (
	bandGenerator = 7000
	bandProvider  = 8000
	bandOverride  = 9000

	prioritySecretResolved = bandOverride + 500
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
	return cmp.Or(cmp.Compare(a.class, b.class), cmp.Compare(a.prio, b.prio))
}

// Assemble: build []stagedLayer from File / Generator / Provider layers.

// stagedLayer is the unit produced by assemble() and consumed by commit().
// Exactly one of `data` (merge) or `patch` (RFC 6902 JSON) is set.
// Layers sort by class, then prio, then declaration order; src.Priority is
// the reported rank (framework band plus in-band order).
type stagedLayer struct {
	src   SourceRef
	class layerClass
	prio  int
	data  map[string]any
	patch []byte
	// sum is the sha256 of a file layer's bytes; zero for layers that do
	// not come from a file (providers, generators, overrides).
	sum [32]byte
}

type assemblyResult struct {
	staged       []stagedLayer
	appendSlices bool
	mergeKeys    map[string]string
	// fingerprint identifies the inputs when every layer is a file and
	// nothing else can change the result (see inputFingerprint); zero
	// otherwise.
	fingerprint [32]byte
	// watchDirs lists the scanned file directories, including empty or
	// missing ones; it is filled even when assembly fails part-way.
	watchDirs []string
}

type assemblyMeta struct {
	metaSum      [32]byte
	profileEnv   string
	defaultProf  string
	appendSlices bool
	mergeKeys    map[string]string
}

// assemble runs Discover + Provider Load and returns ordered layers
// without publishing state. It updates writer-owned caches and invokes
// providers and generators. The assemblyResult carries meta-driven knobs through
// pipelineState so Plan and Reload never share transient Manager fields.
//
// hostnameOverride pins the hostname used to resolve multi-axis overlay
// axes that rely on Axis.FromHostname. Empty string means use the OS
// hostname. Plan sets this from WithPlanHostname; commit()
// passes "" so live reloads always see the real hostname.
//
// extra layers (a WithOverride map) join the same list. Every
// layer is sorted once, stably, by class and in-class priority, so
// declaration order only breaks ties.
func (m *Manager[T]) assemble(ctx context.Context, hostnameOverride string, extra ...stagedLayer) (assemblyResult, error) {
	if err := ctx.Err(); err != nil {
		return assemblyResult{}, err
	}
	generation := codec.Generation()

	scanOpt, meta, err := m.buildScanOptions(hostnameOverride)
	if err != nil {
		return assemblyResult{}, err
	}
	var watchDirs []string
	scanOpt.OnDir = func(dir string) { watchDirs = append(watchDirs, dir) }
	staged, err := m.assembleFileLayers(scanOpt)
	if err != nil {
		return assemblyResult{watchDirs: watchDirs}, err
	}
	generatorLayers, err := m.assembleGeneratorLayers(ctx)
	if err != nil {
		return assemblyResult{}, err
	}
	staged = append(staged, generatorLayers...)
	providerLayers, err := m.assembleProviderLayers(ctx)
	if err != nil {
		return assemblyResult{}, err
	}
	staged = append(staged, providerLayers...)
	staged = append(staged, extra...)
	if len(staged) == 0 {
		return assemblyResult{}, fcerr.ErrNoSources
	}
	slices.SortStableFunc(staged, compareLayers)
	return assemblyResult{
		staged:       staged,
		appendSlices: meta.appendSlices,
		mergeKeys:    meta.mergeKeys,
		fingerprint:  m.inputFingerprint(staged, meta.metaSum, generation),
		watchDirs:    watchDirs,
	}, nil
}

func (m *Manager[T]) buildScanOptions(hostnameOverride string) (scan.ScanOptions, assemblyMeta, error) {
	scanOpt := scan.ScanOptions{
		Strict: m.opts.Strict,
		FS:     m.opts.FS,
	}
	var metaOut assemblyMeta
	metaBytes, err := scan.LoadMeta(m.opts.FS, m.opts.Dir)
	if err != nil {
		return scanOpt, metaOut, fmt.Errorf("%w: _meta.yaml: %v", fcerr.ErrDecode, err)
	}
	metaOut.metaSum = sha256.Sum256(metaBytes)
	var meta scan.MetaFile
	if len(metaBytes) > 0 {
		if err := yaml.Unmarshal(metaBytes, &meta); err != nil {
			return scanOpt, metaOut, fmt.Errorf("%w: _meta.yaml: %v", fcerr.ErrDecode, err)
		}
		meta.Apply(&scanOpt)
		metaOut.profileEnv = meta.Spec.ProfileEnv
		metaOut.defaultProf = meta.Spec.DefaultProfile
		metaOut.appendSlices = meta.Spec.AppendSlices
	}
	metaOut.mergeKeys = m.metaKeys.combined(metaBytes, func() map[string]string {
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
	if names := m.opts.activeProfiles(metaOut.profileEnv, metaOut.defaultProf); len(names) > 0 {
		scanOpt.Profiles = slices.Clone(names)
		scanOpt.MatchAnd = m.opts.ProfileMatch
	}

	// Resolve multi-axis overlays via overlay. fastconfctl plan /
	// PR-bots on CI runners can pin the hostname via WithPlanHostname so
	// that the resulting diff is against the target environment, not the
	// runner.
	hostFn := os.Hostname
	if hostnameOverride != "" {
		override := hostnameOverride
		hostFn = func() (string, error) { return override, nil }
	}
	extras, axisErrs := scan.ResolveAxes(m.opts.OverlayAxes, hostFn)
	for _, e := range axisErrs {
		m.opts.Logger.LogAttrs(context.Background(), slog.LevelWarn, "fastconf: hostname resolution failed; axis skipped", slog.String("axis", e.Axis), slog.Any("err", e.Err))
	}
	scanOpt.ExtraOverlays = append(scanOpt.ExtraOverlays, extras...)
	return scanOpt, metaOut, nil
}

func (m *Manager[T]) assembleFileLayers(scanOpt scan.ScanOptions) ([]stagedLayer, error) {
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
			staged = append(staged, stagedLayer{src: src, prio: src.Priority, patch: patchBytes, sum: sha256.Sum256(layer.Bytes)})
			continue
		}
		generation := codec.Generation()
		dec, derr := codec.For(layer.Codec)
		if derr != nil {
			return nil, fmt.Errorf("%w: %w", fcerr.ErrDecode, derr)
		}
		sum := sha256.Sum256(layer.Bytes)
		raw, derr := m.layerCache.decoded(layer, sum, dec, generation)
		if derr != nil {
			return nil, fmt.Errorf("%w: %s: %w", fcerr.ErrDecode, layer.Path, derr)
		}
		staged = append(staged, stagedLayer{src: src, prio: src.Priority, data: raw, sum: sum})
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
					Priority: bandGenerator + gs.Priority,
					Codec:    gs.Codec,
				},
				class: classGenerator,
				prio:  gs.Priority,
				data:  raw,
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
			// fcerr.ErrDecode wrapping.
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
				Priority: bandProvider + e.Info.Priority,
				Revision: snap.Revision,
				Stale:    snap.Stale,
			},
			class: classProvider,
			prio:  e.Info.Priority,
			data:  snap.Map,
		})
	}
	return staged, nil
}
