package fastconf

// State[T] is the immutable per-reload snapshot atomically published
// via Manager.state. Helpers are grouped into four sections below:
//
//   ── Snapshot ────────  State[T], ReloadCause, Origins/Explain/Lookup/Diff
//   ── Source ──────────  SourceRef, LayerKind
//   ── Provenance ──────  Origin, OriginIndex, ProvenanceLevel
//   ── History ─────────  ringBuffer, Manager.Replay / Manager.Watcher views

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fastabc/fastconf/pkg/feature"
	"gopkg.in/yaml.v3"
)

// ── Snapshot ──────────────────────────────────────────────────────────

// State is an immutable snapshot of the configuration at a point in time.
// Manager replaces it atomically via atomic.Pointer[State[T]] to provide
// lock-free reads.
//
// Callers must treat the *State[T] pointer as read-only.
type State[T any] struct {
	// Value is the strongly-typed configuration struct. Get() returns this pointer directly.
	Value *T
	// Hash is the global SHA-256 fingerprint of *T (based on canonical JSON).
	Hash [32]byte
	// LoadedAt is the Unix nanosecond timestamp when this state was generated.
	LoadedAt int64
	// Sources holds metadata for every layer that participated in this merge.
	Sources []SourceRef
	// Generation is the monotonically increasing version number; incremented on each successful reload.
	Generation uint64
	// origins, when non-nil, lets callers explain which layer wrote to
	// each field. Populated only when WithProvenance(level) is set.
	origins *OriginIndex
	// Cause records why this state was committed: which event triggered
	// the reload, which provider revisions were observed, and an optional
	// caller-supplied request id.
	Cause ReloadCause

	// features holds the feature rule table extracted from this State's
	// *T via WithFeatureRules. nil when no extractor was configured.
	features map[string]feature.Rule

	// redactor is the SecretRedactor stamped at commit-time. Used by
	// Redacted() to produce a default-redacted view without callers
	// having to thread the redactor through.
	redactor SecretRedactor

	// keys holds the lazily-materialised dotted-key view.
	// Filled on first AllKeys/AllSettings/Sub call.
	keys keysHolder
}

// Redacted returns a map[string]any view of the configuration with every
// "secret"-tagged field replaced by the configured SecretRedactor (or
// DefaultSecretRedactor when WithSecretRedactor was not used).
//
// Equivalent to s.Redact(<configured redactor>); use Redact directly when
// you need to apply a different redactor at call time.
//
// Safe to call on a nil receiver; returns nil in that case.
func (s *State[T]) Redacted() map[string]any {
	if s == nil {
		return nil
	}
	r := s.redactor
	if r == nil {
		r = DefaultSecretRedactor
	}
	return s.Redact(r)
}

// FeatureRules returns the feature rule table this State carries. Empty
// when WithFeatureRules was not configured. Pair with feature.Eval when
// you need the untyped runtime value (e.g. OpenFeature integrations);
// for compile-time typed evaluation prefer fastconf.Eval[T,V].
func (s *State[T]) FeatureRules() map[string]feature.Rule {
	if s == nil {
		return nil
	}
	return s.features
}

// ReloadCause is the audit-friendly explanation of a successful commit.
// It is emitted to every AuditSink and surfaced on State[T].Cause so
// downstream tooling can trace an in-process change back to the event
// (file change, provider push, Reload) that drove it.
type ReloadCause struct {
	// Reason mirrors the reloadRequest reason ("initial",
	// "provider:vault://...", "manual", "watcher", ...). Stable string
	// safe for log labels and metric dimensions.
	Reason string
	// At is the wall-clock instant the reload pipeline started.
	At int64
	// Revisions captures every provider's reported revision at the time
	// of assemble (provider name -> revision string). Empty for plain
	// file-only configurations.
	Revisions map[string]string
	// Tenant, when non-empty, identifies which logical tenant this
	// commit belongs to. For single-tenant deployments this is always "".
	Tenant string
	// Key, when non-empty, identifies the watched parent directory whose
	// fsnotify event burst triggered this reload. Populated only for
	// file-system driven reloads (the coalescer keys bursts by parent
	// dir); empty for manual, provider-driven, and initial reloads.
	Key string
}

// Origins returns the per-field origin index; nil when provenance is
// disabled or when called on a nil receiver.
func (s *State[T]) Origins() *OriginIndex {
	if s == nil {
		return nil
	}
	return s.origins
}

// Explain is a shortcut for s.Origins().Explain(path); returns nil
// when provenance is off, the path is unknown, or the receiver is nil.
func (s *State[T]) Explain(path string) []Origin {
	if s == nil {
		return nil
	}
	return s.origins.Explain(path)
}

// Lookup returns every per-layer value recorded for the given dotted
// path, oldest first. The last entry is the winner (the value the
// caller would actually observe via Get). Each entry carries its
// SourceRef and the raw layer value (only populated when
// ProvenanceFull was enabled). Returns nil when provenance is off,
// the path was never written, or the receiver is nil.
func (s *State[T]) Lookup(path string) []Origin {
	if s == nil {
		return nil
	}
	return s.origins.Explain(path)
}

// LookupStrict behaves like Lookup but distinguishes "no provenance"
// from "path unknown" via an error.
func (s *State[T]) LookupStrict(path string) ([]Origin, error) {
	if s == nil || s.origins == nil {
		return nil, ErrNoOrigin
	}
	o := s.origins.Explain(path)
	if len(o) == 0 {
		return nil, ErrNoOrigin
	}
	return o, nil
}

// Diff returns the dotted-path differences between two snapshots
// (typically produced by canonical JSON encoding the *T values).
// The output is stable and human-readable, suitable for tests and
// CLI display. Either operand may be nil; nil is treated as an empty
// configuration so the diff reports every path on the other side.
func (s *State[T]) Diff(other *State[T]) []string {
	a := stateValueMap(s)
	b := stateValueMap(other)
	return diffValueMaps("", a, b)
}

// ── Source ────────────────────────────────────────────────────────────

// SourceRef describes the metadata for a single config layer that participated
// in a merge. Available via State.Sources for diagnostics and tooling.
type SourceRef struct {
	// Path is the stable identifier for the config source: an absolute file path
	// for file layers, or a pseudo-URI like "env://APP_*" for env/cli providers.
	Path string
	// Kind identifies the merge semantics. See LayerKind constants.
	Kind LayerKind
	// Profile is the active overlay name; empty string for base layers.
	Profile string
	// Priority determines merge order: higher values are merged later (higher precedence).
	Priority int
	// Codec is the decoder name: "yaml" | "json" | "" (provider).
	Codec string
	// Revision is the opaque per-provider version string (etcd revision,
	// Vault current_version, Consul ModifyIndex). Empty for file/legacy providers.
	Revision string
	// Stale flags a degraded provider snapshot (best-effort cache).
	Stale bool
}

// LayerKind identifies the merge semantics of a layer.
type LayerKind uint8

const (
	// LayerUnknown is the zero-value placeholder.
	LayerUnknown LayerKind = iota
	// LayerMerge is a standard deep-merge layer.
	LayerMerge
	// LayerPatch is an RFC 6902 JSON Patch layer.
	LayerPatch
	// LayerProvider is a layer injected by a Provider (env/cli/kv/...).
	LayerProvider
	// LayerSecret marks a per-field plaintext supplied by a SecretResolver
	// (SOPS / Vault transit / KMS / age).
	LayerSecret
)

// String returns a human-readable name for the LayerKind.
func (k LayerKind) String() string {
	switch k {
	case LayerMerge:
		return "merge"
	case LayerPatch:
		return "patch"
	case LayerProvider:
		return "provider"
	case LayerSecret:
		return "secret"
	default:
		return "unknown"
	}
}

// ── Provenance ────────────────────────────────────────────────────────

// Origin identifies which configuration layer last wrote a particular
// dotted field path during the merge stage.
//
// Provenance is opt-in via WithProvenance(level): the merger emits an
// OriginIndex only when level > ProvenanceOff; this keeps the default
// reload pipeline allocation-free for installations that don't need
// field-level "where did this come from?" answers.
type Origin struct {
	// Path is the dotted JSON path of the field, e.g. "database.dsn".
	Path string
	// Source is the SourceRef that contributed this value.
	Source SourceRef
	// Value is the per-layer value as it appeared in this Source's
	// contribution before downstream layers overrode it. Only populated
	// when ProvenanceFull is enabled and the value is a JSON-leaf
	// (non-map). Map values are intentionally left nil to avoid
	// retaining large subtrees.
	Value any
}

// ProvenanceLevel controls how aggressively the merger records field
// origins.
//
//	ProvenanceOff       — default; no recording, zero overhead.
//	ProvenanceTopLevel  — only track top-level keys (cheap).
//	ProvenanceFull      — track every leaf path (linear in tree size).
type ProvenanceLevel uint8

const (
	// ProvenanceOff disables origin tracking entirely (default).
	ProvenanceOff ProvenanceLevel = iota
	// ProvenanceTopLevel records only top-level (depth=1) keys.
	ProvenanceTopLevel
	// ProvenanceFull records every leaf path — recommended for CLI
	// "explain" use, but adds O(N) work per reload.
	ProvenanceFull
)

// OriginIndex maps dotted JSON paths to the chain of layers that wrote
// to them, oldest first. The last element wins the merge.
type OriginIndex struct {
	entries map[string][]Origin
	level   ProvenanceLevel
}

func newOriginIndex(level ProvenanceLevel) *OriginIndex {
	if level == ProvenanceOff {
		return nil
	}
	return &OriginIndex{entries: map[string][]Origin{}, level: level}
}

// record annotates path with src. Patches and providers append, the
// chain order is preserved so callers can reconstruct merge history.
func (o *OriginIndex) record(path string, src SourceRef) {
	o.recordValue(path, src, nil)
}

func (o *OriginIndex) recordValue(path string, src SourceRef, val any) {
	if o == nil {
		return
	}
	if o.level == ProvenanceTopLevel && strings.Contains(path, ".") {
		return
	}
	o.entries[path] = append(o.entries[path], Origin{Path: path, Source: src, Value: val})
}

// recordTree walks a freshly-merged map and records every leaf path
// that exists in it as having been written by src. Used by the
// merger after deep-merging a layer so that overlay paths win. The
// walk is depth-limited (256) to defeat pathological YAML anchor/alias
// graphs that produce self-referential maps.
func (o *OriginIndex) recordTree(prefix string, m map[string]any, src SourceRef) {
	o.recordTreeDepth(prefix, m, src, 0)
}

const maxProvenanceDepth = 256

func (o *OriginIndex) recordTreeDepth(prefix string, m map[string]any, src SourceRef, depth int) {
	if o == nil || depth > maxProvenanceDepth {
		return
	}
	for k, v := range m {
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		switch nested := v.(type) {
		case map[string]any:
			if o.level == ProvenanceFull {
				o.recordTreeDepth(full, nested, src, depth+1)
			} else {
				o.record(full, src)
			}
		default:
			o.recordValue(full, src, v)
		}
	}
}

// Explain returns the chain of layers that contributed to the given
// dotted field path. The chain is oldest→newest; the last element
// "won" the merge. An unknown path yields nil.
func (o *OriginIndex) Explain(path string) []Origin {
	if o == nil {
		return nil
	}
	chain := o.entries[path]
	if chain == nil {
		return nil
	}
	out := make([]Origin, len(chain))
	copy(out, chain)
	return out
}

// Paths returns every recorded path in deterministic order, useful
// for CLI listings and tests.
func (o *OriginIndex) Paths() []string {
	if o == nil {
		return nil
	}
	out := make([]string, 0, len(o.entries))
	for k := range o.entries {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Format renders an explain entry as one line per origin.
func (o *OriginIndex) Format(path string) string {
	chain := o.Explain(path)
	if len(chain) == 0 {
		return fmt.Sprintf("%s: <no origin recorded>", path)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s:\n", path)
	for i, e := range chain {
		marker := " "
		if i == len(chain)-1 {
			marker = "*" // "winner"
		}
		fmt.Fprintf(&b, "  %s [%d] %s (%s)\n", marker, e.Source.Priority, e.Source.Path, e.Source.Kind)
	}
	return b.String()
}

// ── History ───────────────────────────────────────────────────────────

// ErrUnknownGeneration is returned by Rollback when the requested
// generation is not in the in-memory history ring.
var ErrUnknownGeneration = errors.New("fastconf: unknown generation")

// ErrHistoryDisabled is returned when history APIs are called but
// WithHistory was not used.
var ErrHistoryDisabled = errors.New("fastconf: history disabled")

// ringBuffer is a tiny FIFO of past *State[T] snapshots. It is owned
// by the Manager and protected by historyMu; reads use a copy slice
// so callers don't see torn writes. The implementation is a true
// circular buffer (constant-time push) to keep reload's tail latency
// flat even at high history caps.
type ringBuffer[T any] struct {
	cap   int
	items []*State[T] // fixed-length cap slice
	head  int         // index of oldest element
	size  int         // current number of valid elements
}

func newRing[T any](cap int) *ringBuffer[T] {
	if cap <= 0 {
		return nil
	}
	return &ringBuffer[T]{cap: cap, items: make([]*State[T], cap)}
}

func (r *ringBuffer[T]) push(s *State[T]) {
	if r == nil {
		return
	}
	tail := (r.head + r.size) % r.cap
	r.items[tail] = s
	if r.size < r.cap {
		r.size++
	} else {
		r.head = (r.head + 1) % r.cap
	}
}

func (r *ringBuffer[T]) snapshot() []*State[T] {
	if r == nil {
		return nil
	}
	out := make([]*State[T], r.size)
	for i := 0; i < r.size; i++ {
		out[i] = r.items[(r.head+i)%r.cap]
	}
	return out
}

func (r *ringBuffer[T]) findGeneration(gen uint64) *State[T] {
	if r == nil {
		return nil
	}
	for i := 0; i < r.size; i++ {
		s := r.items[(r.head+i)%r.cap]
		if s != nil && s.Generation == gen {
			return s
		}
	}
	return nil
}

// Replay is the sub-namespace accessor that exposes time-travel
// operations on the Manager's history ring (configured via WithHistory).
// Returns a zero-cost view; methods short-circuit when history is
// disabled.
//
//	for _, s := range m.Replay().List() {
//	    fmt.Println(s.Generation, s.Hash)
//	}
//	_ = m.Replay().Rollback(prev)
func (m *Manager[T]) Replay() *Replay[T] { return (*Replay[T])(m) }

// Replay is the time-travel sub-API. Created via Manager.Replay().
type Replay[T any] Manager[T]

// List returns up to cap previously committed snapshots, oldest first.
// Returns an empty slice if WithHistory(n) was not configured.
func (r *Replay[T]) List() []*State[T] {
	m := (*Manager[T])(r)
	if m.history == nil {
		return nil
	}
	m.historyMu.Lock()
	defer m.historyMu.Unlock()
	return m.history.snapshot()
}

// Rollback atomically swaps the active state to the supplied snapshot,
// provided it is still retained in the history ring. The swap is
// serialized through the single-writer reloadCh so it cannot race with
// an in-flight reload pipeline.
//
// Returns ErrHistoryDisabled when WithHistory(n) was not configured, and
// ErrUnknownGeneration when target is not (or no longer) in the ring.
func (r *Replay[T]) Rollback(target *State[T]) error {
	m := (*Manager[T])(r)
	if m.history == nil {
		return ErrHistoryDisabled
	}
	if target == nil {
		return fmt.Errorf("%w: nil target", ErrUnknownGeneration)
	}
	m.historyMu.Lock()
	found := m.history.findGeneration(target.Generation)
	m.historyMu.Unlock()
	if found != target {
		return fmt.Errorf("%w: generation %d not in history", ErrUnknownGeneration, target.Generation)
	}

	req := reloadRequest{
		reason: "rollback",
		applyFn: func(_ context.Context) error {
			// Rollback is an in-memory pointer swap + fan-out; there is
			// no cancellable I/O to honour, so we accept ctx for the
			// applyFn shape but intentionally ignore it.
			return m.applyRollback(target)
		},
		doneCh: make(chan error, 1),
	}
	select {
	case m.reloadCh <- req:
	case <-m.closed:
		return ErrClosed
	}
	select {
	case err := <-req.doneCh:
		return err
	case <-m.closed:
		return ErrClosed
	}
}

// applyRollback executes the rollback state-swap. Must only be called
// from the single-writer reloadLoop goroutine (via a reloadRequest.applyFn).
func (m *Manager[T]) applyRollback(target *State[T]) error {
	prev := m.state.Load()
	m.state.Store(target)
	// Advance the generation counter so the next successful reload is
	// strictly newer than the rolled-back snapshot.
	for {
		cur := m.gen.Load()
		next := target.Generation + 1
		if cur >= next {
			break
		}
		if m.gen.CompareAndSwap(cur, next) {
			break
		}
	}
	m.opts.log.Info().
		Uint64("from", prev.Generation).
		Uint64("to", target.Generation).
		Msg("fastconf rollback")
	m.fireWatches(prev, target)
	return nil
}

// Watcher is the sub-namespace accessor for watch-loop control. Returns
// a zero-cost view onto Manager. Methods are nil-safe-ish: they only
// operate on the watchPaused atomic, which is always present.
//
//	m.Watcher().Pause()
//	defer m.Watcher().Resume()
func (m *Manager[T]) Watcher() *Watcher[T] { return (*Watcher[T])(m) }

// Watcher is the watch-loop control sub-API. Created via Manager.Watcher().
type Watcher[T any] Manager[T]

// Pause stops the manager from honouring file/provider events until
// Resume is called. Manual Reload() still works. Pausing is best-effort:
// events that arrived before the pause may still be processed.
func (w *Watcher[T]) Pause() { (*Manager[T])(w).watchPaused.Store(true) }

// Resume re-enables file/provider event processing after Pause.
func (w *Watcher[T]) Resume() { (*Manager[T])(w).watchPaused.Store(false) }

// Paused reports the current pause state.
func (w *Watcher[T]) Paused() bool { return (*Manager[T])(w).watchPaused.Load() }

// stateValueMap turns a *State[T] into a generic map for diffing.
// It uses encoding/json so user struct tags govern the field names.
func stateValueMap[T any](s *State[T]) map[string]any {
	if s == nil || s.Value == nil {
		return nil
	}
	buf, err := json.Marshal(s.Value)
	if err != nil {
		return nil
	}
	out := map[string]any{}
	_ = json.Unmarshal(buf, &out)
	return out
}

func diffValueMaps(prefix string, a, b map[string]any) []string {
	keys := map[string]struct{}{}
	for k := range a {
		keys[k] = struct{}{}
	}
	for k := range b {
		keys[k] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	var out []string
	for _, k := range ordered {
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		va, oka := a[k]
		vb, okb := b[k]
		switch {
		case oka && !okb:
			out = append(out, fmt.Sprintf("- %s = %v", full, va))
		case !oka && okb:
			out = append(out, fmt.Sprintf("+ %s = %v", full, vb))
		default:
			ma, _ := va.(map[string]any)
			mb, _ := vb.(map[string]any)
			if ma != nil && mb != nil {
				out = append(out, diffValueMaps(full, ma, mb)...)
				continue
			}
			ja, _ := json.Marshal(va)
			jb, _ := json.Marshal(vb)
			if string(ja) != string(jb) {
				out = append(out, fmt.Sprintf("~ %s : %v -> %v", full, va, vb))
			}
		}
	}
	return out
}

// ── YAML dump ─────────────────────────────────────────────────────────

// MarshalYAML returns a deterministic YAML encoding of the State's
// merged settings. Map keys are emitted in lexicographic order so
// operator-driven diff tooling produces stable output across reloads.
//
// When redactor is non-nil, every `fastconf:"secret"` field is replaced
// in the output via redactor(path, value). Pass DefaultSecretRedactor
// for the standard "***REDACTED***" mask, or a custom redactor for
// alternative display logic. When redactor is nil the raw values are
// emitted (callers must redact upstream if sensitivity matters).
func (s *State[T]) MarshalYAML(redactor SecretRedactor) ([]byte, error) {
	if s == nil {
		return []byte("{}\n"), nil
	}
	var tree map[string]any
	if redactor != nil {
		// Redact returns a nested map with secret paths already masked.
		// Empty when the state value is nil; fall through to the regular
		// Settings path in that case so we still emit a deterministic
		// "{}\n" instead of YAML null.
		tree = s.Redact(redactor)
	}
	if tree == nil {
		tree = unflattenForYAML(s.Introspect().Settings())
	}
	return yaml.Marshal(orderedYAMLNode(tree))
}

// unflattenForYAML turns a flat dotted-key map (AllSettings shape) into
// a nested map[string]any tree.
func unflattenForYAML(flat map[string]any) map[string]any {
	out := map[string]any{}
	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		setDottedYAML(out, k, flat[k])
	}
	return out
}

func setDottedYAML(root map[string]any, dotted string, v any) {
	cur := root
	start := 0
	for i := 0; i <= len(dotted); i++ {
		if i == len(dotted) || dotted[i] == '.' {
			part := dotted[start:i]
			if i == len(dotted) {
				cur[part] = v
				return
			}
			next, ok := cur[part].(map[string]any)
			if !ok {
				next = map[string]any{}
				cur[part] = next
			}
			cur = next
			start = i + 1
		}
	}
}

// orderedYAMLNode recursively converts map[string]any into a yaml.Node
// with keys sorted lexicographically — yaml.v3 doesn't preserve map
// order otherwise, so operator-driven diff would flake.
func orderedYAMLNode(v any) *yaml.Node {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		node := &yaml.Node{Kind: yaml.MappingNode}
		for _, k := range keys {
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: k},
				orderedYAMLNode(t[k]),
			)
		}
		return node
	case []any:
		node := &yaml.Node{Kind: yaml.SequenceNode}
		for _, e := range t {
			node.Content = append(node.Content, orderedYAMLNode(e))
		}
		return node
	default:
		n := &yaml.Node{}
		_ = n.Encode(v)
		return n
	}
}

// DiffReporter receives an event after every successful reload that
// changed at least one field. Implementations MUST be goroutine-safe.
// The Report method is invoked on a fresh goroutine so it may block
// without affecting reload latency, but it SHOULD still bound its
// own time spent (e.g. with an HTTP timeout).
type DiffReporter interface {
	Report(ctx context.Context, ev DiffEvent) error
}

// DiffReporterFunc adapts a function into a DiffReporter.
type DiffReporterFunc func(context.Context, DiffEvent) error

// Report implements DiffReporter.
func (f DiffReporterFunc) Report(ctx context.Context, ev DiffEvent) error {
	return f(ctx, ev)
}

// DiffEvent is the payload handed to every DiffReporter.
type DiffEvent struct {
	// Reason mirrors ReloadCause.Reason — "manual", "watcher",
	// "provider:vault://...", "override", etc.
	Reason string
	// PrevGeneration is the generation number of the State that was
	// just replaced; zero on the first reload.
	PrevGeneration uint64
	// NewGeneration is the generation number just published.
	NewGeneration uint64
	// At captures when the reload swap occurred.
	At time.Time
	// Diff is the human-readable list of dotted paths that changed,
	// produced by State.Diff. Empty when the previous state had a
	// different hash but identical field values (which should be rare
	// once canonicalisation has run).
	Diff []string
	// Cause is the full ReloadCause for downstream tooling that needs
	// the audit trail (revisions, tenant, request id, ...).
	Cause ReloadCause
}

// WithDiffReporter installs a reporter invoked asynchronously after
// every successful reload that produced a non-empty diff. Multiple
// reporters can be installed; each runs on its own dedicated worker
// goroutine fed by a bounded queue.
//
// Backpressure: events are enqueued non-blockingly. When a reporter's
// queue is full (slow reporter + high reload churn) the event is DROPPED
// and EventDropped("diff-reporter") is reported to the MetricsSink.
// Reload throughput is therefore independent of reporter latency.
//
// Tune the per-reporter queue depth with WithDiffReporterQueueCap.
func WithDiffReporter(r DiffReporter) Option {
	return func(o *options) {
		if r == nil {
			return
		}
		o.diffReporters = append(o.diffReporters, r)
	}
}

// WithDiffReporterQueueCap sets the per-reporter bounded queue depth used
// for backpressure when fan-out cannot keep up with reload churn. Default
// is defaultDiffReporterQueueCap (64). n < 1 is clamped to 1.
func WithDiffReporterQueueCap(n int) Option {
	return func(o *options) {
		if n < 1 {
			n = 1
		}
		o.diffReporterQueueCap = n
	}
}

// diffReporterWorker owns a bounded queue and a single goroutine that
// drains it. Per-reporter isolation prevents one slow reporter from
// starving another.
//
// label is the stable identifier surfaced to MetricsSink (EventDropped,
// DiffReporterQueueDepth). Format: "diff-reporter:<idx>".
type diffReporterWorker struct {
	r     DiffReporter
	ch    chan DiffEvent
	label string
}

// startDiffReporterWorkers spawns one worker per installed reporter.
// Called from New() once construction has succeeded. Workers exit when
// m.closed fires.
//
// We intentionally do NOT close the per-worker channel during shutdown:
// a reload pipeline can still be running when Close() sets m.closed, and
// closing the channel from a different goroutine would race with
// fireDiffReporters' send. By signalling shutdown only via m.closed,
// the worker exits cleanly and any in-flight non-blocking send becomes a
// drop-on-full no-op (the buffered channel is still valid memory).
func (m *Manager[T]) startDiffReporterWorkers() {
	if len(m.opts.diffReporters) == 0 {
		return
	}
	qcap := m.opts.diffReporterQueueCap
	if qcap <= 0 {
		qcap = defaultDiffReporterQueueCap
	}
	m.diffReporterWorkers = make([]*diffReporterWorker, 0, len(m.opts.diffReporters))
	for i, r := range m.opts.diffReporters {
		w := &diffReporterWorker{
			r:     r,
			ch:    make(chan DiffEvent, qcap),
			label: fmt.Sprintf("diff-reporter:%d", i),
		}
		m.diffReporterWorkers = append(m.diffReporterWorkers, w)
		m.bgWG.Add(1)
		go m.runDiffReporterWorker(w)
	}
}

func (m *Manager[T]) runDiffReporterWorker(w *diffReporterWorker) {
	defer m.bgWG.Done()
	for {
		select {
		case <-m.closed:
			return
		case ev := <-w.ch:
			if err := w.r.Report(context.Background(), ev); err != nil {
				m.opts.log.Warn().Err(err).Msg("fastconf diff reporter error")
			}
		}
	}
}

// fireDiffReporters enqueues the diff event to every installed reporter.
// Enqueue is non-blocking: when a reporter's bounded queue is full the
// event is dropped and the metrics sink is notified. This decouples
// reload latency from reporter latency.
func (m *Manager[T]) fireDiffReporters(prev, ns *State[T]) {
	if len(m.diffReporterWorkers) == 0 {
		return
	}
	if prev == nil {
		return
	}
	diff := prev.Diff(ns)
	if len(diff) == 0 {
		return
	}
	ev := DiffEvent{
		Reason:         ns.Cause.Reason,
		PrevGeneration: prev.Generation,
		NewGeneration:  ns.Generation,
		At:             time.Unix(0, ns.LoadedAt),
		Diff:           diff,
		Cause:          ns.Cause,
	}
	for _, w := range m.diffReporterWorkers {
		select {
		case w.ch <- ev:
		default:
			m.opts.metrics.EventDropped(w.label)
			m.opts.log.Warn().
				Str("reporter", w.label).
				Uint64("prev_gen", prev.Generation).
				Uint64("new_gen", ns.Generation).
				Msg("fastconf diff reporter queue full; event dropped")
		}
		// Sample the queue depth after every enqueue attempt so the
		// gauge tracks both successful inserts and post-drop occupancy.
		m.opts.metrics.DiffReporterQueueDepth(w.label, len(w.ch), cap(w.ch))
	}
}
