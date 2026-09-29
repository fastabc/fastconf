// Package k8s reads Kubernetes Downward API labels and annotations. Mount
// metadata.labels and metadata.annotations at /etc/podinfo/{labels,annotations}.
// Files contain KEY="VALUE" lines with quoted-string escapes.
//
// Metadata keys remain raw by default; NewDefault nests them under k8s.metadata.
// MetadataExpanded splits keys using Separators (default slash and dot).
// Describe exposes file paths to the shared parent-directory watcher so atomic
// ..data symlink swaps are detected; no provider-local watcher is started.
package k8s

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/contracts"
)

// Default Downward API file paths set by the Kubernetes documentation.
const (
	DefaultLabelsPath      = "/etc/podinfo/labels"
	DefaultAnnotationsPath = "/etc/podinfo/annotations"
)

// MetadataMode controls whether Downward API keys are preserved verbatim or expanded into nested
// configuration paths.
type MetadataMode uint8

const (
	// MetadataRaw preserves every source key exactly as mounted by kubelet.
	MetadataRaw MetadataMode = iota
	// MetadataExpanded splits keys according to Options.Separators.
	MetadataExpanded
)

// Options configures a DownwardProvider. Either or both of LabelsPath and AnnotationsPath may be
// empty — empty paths are skipped silently.
type Options struct {
	// Name overrides the default Provider name ("k8s-downward").
	Name string
	// Priority sets merge priority. Defaults to contracts.PriorityK8s (40):
	// above remote KV, below process env. Override for site-specific layering.
	Priority int
	// LabelsPath is the file the kubelet writes metadata.labels to.
	// NewDefault sets DefaultLabelsPath; leave empty on low-level New to skip
	// the labels bucket entirely.
	LabelsPath string
	// AnnotationsPath is the file the kubelet writes metadata.annotations
	// to. NewDefault sets DefaultAnnotationsPath; leave empty on low-level New
	// to skip the annotations bucket entirely.
	AnnotationsPath string
	// Separators controls how each label / annotation key is decomposed when
	// the corresponding MetadataMode is MetadataExpanded. Default {"/", "."}
	// matches the K8s recommended label form
	// (app.kubernetes.io/name → app.kubernetes.io.name).
	Separators []string
	// LabelsMode controls whether labels preserve raw keys or expand them.
	// Zero value MetadataRaw is the safe default.
	LabelsMode MetadataMode
	// AnnotationsMode controls whether annotations preserve raw keys or expand
	// them. Zero value MetadataRaw is the safe default.
	AnnotationsMode MetadataMode
	// At, when non-empty, grafts the entire loaded tree under this
	// dotted path. Useful for namespacing K8s metadata away from
	// application configuration:
	//
	//	Options{At: "k8s.metadata"}
	//	// → {"k8s":{"metadata":{"labels":{...},"annotations":{...}}}}
	At string
	// MustExist, when true, fails Load if a configured (non-empty)
	// path does not exist. Default false: missing files are silently
	// skipped so the provider remains a no-op when the Downward API
	// volume is not mounted (typical for unit tests and local runs).
	MustExist bool
}

// DownwardProvider reads K8s Downward API files into a nested configuration tree.
type DownwardProvider struct {
	opts Options
	root []string
}

// New applies default name, priority, separators and raw metadata modes. Empty file paths disable
// those buckets; NewDefault supplies standard paths.
func New(opts Options) *DownwardProvider {
	if opts.Name == "" {
		opts.Name = "k8s-downward"
	}
	if opts.Priority == 0 {
		opts.Priority = contracts.PriorityK8s
	}
	if len(opts.Separators) == 0 {
		opts.Separators = []string{"/", "."}
	}
	return &DownwardProvider{opts: opts, root: confmap.Split(opts.At)}
}

// NewDefault returns the recommended metadata preset for the standard Downward API layout from the
// K8s docs. It preserves raw keys and namespaces the buckets under k8s.metadata.
func NewDefault() *DownwardProvider {
	return New(Options{
		LabelsPath:      DefaultLabelsPath,
		AnnotationsPath: DefaultAnnotationsPath,
		At:              "k8s.metadata",
	})
}

// NewExpandedDefault expands metadata keys into root configuration paths. Use NewDefault to
// preserve raw keys under k8s.metadata instead.
func NewExpandedDefault() *DownwardProvider {
	return New(Options{
		LabelsPath:      DefaultLabelsPath,
		AnnotationsPath: DefaultAnnotationsPath,
		LabelsMode:      MetadataExpanded,
		AnnotationsMode: MetadataExpanded,
	})
}

// Name implements contracts.Provider.
func (p *DownwardProvider) Name() string { return p.opts.Name }

// Describe implements contracts.Describer. WatchPaths lets Manager's shared filesystem watcher
// subscribe to the mounted Downward API files.
func (p *DownwardProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.opts.Priority, WatchPaths: p.watchPaths()}
}

// Load implements contracts.Provider.
func (p *DownwardProvider) Load(ctx context.Context) (contracts.Snapshot, error) {
	m, err := p.loadMap(ctx)
	return contracts.Snapshot{Map: m}, err
}

// Load implements contracts.Provider.
func (p *DownwardProvider) loadMap(_ context.Context) (map[string]any, error) {
	inner := map[string]any{}
	if p.opts.LabelsPath != "" {
		tree, err := p.loadBucket(p.opts.LabelsPath, p.opts.LabelsMode)
		if err != nil {
			return nil, err
		}
		if tree != nil {
			inner["labels"] = tree
		}
	}
	if p.opts.AnnotationsPath != "" {
		tree, err := p.loadBucket(p.opts.AnnotationsPath, p.opts.AnnotationsMode)
		if err != nil {
			return nil, err
		}
		if tree != nil {
			inner["annotations"] = tree
		}
	}
	if len(p.root) == 0 {
		return inner, nil
	}
	out := map[string]any{}
	confmap.Set(out, p.root, inner)
	return out, nil
}

// Watch implements contracts.Provider. See package doc.
func (p *DownwardProvider) Watch(_ context.Context, _ string) (<-chan contracts.Event, error) {
	return nil, nil
}

func (p *DownwardProvider) watchPaths() []string {
	paths := make([]string, 0, 2)
	seen := map[string]struct{}{}
	for _, path := range []string{p.opts.LabelsPath, p.opts.AnnotationsPath} {
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

// loadBucket reads one Downward API file and expands its KEY="VALUE" lines into a nested map.
// Returns (nil, nil) when the file is absent and MustExist is false.
func (p *DownwardProvider) loadBucket(path string, mode MetadataMode) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !p.opts.MustExist {
			return nil, nil
		}
		return nil, fmt.Errorf("k8s downward: read %q: %w", path, err)
	}
	pairs, err := parseDownward(data)
	if err != nil {
		return nil, fmt.Errorf("k8s downward: parse %q: %w", path, err)
	}
	if len(pairs) == 0 {
		return map[string]any{}, nil
	}
	if mode == MetadataRaw {
		raw := make(map[string]any, len(pairs))
		for k, v := range pairs {
			raw[k] = v
		}
		return raw, nil
	}
	return confmap.ExpandLabels(pairs, confmap.LabelOptions{
		Separators: p.opts.Separators,
	}), nil
}

// parseDownward parses KEY="VALUE" lines with Go quoted-string escapes. Malformed lines fail the
// reload rather than silently discarding metadata.
func parseDownward(data []byte) (map[string]string, error) {
	out := map[string]string{}
	lineno := 0
	for _, raw := range strings.Split(string(data), "\n") {
		lineno++
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			return nil, fmt.Errorf("line %d: missing '='", lineno)
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", lineno)
		}
		rest := line[eq+1:]
		if len(rest) == 0 || rest[0] != '"' {
			return nil, fmt.Errorf("line %d: value must be double-quoted", lineno)
		}
		val, err := parseQuoted(rest[1:])
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineno, err)
		}
		out[key] = val
	}
	return out, nil
}

// parseQuoted scans a double-quoted body up to the closing quote, processing the documented escape
// set.
func parseQuoted(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			return b.String(), nil
		}
		if c == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			default:
				b.WriteByte('\\')
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(c)
	}
	return "", fmt.Errorf("unterminated quoted value")
}
