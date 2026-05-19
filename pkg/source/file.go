package source

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/fastabc/fastconf/contracts"
)

// FileSource reads a single auxiliary file (yaml/json/toml/...) at
// load time. Watching is handled by the global file-watcher subsystem
// rather than the Source; Watch returns (nil, nil).
//
// The content-type returned by Read is the lowercase file extension
// without the leading dot ("yaml", "json", "toml"). Bind uses this
// hint to auto-select a Parser when WithSource is called with nil.
type FileSource struct {
	path     string
	priority int
}

// NewFile constructs a FileSource. Default priority is
// contracts.BandProvider (above generator layers, below
// provider/CLI). Override via WithPriority.
func NewFile(path string) *FileSource {
	return &FileSource{path: path, priority: contracts.BandProvider}
}

// WithPriority overrides the default priority.
func (f *FileSource) WithPriority(p int) *FileSource { f.priority = p; return f }

// Name implements contracts.Source. Returns "file:<path>".
func (f *FileSource) Name() string { return "file:" + f.path }

// Priority implements contracts.Source.
func (f *FileSource) Priority() int { return f.priority }

// Read implements contracts.Source. A missing file is reported as an
// empty payload + empty rev (and no error) so the layer can be
// optional. Any other I/O error is propagated.
func (f *FileSource) Read(_ context.Context) ([]byte, string, string, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, contentTypeForPath(f.path), "", nil
		}
		return nil, "", "", fmt.Errorf("file source: %w", err)
	}
	rev := fileRevision(f.path)
	return data, contentTypeForPath(f.path), rev, nil
}

// Watch implements contracts.Source. The global file-watcher handles
// file changes; the Source itself does not subscribe.
func (f *FileSource) Watch(_ context.Context) (<-chan contracts.Event, error) {
	return nil, nil
}

func contentTypeForPath(p string) string {
	ext := filepath.Ext(p)
	if ext == "" {
		return ""
	}
	return ext // ".yaml" / ".json" / ...
}

// fileRevision returns a cheap stable fingerprint based on the file's
// stat. Identical inode+size+mtime ⇒ identical revision. Missing file
// returns "".
func fileRevision(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return strconv.FormatInt(st.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(st.Size(), 10)
}
