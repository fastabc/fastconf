//go:build !no_provider_k8s

// Package k8s implements first-party FastConf providers for Kubernetes
// integration. The DownwardProvider reads metadata.labels and
// metadata.annotations from the Downward API volume that the kubelet
// mounts into the pod, exposing them as nested configuration paths
// alongside file layers and other providers.
//
// # Mounting the Downward API
//
// The provider expects the standard layout:
//
//	volumes:
//	  - name: podinfo
//	    downwardAPI:
//	      items:
//	        - path: "labels"
//	          fieldRef: { fieldPath: metadata.labels }
//	        - path: "annotations"
//	          fieldRef: { fieldPath: metadata.annotations }
//	volumeMounts:
//	  - name: podinfo
//	    mountPath: /etc/podinfo
//
// With this mount, the kubelet writes one KEY="VALUE" line per label
// and annotation into /etc/podinfo/labels and /etc/podinfo/annotations.
// Multi-line annotation values are escaped with \n; KEY may contain
// "/" and "." (matching K8s recommended-label conventions).
//
// # Key decomposition
//
// Keys are split with Separators (default {"/", "."}) so the K8s
// recommended label "app.kubernetes.io/name" decomposes into the
// nested path "app.kubernetes.io.name". Pass a custom Separators
// list to match other conventions.
//
// # Watch
//
// The kubelet rewrites the mounted files in-place when labels or
// annotations change, but the rewrite cadence is per-sync-period
// (~60s by default) and not signalled to the container. The provider
// therefore returns (nil, nil) from Watch; trigger Manager.Reload(ctx)
// on whatever signal your operator already uses (SIGHUP, controller
// reconcile, etc.).
package k8s

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/mappath"
)

// Default Downward API file paths set by the Kubernetes documentation.
const (
	DefaultLabelsPath      = "/etc/podinfo/labels"
	DefaultAnnotationsPath = "/etc/podinfo/annotations"
)

// Options configures a DownwardProvider. Either or both of LabelsPath
// and AnnotationsPath may be empty — empty paths are skipped silently.
type Options struct {
	// Name overrides the default Provider name ("k8s-downward").
	Name string
	// Priority sets merge priority. Defaults to contracts.PriorityK8s (40):
	// above remote KV, below process env. Override for site-specific layering.
	Priority int
	// LabelsPath is the file the kubelet writes metadata.labels to.
	// Defaults to DefaultLabelsPath; pass an explicit "" to disable
	// the labels bucket entirely.
	LabelsPath string
	// AnnotationsPath is the file the kubelet writes metadata.annotations
	// to. Defaults to DefaultAnnotationsPath; pass "" to disable.
	AnnotationsPath string
	// Separators controls how each label / annotation key is decomposed
	// into a nested path. Default {"/", "."} matches the K8s recommended
	// label form (app.kubernetes.io/name → app.kubernetes.io.name).
	Separators []string
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

// DownwardProvider reads K8s Downward API files into a nested
// configuration tree.
type DownwardProvider struct {
	opts Options
	root []string
}

// New builds a DownwardProvider with the given options. Apply Options
// defaults: empty Name → "k8s-downward"; zero Priority → PriorityK8s;
// empty Separators → {"/", "."}; LabelsPath/AnnotationsPath unset only
// when explicitly zeroed (struct literal omits LabelsPath leaves it ""
// which means "skip" — pass DefaultLabelsPath explicitly to enable).
//
// For the "I want the defaults" path use NewDefault().
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
	return &DownwardProvider{opts: opts, root: mappath.Split(opts.At)}
}

// NewDefault is a shortcut for New(Options{
//     LabelsPath: DefaultLabelsPath,
//     AnnotationsPath: DefaultAnnotationsPath,
// }) — the standard Downward API layout from the K8s docs.
func NewDefault() *DownwardProvider {
	return New(Options{
		LabelsPath:      DefaultLabelsPath,
		AnnotationsPath: DefaultAnnotationsPath,
	})
}

// Name implements contracts.Provider.
func (p *DownwardProvider) Name() string { return p.opts.Name }

// Priority implements contracts.Provider.
func (p *DownwardProvider) Priority() int { return p.opts.Priority }

// Load implements contracts.Provider.
func (p *DownwardProvider) Load(_ context.Context) (map[string]any, error) {
	inner := map[string]any{}
	if p.opts.LabelsPath != "" {
		tree, err := p.loadBucket(p.opts.LabelsPath)
		if err != nil {
			return nil, err
		}
		if tree != nil {
			inner["labels"] = tree
		}
	}
	if p.opts.AnnotationsPath != "" {
		tree, err := p.loadBucket(p.opts.AnnotationsPath)
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
	mappath.Set(out, p.root, inner)
	return out, nil
}

// Watch implements contracts.Provider. See package doc.
func (p *DownwardProvider) Watch(_ context.Context) (<-chan contracts.Event, error) {
	return nil, nil
}

// loadBucket reads one Downward API file and expands its KEY="VALUE"
// lines into a nested map. Returns (nil, nil) when the file is absent
// and MustExist is false.
func (p *DownwardProvider) loadBucket(path string) (map[string]any, error) {
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
	return mappath.ExpandLabels(pairs, mappath.LabelOptions{
		Separators: p.opts.Separators,
	}), nil
}

// parseDownward parses a Downward API file body into a key→value map.
// Each non-empty, non-comment line has the form:
//
//	KEY="VALUE"
//
// where VALUE supports the same backslash escapes as a double-quoted
// string ( \n \t \" \\ ). Lines that do not match this form are
// reported as errors so a corrupted mount fails the reload rather
// than silently dropping data.
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

// parseQuoted scans a double-quoted body up to the closing quote,
// processing the documented escape set.
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
