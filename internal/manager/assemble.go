package manager

// Assemble: build []stagedLayer from File / Generator / Provider layers.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/fcerr"
	istate "github.com/fastabc/fastconf/internal/state"
	"github.com/fastabc/fastconf/pkg/decoder"
	"github.com/fastabc/fastconf/pkg/discovery"
	"github.com/fastabc/fastconf/pkg/merger"
)

// stagedLayer is the unit produced by assemble() and consumed by commit().
// Exactly one of `data` (merge) or `patch` (RFC 6902 JSON) is set.
type stagedLayer struct {
	src   istate.SourceRef
	data  map[string]any
	patch []byte
}

type providerEntry struct {
	name     string
	priority int
	data     map[string]any
	revision string
	stale    bool
}

type assemblyResult struct {
	staged       []stagedLayer
	appendSlices bool
	mergeKeys    map[string]string
}

type assemblyMeta struct {
	profileEnv   string
	defaultProf  string
	appendSlices bool
	mergeKeys    map[string]string
}

// assemble runs Discover + Provider Load and returns ordered layers
// WITHOUT publishing any state. It is pure: callable in shadow mode
// for preflight. The assemblyResult carries meta-driven knobs through
// pipelineCtx so Plan and Reload never share transient Manager fields.
//
// hostnameOverride pins the hostname used to resolve multi-axis overlay
// axes that rely on DefaultFromHostname. Empty string means use the OS
// hostname. Plan() sets this from PlanBuilder.WithHostname; commit()
// passes "" so live reloads always see the real hostname.
func (m *M[T]) assemble(ctx context.Context, hostnameOverride string) (assemblyResult, error) {
	if err := ctx.Err(); err != nil {
		return assemblyResult{}, err
	}

	scanOpt, meta, err := m.buildScanOptions(hostnameOverride)
	if err != nil {
		return assemblyResult{}, err
	}
	staged := make([]stagedLayer, 0, 8)
	fileLayers, err := m.assembleFileLayers(scanOpt)
	if err != nil {
		return assemblyResult{}, err
	}
	staged = append(staged, fileLayers...)
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
	if len(staged) == 0 {
		return assemblyResult{}, fcerr.ErrNoSources
	}
	return assemblyResult{
		staged:       staged,
		appendSlices: meta.appendSlices,
		mergeKeys:    meta.mergeKeys,
	}, nil
}

func (m *M[T]) buildScanOptions(hostnameOverride string) (discovery.ScanOptions, assemblyMeta, error) {
	scanOpt := discovery.ScanOptions{
		Strict: m.opts.Strict,
		FS:     m.opts.FS,
	}
	var metaOut assemblyMeta
	if metaBytes, _ := discovery.LoadMeta(m.opts.FS, m.opts.Dir); len(metaBytes) > 0 {
		var meta discovery.MetaFile
		if err := yaml.Unmarshal(metaBytes, &meta); err != nil {
			return scanOpt, metaOut, fmt.Errorf("%w: _meta.yaml: %v", fcerr.ErrDecode, err)
		}
		meta.Apply(&scanOpt)
		metaOut.profileEnv = meta.Spec.ProfileEnv
		metaOut.defaultProf = meta.Spec.DefaultProfile
		metaOut.appendSlices = meta.Spec.AppendSlices
		metaOut.mergeKeys = maps.Clone(meta.Spec.MergeKeys)
	}
	// Compose the active profile set. Multi-profile callers (Profiles
	// non-empty) take precedence; otherwise a non-empty single-profile
	// effective value is promoted to a one-element set so the discovery
	// scanner has a uniform expression-matching path.
	if len(m.opts.Profiles) > 0 {
		scanOpt.Profiles = append([]string{}, m.opts.Profiles...)
		scanOpt.MatchAnd = m.opts.ProfileExpr
	} else if eff := m.opts.EffectiveProfile(metaOut.profileEnv, metaOut.defaultProf); eff != "" {
		scanOpt.Profiles = []string{eff}
	}

	// Resolve multi-axis overlays via pkg/discovery. fastconfctl plan /
	// PR-bots on CI runners can pin the hostname via WithPlanHostname so
	// that the resulting diff is against the target environment, not the
	// runner.
	hostFn := os.Hostname
	if hostnameOverride != "" {
		override := hostnameOverride
		hostFn = func() (string, error) { return override, nil }
	}
	extras, axisErrs := discovery.ResolveAxes(m.opts.OverlayAxes, hostFn)
	for _, e := range axisErrs {
		m.opts.Log.Warn().
			Str("axis", e.Axis).
			Err(e.Err).
			Msg("fastconf: hostname resolution failed; axis skipped")
	}
	scanOpt.ExtraOverlays = append(scanOpt.ExtraOverlays, extras...)
	return scanOpt, metaOut, nil
}

func (m *M[T]) assembleFileLayers(scanOpt discovery.ScanOptions) ([]stagedLayer, error) {
	staged := make([]stagedLayer, 0, 8)
	var scanErr error
	discovery.Scan(m.opts.Dir, scanOpt)(func(layer discovery.Layer, err error) bool {
		if err != nil {
			scanErr = err
			return false
		}
		src := istate.SourceRef{
			Path:     layer.Path,
			Kind:     mapLayerKind(layer.Kind),
			Profile:  layer.Profile,
			Priority: layer.Priority,
			Codec:    layer.Codec,
		}
		if layer.Kind == discovery.KindPatch {
			raw, derr := decoder.DecodeAny(layer.Codec, layer.Bytes)
			if derr != nil {
				scanErr = fmt.Errorf("%w: %s: %w", fcerr.ErrDecode, layer.Path, derr)
				return false
			}
			patchBytes, perr := merger.PatchBytesFromAny(raw)
			if perr != nil {
				scanErr = fmt.Errorf("%w: %s: %w", fcerr.ErrPatch, layer.Path, perr)
				return false
			}
			staged = append(staged, stagedLayer{src: src, patch: patchBytes})
			return true
		}
		dec, derr := decoder.For(layer.Codec)
		if derr != nil {
			scanErr = fmt.Errorf("%w: %w", fcerr.ErrDecode, derr)
			return false
		}
		raw, derr := dec.Decode(layer.Bytes)
		if derr != nil {
			scanErr = fmt.Errorf("%w: %s: %w", fcerr.ErrDecode, layer.Path, derr)
			return false
		}
		staged = append(staged, stagedLayer{src: src, data: raw})
		return true
	})
	if scanErr != nil {
		return nil, scanErr
	}
	return staged, nil
}

func (m *M[T]) assembleGeneratorLayers(ctx context.Context) ([]stagedLayer, error) {
	staged := make([]stagedLayer, 0, len(m.opts.Generators))
	for _, g := range m.opts.Generators {
		srcs, err := g.Generate(ctx)
		if err != nil {
			return nil, fmt.Errorf("%w: generator %q: %w", fcerr.ErrDecode, g.Name(), err)
		}
		for _, gs := range srcs {
			dec, derr := decoder.For(gs.Codec)
			if derr != nil {
				return nil, fmt.Errorf("%w: generator %q codec %q: %w", fcerr.ErrDecode, g.Name(), gs.Codec, derr)
			}
			raw, derr := dec.Decode(gs.Data)
			if derr != nil {
				return nil, fmt.Errorf("%w: generator %q: %w", fcerr.ErrDecode, g.Name(), derr)
			}
			prio := gs.Priority
			if prio == 0 {
				prio = contracts.PriorityGenerator
			}
			staged = append(staged, stagedLayer{
				src: istate.SourceRef{
					Path:     "gen://" + g.Name() + "/" + gs.Name,
					Kind:     istate.LayerGenerator,
					Priority: contracts.BandGenerator + prio,
					Codec:    gs.Codec,
				},
				data: raw,
			})
		}
	}
	return staged, nil
}

func (m *M[T]) assembleProviderLayers(ctx context.Context) ([]stagedLayer, error) {
	if len(m.opts.Providers) == 0 {
		return nil, nil
	}
	ps := make([]providerEntry, 0, len(m.opts.Providers))
	for _, p := range m.opts.Providers {
		snap, err := loadProviderSnapshot(ctx, p)
		if err != nil {
			// Preserve ctx cancellation as-is so callers can
			// errors.Is(err, context.Canceled / DeadlineExceeded)
			// after a Reload(ctx) timeout instead of wading through
			// fcerr.ErrDecode wrapping.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: provider %q: %w", fcerr.ErrDecode, p.Name(), err)
		}
		if snap.Map == nil {
			continue
		}
		if snap.Stale {
			m.opts.Log.Warn().
				Str("provider", p.Name()).
				Str("revision", snap.Revision).
				Msg("fastconf provider snapshot stale")
		}
		ps = append(ps, providerEntry{
			name:     p.Name(),
			priority: p.Priority(),
			data:     snap.Map,
			revision: snap.Revision,
			stale:    snap.Stale,
		})
	}
	sortProviderEntries(ps)

	staged := make([]stagedLayer, 0, len(ps))
	for _, e := range ps {
		src := istate.SourceRef{
			Path:     "provider://" + e.name,
			Kind:     istate.LayerProvider,
			Priority: contracts.BandProvider + e.priority,
			Codec:    "",
			Revision: e.revision,
			Stale:    e.stale,
		}
		staged = append(staged, stagedLayer{src: src, data: e.data})
	}
	return staged, nil
}

func sortProviderEntries(ps []providerEntry) {
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].priority < ps[j].priority })
}

// loadProviderSnapshot prefers SnapshotProvider.LoadSnapshot when the
// provider implements it, and falls back to the legacy Load() map.
func loadProviderSnapshot(ctx context.Context, p contracts.Provider) (contracts.Snapshot, error) {
	if sp, ok := p.(contracts.SnapshotProvider); ok {
		return sp.LoadSnapshot(ctx)
	}
	m, err := p.Load(ctx)
	if err != nil {
		return contracts.Snapshot{}, err
	}
	return contracts.Snapshot{Map: m}, nil
}
