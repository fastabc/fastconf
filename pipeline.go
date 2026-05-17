package fastconf

// assemble + commit + reload pipeline glue. The Stage list lives in
// pipeline_stages.go.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"reflect"
	"sort"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/decoder"
	"github.com/fastabc/fastconf/pkg/discovery"
	"github.com/fastabc/fastconf/pkg/merger"
	"github.com/fastabc/fastconf/policy"
)

// stagedLayer is the unit produced by assemble() and consumed by commit().
// Exactly one of `data` (merge) or `patch` (RFC 6902 JSON) is set.
type stagedLayer struct {
	src   SourceRef
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
func (m *Manager[T]) assemble(ctx context.Context) ([]stagedLayer, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	scanOpt := discovery.ScanOptions{
		Strict: m.opts.strict,
		FS:     m.opts.fsys,
	}
	var (
		metaProfileEnv string
		metaDefault    string
		appendSlices   bool
	)
	if metaBytes, _ := discovery.LoadMeta(m.opts.fsys, m.opts.dir); len(metaBytes) > 0 {
		var meta discovery.MetaFile
		if err := yaml.Unmarshal(metaBytes, &meta); err != nil {
			return nil, false, fmt.Errorf("%w: _meta.yaml: %v", ErrDecode, err)
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
	scanOpt.Profile = m.opts.effectiveProfile(metaProfileEnv, metaDefault)
	if len(m.opts.profiles) > 0 {
		scanOpt.Profiles = append([]string{}, m.opts.profiles...)
		scanOpt.MatchAnd = m.opts.profileExpr
	}

	// Resolve multi-axis overlays: for each axis, determine the active value
	// and add the subdirectory as an extra overlay layer.
	//
	// Resolution order (per OverlayAxis.DefaultFromHostname doc):
	//   1. EnvVar present + non-empty  → use that value
	//   2. EnvVar present + empty      → skip axis (operator opt-out)
	//   3. EnvVar absent + DefaultFromHostname → fall back to os.Hostname()
	//   4. otherwise                   → skip axis
	for _, ax := range m.opts.overlayAxes {
		var val string
		if ax.EnvVar != "" {
			envVal, present := os.LookupEnv(ax.EnvVar)
			switch {
			case present && envVal != "":
				val = envVal
			case present:
				// Explicitly empty → skip this axis regardless of DefaultFromHostname.
				continue
			}
			// !present: fall through to DefaultFromHostname check below.
		}
		if val == "" && ax.DefaultFromHostname {
			// Callers can pin the hostname via WithPlanHostname so that
			// fastconfctl plan / PR-bots on CI runners produce a diff
			// against the target environment, not the runner.
			if override, ok := ctx.Value(planHostnameKey{}).(string); ok && override != "" {
				val = override
			} else {
				h, err := os.Hostname()
				if err != nil {
					m.opts.log.Warn().
						Str("axis", ax.Dir).
						Err(err).
						Msg("fastconf: os.Hostname failed; skipping host axis")
					continue
				}
				val = h
			}
		}
		if val == "" {
			continue
		}
		rel := path.Join(ax.Dir, val)
		scanOpt.ExtraOverlays = append(scanOpt.ExtraOverlays, discovery.ExtraOverlay{
			Dir:      rel,
			Profile:  ax.Dir + ":" + val,
			Priority: ax.Priority,
		})
	}

	staged := make([]stagedLayer, 0, 8)

	// 1) File layers (base + overlay) in discovery order.
	for layer, err := range discovery.Scan(m.opts.dir, scanOpt) {
		if err != nil {
			return nil, false, err
		}
		src := SourceRef{
			Path:     layer.Path,
			Kind:     mapLayerKind(layer.Kind),
			Profile:  layer.Profile,
			Priority: layer.Priority,
			Codec:    layer.Codec,
		}
		if layer.Kind == discovery.KindPatch {
			raw, derr := decoder.DecodeAny(layer.Codec, layer.Bytes)
			if derr != nil {
				return nil, false, fmt.Errorf("%w: %s: %v", ErrDecode, layer.Path, derr)
			}
			patchBytes, perr := merger.PatchBytesFromAny(raw)
			if perr != nil {
				return nil, false, fmt.Errorf("%w: %s: %v", ErrPatch, layer.Path, perr)
			}
			staged = append(staged, stagedLayer{src: src, patch: patchBytes})
			continue
		}
		dec, derr := decoder.For(layer.Codec)
		if derr != nil {
			return nil, false, fmt.Errorf("%w: %v", ErrDecode, derr)
		}
		raw, derr := dec.Decode(layer.Bytes)
		if derr != nil {
			return nil, false, fmt.Errorf("%w: %s: %v", ErrDecode, layer.Path, derr)
		}
		staged = append(staged, stagedLayer{src: src, data: raw})
	}

	// 2a) Dynamic generators. Run after file discovery so generated layers
	// see file-layer context (e.g. via env), but before providers so
	// providers can override generator output. Generators may emit multiple
	// Sources at distinct priorities (see PriorityGenerator band
	// 7000-7999 by convention; not enforced).
	for _, g := range m.opts.generators {
		srcs, err := g.Generate(ctx)
		if err != nil {
			return nil, false, fmt.Errorf("%w: generator %q: %v", ErrDecode, g.Name(), err)
		}
		for _, gs := range srcs {
			dec, derr := decoder.For(gs.Codec)
			if derr != nil {
				return nil, false, fmt.Errorf("%w: generator %q codec %q: %v", ErrDecode, g.Name(), gs.Codec, derr)
			}
			raw, derr := dec.Decode(gs.Data)
			if derr != nil {
				return nil, false, fmt.Errorf("%w: generator %q: %v", ErrDecode, g.Name(), derr)
			}
			staged = append(staged, stagedLayer{
				src: SourceRef{
					Path:     "gen://" + g.Name() + "/" + gs.Name,
					Kind:     LayerProvider,
					Priority: 7000,
					Codec:    gs.Codec,
				},
				data: raw,
			})
		}
	}

	// 2) Provider layers, sorted by their declared Priority() ascending so
	//    higher-priority providers (CLI > Env > KV) override lower ones.
	if len(m.opts.providers) > 0 {
		ps := make([]providerEntry, 0, len(m.opts.providers))
		for _, p := range m.opts.providers {
			snap, err := loadProviderSnapshot(ctx, p)
			if err != nil {
				// Preserve ctx cancellation as-is so callers can
				// errors.Is(err, context.Canceled / DeadlineExceeded)
				// after a Reload(ctx) timeout instead of wading through
				// ErrDecode wrapping.
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return nil, false, err
				}
				return nil, false, fmt.Errorf("%w: provider %q: %v", ErrDecode, p.Name(), err)
			}
			if snap.Map == nil {
				continue
			}
			if snap.Stale {
				m.opts.log.Warn().
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
			src := SourceRef{
				Path:     "provider://" + e.name,
				Kind:     LayerProvider,
				Priority: 8000 + e.priority,
				Codec:    "",
				Revision: e.revision,
				Stale:    e.stale,
			}
			staged = append(staged, stagedLayer{src: src, data: e.data})
		}
	}

	if len(staged) == 0 {
		return nil, false, ErrNoSources
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

// commit consumes assembled layers, runs the staged pipeline, and on
// success atomically swaps state. The pipeline itself lives in
// pipeline.go; commit() retains only the terminal "publish" duties:
// hash, swap, history, audit, watches.
func (m *Manager[T]) commit(ctx context.Context, staged []stagedLayer, appendSlices bool, reason string) error {
	return m.commitWithKey(ctx, staged, appendSlices, reason, "")
}

// commitWithKey is the variant used by the file-system watcher, which
// supplies a parent-directory key so audit fan-out can attribute the
// reload to the specific watched dir whose burst triggered it.
func (m *Manager[T]) commitWithKey(ctx context.Context, staged []stagedLayer, appendSlices bool, reason, key string) error {
	pc := &pipelineCtx[T]{
		reason:       reason,
		staged:       staged,
		appendSlices: appendSlices,
	}
	if err := m.runStages(ctx, pc); err != nil {
		return err
	}

	// Short-circuit duplicate canonicalHash when mergedJSON has not changed
	// since the last commit. The cache is repopulated below after a
	// successful swap so the first reload always pays the marshal cost
	// (cache miss).
	var hash [32]byte
	if pc.mergedJSON != nil {
		mergedSha := sha256.Sum256(pc.mergedJSON)
		if cached := m.hashCache.Load(); cached != nil && cached.mergedSha == mergedSha {
			hash = cached.stateHash
		} else {
			h, err := canonicalHashBytes(pc.mergedJSON, pc.target, m.opts.codecBridge)
			if err != nil {
				return fmt.Errorf("fastconf: hash: %w", err)
			}
			hash = h
			m.hashCache.Store(&hashCacheEntry{mergedSha: mergedSha, stateHash: hash})
		}
	} else {
		h, err := canonicalHashBytes(pc.mergedJSON, pc.target, m.opts.codecBridge)
		if err != nil {
			return fmt.Errorf("fastconf: hash: %w", err)
		}
		hash = h
	}

	prev := m.state.Load()
	if prev != nil && prev.Hash == hash {
		m.opts.log.Debug().Str("reason", reason).Msg("fastconf reload skipped: identical hash")
		return nil
	}
	gen := m.gen.Add(1)
	cause := ReloadCause{
		Reason:    reason,
		At:        time.Now().UnixNano(),
		Revisions: collectRevisions(pc.sources),
		Key:       key,
	}
	ns := &State[T]{
		Value:      pc.target,
		Hash:       hash,
		LoadedAt:   time.Now().UnixNano(),
		Sources:    pc.sources,
		Generation: gen,
		origins:    pc.origins,
		Cause:      cause,
		redactor:   m.opts.secretRedactor,
	}
	if m.opts.featureExtract != nil {
		ns.features = m.opts.featureExtract(pc.target)
	}
	m.state.Store(ns)
	if m.history != nil {
		m.historyMu.Lock()
		if prev != nil {
			m.history.push(prev)
		}
		m.historyMu.Unlock()
	}
	m.opts.metrics.StateGeneration(gen)
	m.opts.metrics.LayersTotal(len(pc.sources))
	m.opts.log.Info().
		Str("reason", reason).
		Uint64("generation", gen).
		Int("layers", len(pc.sources)).
		Msg("fastconf reload swap")
	for _, sink := range m.opts.auditSinks {
		if err := sink.Audit(context.Background(), cause); err != nil {
			m.opts.log.Warn().Str("reason", reason).Err(err).Msg("fastconf audit sink error")
		}
	}
	m.fireWatches(prev, ns)
	m.fireDiffReporters(prev, ns)
	return nil
}

// collectRevisions, mapLayerKind, canonicalHash live in layers.go / hash.go.

// reload runs the full pipeline. On any failure, state is preserved.
func (m *Manager[T]) reload(ctx context.Context, reason string) error {
	return m.reloadWithKey(ctx, reason, "")
}

// reloadWithKey is the file-system-watcher variant; key is the parent
// dir whose burst triggered this reload (stamped into ReloadCause).
func (m *Manager[T]) reloadWithKey(ctx context.Context, reason, key string) error {
	start := time.Now()
	m.opts.metrics.ReloadStarted()
	m.opts.log.Debug().Str("reason", reason).Msg("fastconf reload start")

	ctx, root := m.startSpan(ctx, "fastconf.reload")
	root.SetAttribute("reason", reason)
	root.SetAttribute("generation", int64(m.gen.Load()))
	defer root.End()

	asmCtx, asmSp := m.startSpan(ctx, "fastconf.assemble")
	staged, appendSlices, err := m.assemble(asmCtx)
	if err != nil {
		asmSp.RecordError(err)
		asmSp.End()
		root.RecordError(err)
		m.opts.metrics.ReloadFinished(false, time.Since(start))
		m.opts.metrics.StageDuration("assemble", time.Since(start), false)
		m.opts.log.Warn().Str("reason", reason).Err(err).Msg("fastconf reload shadow_failed")
		return err
	}
	asmSp.SetAttribute("layers", int64(len(staged)))
	asmSp.End()
	m.opts.metrics.StageDuration("assemble", time.Since(start), true)

	cmtCtx, cmtSp := m.startSpan(ctx, "fastconf.commit")
	commitStart := time.Now()
	if err := m.commitWithKey(cmtCtx, staged, appendSlices, reason, key); err != nil {
		cmtSp.RecordError(err)
		cmtSp.End()
		root.RecordError(err)
		m.opts.metrics.ReloadFinished(false, time.Since(start))
		m.opts.metrics.StageDuration("commit", time.Since(commitStart), false)
		m.opts.log.Warn().Str("reason", reason).Err(err).Msg("fastconf reload commit_failed")
		return err
	}
	cmtSp.End()
	m.opts.metrics.StageDuration("commit", time.Since(commitStart), true)
	m.opts.metrics.ReloadFinished(true, time.Since(start))
	return nil
}

// decodeInto round-trips a map through json to populate *T. We pick json
// (not yaml) so the same byte stream feeds canonicalHash without a
// second marshal. Users whose struct only has yaml tags can opt back
// into the yaml path with WithCodecBridge(BridgeYAML).
func decodeInto[T any](m map[string]any, target *T, bridge codecBridge) ([]byte, error) {
	if bridge == bridgeYAML {
		b, err := yaml.Marshal(m)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(b, target); err != nil {
			return nil, err
		}
		return b, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, target); err != nil {
		return nil, err
	}
	return b, nil
}

// ---------------------------------------------------------------------
// Canonical hashing — used by commit() to skip identical states and by
// dryRun's Plan to fingerprint candidate outputs.
// ---------------------------------------------------------------------

// canonicalHash computes SHA-256 over the JSON encoding of *T.
// encoding/json emits struct fields in declaration order and map keys
// in lexicographic order, giving a stable canonical form for free.
func canonicalHash[T any](v *T) ([32]byte, error) {
	buf, err := json.Marshal(v)
	if err != nil {
		var zero [32]byte
		return zero, err
	}
	return sha256.Sum256(buf), nil
}

// canonicalHashBytes reuses mergedJSON only when that byte stream is
// already the canonical JSON of *T. Struct targets fall back to
// canonicalHash because the merged map may contain ignored fields or a
// different key order than json.Marshal(*T).
func canonicalHashBytes[T any](mergedJSON []byte, v *T, bridge codecBridge) ([32]byte, error) {
	if bridge == bridgeJSON && len(mergedJSON) > 0 {
		if reflect.TypeFor[T]().Kind() == reflect.Map {
			return sha256.Sum256(mergedJSON), nil
		}
	}
	return canonicalHash(v)
}

// hashCacheEntry caches the most recent (mergedJSON sha → state hash) pair
// so an idempotent reload can short-circuit canonicalHash entirely.
// Stored on Manager as atomic.Pointer.
type hashCacheEntry struct {
	mergedSha [32]byte
	stateHash [32]byte
}

// ---------------------------------------------------------------------
// Layer-kind mapping and provider revision extraction.
// ---------------------------------------------------------------------

const providerPathPrefix = "provider://"

// mapLayerKind translates an internal discovery.Kind into the public
// LayerKind enum reported via SourceRef.
func mapLayerKind(k discovery.Kind) LayerKind {
	switch k {
	case discovery.KindMerge:
		return LayerMerge
	case discovery.KindPatch:
		return LayerPatch
	default:
		return LayerUnknown
	}
}

// collectRevisions extracts the per-provider revision map from sources;
// only LayerProvider entries with non-empty Revision are included.
func collectRevisions(sources []SourceRef) map[string]string {
	var out map[string]string
	for _, s := range sources {
		if s.Kind != LayerProvider || s.Revision == "" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		// Strip the "provider://" prefix for readability.
		name := s.Path
		if len(name) > len(providerPathPrefix) && name[:len(providerPathPrefix)] == providerPathPrefix {
			name = name[len(providerPathPrefix):]
		}
		out[name] = s.Revision
	}
	return out
}

// ---------------------------------------------------------------------
// Public Codec registry — bridges fastconf <-> pkg/decoder while keeping
// discovery decoupled from decoder via the CodecExtFunc indirection.
// ---------------------------------------------------------------------

func init() {
	// Bridge the public Codec registry into the discovery package without
	// forcing discovery to import a sibling.
	discovery.CodecExtFunc = decoder.LookupExt
}

// RegisterCodec installs a third-party Codec under the given name.
func RegisterCodec(name string, c contracts.Codec) {
	decoder.Register(name, c)
}

// RegisterCodecExt maps a file extension to a previously-registered codec name.
func RegisterCodecExt(ext, codec string) {
	decoder.RegisterExt(ext, codec)
}

// LookupCodec returns the codec registered under name (case-insensitive).
func LookupCodec(name string) (contracts.Codec, bool) {
	return decoder.Lookup(name)
}

// PlanResult describes the outcome of Manager.Plan.
type PlanResult[T any] struct {
	Proposed   *State[T]
	Diff       []string
	Validators []ValidatorReport
	// Policies holds all policy findings (warnings and errors alike) gathered
	// during the dry-run. Findings with SeverityError would have aborted a real
	// reload; here they are captured for inspection instead.
	Policies []policy.Violation
}

// ValidatorReport is one row in PlanResult.Validators.
type ValidatorReport struct {
	Name string
	Err  error
}

// PlanBuilder is the dry-run builder returned by Manager.Plan(). Use the
// With* chain to tune the preview, then call Run(ctx) to execute.
type PlanBuilder[T any] struct {
	m                *Manager[T]
	hostnameOverride string
}

// planHostnameKey is the context key used to thread the override into
// the assemble path without changing its existing signature.
type planHostnameKey struct{}

// Plan opens a dry-run builder. The actual preview executes when Run is
// called; nothing happens beforehand.
//
//	result, err := m.Plan().
//	    WithHostname("prod-eu-1").
//	    Run(ctx)
func (m *Manager[T]) Plan() *PlanBuilder[T] {
	return &PlanBuilder[T]{m: m}
}

// WithHostname pins the hostname value used to resolve multi-axis
// overlay axes that rely on DefaultFromHostname. Use it from fastconfctl
// plan / PR-bots running on CI runners so the produced diff reflects
// the target environment instead of "ci-runner-7".
func (b *PlanBuilder[T]) WithHostname(host string) *PlanBuilder[T] {
	b.hostnameOverride = host
	return b
}

// Run executes the configured dry-run preview without mutating Manager
// state.
func (b *PlanBuilder[T]) Run(ctx context.Context) (*PlanResult[T], error) {
	if b == nil || b.m == nil {
		return nil, fmt.Errorf("fastconf: nil manager")
	}
	m := b.m
	if b.hostnameOverride != "" {
		ctx = context.WithValue(ctx, planHostnameKey{}, b.hostnameOverride)
	}
	staged, appendSlices, err := m.assemble(ctx)
	if err != nil {
		return nil, err
	}
	pc := &pipelineCtx[T]{
		reason:       "plan",
		staged:       staged,
		appendSlices: appendSlices,
		dryRun:       true,
	}
	if err := m.runStages(ctx, pc); err != nil {
		return nil, err
	}

	hash, err := canonicalHash(pc.target)
	if err != nil {
		return nil, fmt.Errorf("fastconf: hash: %w", err)
	}
	proposed := &State[T]{
		Value:      pc.target,
		Hash:       hash,
		LoadedAt:   time.Now().UnixNano(),
		Sources:    pc.sources,
		Generation: m.gen.Load(), // not incremented; this is dry-run
		redactor:   m.opts.secretRedactor,
	}

	var diff []string
	if cur := m.state.Load(); cur != nil {
		diff = cur.Diff(proposed)
	}
	return &PlanResult[T]{
		Proposed:   proposed,
		Diff:       diff,
		Validators: pc.reports,
		Policies:   pc.policyViolations,
	}, nil
}
