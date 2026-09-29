package source

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/providerutil"
)

// FileSource reads a single auxiliary file (yaml/json/toml/...) at load time and decodes it by
// extension. Change detection is handled by the manager's shared file watcher
// (Describe().WatchPaths); Watch returns (nil, nil).
type FileSource struct {
	path         string
	priority     int
	maxBodyBytes int64
}

// NewFile constructs a FileSource. Default priority is contracts.PriorityStatic, so env and CLI
// providers override it. Override via WithPriority.
func NewFile(path string) *FileSource {
	return &FileSource{path: path, priority: contracts.PriorityStatic, maxBodyBytes: contracts.DefaultMaxBodyBytes}
}

// WithPriority overrides the default priority.
func (f *FileSource) WithPriority(p int) *FileSource { f.priority = p; return f }

// WithMaxBodyBytes limits the file payload before it is decoded. Zero restores the 4 MiB default.
func (f *FileSource) WithMaxBodyBytes(n int64) *FileSource { f.maxBodyBytes = n; return f }

// Name implements contracts.Provider. Returns "file:<path>".
func (f *FileSource) Name() string { return "file:" + f.path }

// Describe implements contracts.Describer. The manager's shared file watcher observes the parent
// directory so atomic symlink swaps are covered.
func (f *FileSource) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: f.priority, WatchPaths: []string{f.path}}
}

// Load implements contracts.Provider. A missing file is an empty layer.
func (f *FileSource) Load(ctx context.Context) (contracts.Snapshot, error) {
	data, ct, rev, err := f.Read(ctx)
	return decode(f.Name(), data, ct, rev, err)
}

// Read returns the raw file, its extension as content-type hint and a stat-based revision. A
// missing file is reported as an empty payload + empty rev (and no error) so the layer can be
// optional. Any other I/O error is propagated.
func (f *FileSource) Read(_ context.Context) ([]byte, string, string, error) {
	file, err := os.Open(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, contentTypeForPath(f.path), "", nil
		}
		return nil, "", "", fmt.Errorf("file source: %w", err)
	}
	defer func() { _ = file.Close() }()
	data, err := providerutil.ReadAllMax(file, f.maxBodyBytes)
	if err != nil {
		return nil, contentTypeForPath(f.path), "", fmt.Errorf("file source: %w", err)
	}
	rev := fileRevision(f.path)
	return data, contentTypeForPath(f.path), rev, nil
}

// Watch implements contracts.Provider. The shared file watcher handles file changes; the provider
// itself does not subscribe.
func (f *FileSource) Watch(context.Context, string) (<-chan contracts.Event, error) {
	return nil, nil
}

func contentTypeForPath(p string) string {
	ext := filepath.Ext(p)
	if ext == "" {
		return ""
	}
	return ext // ".yaml" / ".json" / ...
}

// fileRevision returns a size+mtime hint, not a content hash. Missing files return "".
func fileRevision(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return strconv.FormatInt(st.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(st.Size(), 10)
}
