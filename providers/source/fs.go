package source

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"path"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/providerutil"
)

// FS reads one document from an fs.FS, including embed.FS. It has no automatic
// watcher; Reload reads the file again. Unlike NewFile, missing files are errors.
type FS struct {
	fsys         fs.FS
	path         string
	priority     int
	maxBodyBytes int64
}

// NewFS constructs a single-file provider. Paths are relative to fsys and must
// satisfy fs.ValidPath. The document codec is inferred from the file extension.
func NewFS(fsys fs.FS, name string) *FS {
	return &FS{fsys: fsys, path: name, priority: contracts.PriorityStatic, maxBodyBytes: contracts.DefaultMaxBodyBytes}
}

// WithPriority overrides the default static priority.
func (f *FS) WithPriority(p int) *FS { f.priority = p; return f }

// WithMaxBodyBytes limits the document before decoding. Non-positive values
// restore the default 4 MiB limit.
func (f *FS) WithMaxBodyBytes(n int64) *FS { f.maxBodyBytes = n; return f }

// Name implements contracts.Provider.
func (f *FS) Name() string { return "fs:" + f.path }

// Describe implements contracts.Describer. Arbitrary filesystems have no OS watch paths.
func (f *FS) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: f.priority}
}

// Load opens, bounds and decodes the document. Revision is a hash of its bytes.
func (f *FS) Load(ctx context.Context) (contracts.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return contracts.Snapshot{}, err
	}
	if f.fsys == nil {
		return contracts.Snapshot{}, fmt.Errorf("%s: filesystem is required", f.Name())
	}
	if !fs.ValidPath(f.path) {
		return contracts.Snapshot{}, fmt.Errorf("%s: %w", f.Name(), fs.ErrInvalid)
	}
	file, err := f.fsys.Open(f.path)
	if err != nil {
		return contracts.Snapshot{}, fmt.Errorf("%s: %w", f.Name(), err)
	}
	defer func() { _ = file.Close() }()
	data, err := providerutil.ReadAllMax(file, f.maxBodyBytes)
	if err != nil {
		return contracts.Snapshot{}, fmt.Errorf("%s: %w", f.Name(), err)
	}
	if err := ctx.Err(); err != nil {
		return contracts.Snapshot{}, err
	}
	rev := fmt.Sprintf("%x", sha256.Sum256(data))
	return decode(f.Name(), data, path.Ext(f.path), rev, nil)
}

var _ contracts.Provider = (*FS)(nil)
var _ contracts.Describer = (*FS)(nil)
