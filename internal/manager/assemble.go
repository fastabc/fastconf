package manager

// Assemble: build []stagedLayer from File / Generator / Provider layers.

import (
	"context"
	"errors"
	"fmt"
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

// assemble runs Discover + Provider Load and returns ordered layers
// WITHOUT publishing any state. It is pure: callable in shadow mode
// for preflight. The bool return value is the meta-driven appendSlices
// flag, threaded through pipelineCtx by the caller.
//
// hostnameOverride pins the hostname used to resolve multi-axis overlay
// axes that rely on DefaultFromHostname. Empty string means use the OS
// hostname. Plan() sets this from PlanBuilder.WithHostname; commit()
// passes "" so live reloads always see the real hostname.
func (m *M[T]) assemble(ctx context.Context, hostnameOverride string) ([]stagedLayer, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	scanOpt := discovery.ScanOptions{
		Strict: m.opts.Strict,
		FS:     m.opts.FS,
	}
	var (
		metaProfileEnv string
		metaDefault    string
		appendSlices   bool
	)
	if metaBytes, _ := discovery.LoadMeta(m.opts.FS, m.opts.Dir); len(metaBytes) > 0 {
		var meta discovery.MetaFile
		if err := yaml.Unmarshal(metaBytes, &meta); err != nil {
			return nil, false, fmt.Errorf("%w: _meta.yaml: %v", fcerr.ErrDecode, err)
		}
		meta.Apply(&scanOpt)
		metaProfileEnv = meta.Spec.ProfileEnv
		metaDefault = meta.Spec.DefaultProfile
		appendSlices = meta.Spec.AppendSlices
		// Capture meta-driven strategic merge keys for the next
		// runMerge invocation. We cache on the Manager so the merge stage
		// can read them without rethreading the assemble signature.
		m.lastMergeKeys.Store(&meta.Spec.MergeKeys)
	}
	// Compose the active profile set. Multi-profile callers (Profiles
	// non-empty) take precedence; otherwise a non-empty single-profile
	// effective value is promoted to a one-element set so the discovery
	// scanner has a uniform expression-matching path.
	if len(m.opts.Profiles) > 0 {
		scanOpt.Profiles = append([]string{}, m.opts.Profiles...)
		scanOpt.MatchAnd = m.opts.ProfileExpr
	} else if eff := m.opts.EffectiveProfile(metaProfileEnv, metaDefault); eff != "" {
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

	staged := make([]stagedLayer, 0, 8)

	// 1) File layers (base + overlay) in discovery order.
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
				scanErr = fmt.Errorf("%w: %s: %v", fcerr.ErrDecode, layer.Path, derr)
				return false
			}
			patchBytes, perr := merger.PatchBytesFromAny(raw)
			if perr != nil {
				scanErr = fmt.Errorf("%w: %s: %v", fcerr.ErrPatch, layer.Path, perr)
				return false
			}
			staged = append(staged, stagedLayer{src: src, patch: patchBytes})
			return true
		}
		dec, derr := decoder.For(layer.Codec)
		if derr != nil {
			scanErr = fmt.Errorf("%w: %v", fcerr.ErrDecode, derr)
			return false
		}
		raw, derr := dec.Decode(layer.Bytes)
		if derr != nil {
			scanErr = fmt.Errorf("%w: %s: %v", fcerr.ErrDecode, layer.Path, derr)
			return false
		}
		staged = append(staged, stagedLayer{src: src, data: raw})
		return true
	})
	if scanErr != nil {
		return nil, false, scanErr
	}

	// 2a) Dynamic generators. Run after file discovery so generated layers
	// see file-layer context (e.g. via env), but before providers so
	// providers can override generator output. Generators may emit
	// multiple Sources at distinct priorities: each RawLayer.Priority is
	// offset into the BandGenerator (7000) range; zero defaults to
	// contracts.PriorityGenerator so single-layer emissions need no
	// declaration.
	for _, g := range m.opts.Generators {
		srcs, err := g.Generate(ctx)
		if err != nil {
			return nil, false, fmt.Errorf("%w: generator %q: %v", fcerr.ErrDecode, g.Name(), err)
		}
		for _, gs := range srcs {
			dec, derr := decoder.For(gs.Codec)
			if derr != nil {
				return nil, false, fmt.Errorf("%w: generator %q codec %q: %v", fcerr.ErrDecode, g.Name(), gs.Codec, derr)
			}
			raw, derr := dec.Decode(gs.Data)
			if derr != nil {
				return nil, false, fmt.Errorf("%w: generator %q: %v", fcerr.ErrDecode, g.Name(), derr)
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

	// 2) Provider layers, sorted by their declared Priority() ascending so
	//    higher-priority providers (CLI > Env > KV) override lower ones.
	if len(m.opts.Providers) > 0 {
		ps := make([]providerEntry, 0, len(m.opts.Providers))
		for _, p := range m.opts.Providers {
			snap, err := loadProviderSnapshot(ctx, p)
			if err != nil {
				// Preserve ctx cancellation as-is so callers can
				// errors.Is(err, context.Canceled / DeadlineExceeded)
				// after a Reload(ctx) timeout instead of wading through
				// fcerr.ErrDecode wrapping.
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return nil, false, err
				}
				return nil, false, fmt.Errorf("%w: provider %q: %v", fcerr.ErrDecode, p.Name(), err)
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
		sort.SliceStable(ps, func(i, j int) bool { return ps[i].priority < ps[j].priority })
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
	}

	if len(staged) == 0 {
		return nil, false, fcerr.ErrNoSources
	}
	return staged, appendSlices, nil
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
