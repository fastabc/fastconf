package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/decoder"
)

// FileProvider loads a single auxiliary file (yaml/json) outside the main
// conf.d/ tree — useful for site-specific overrides like
// /etc/myapp/override.yaml.
type FileProvider struct {
	path     string
	priority int
}

// NewFile builds a FileProvider. Codec is inferred from the file extension.
func NewFile(path string, priority int) *FileProvider {
	return &FileProvider{path: path, priority: priority}
}

// Name implements Provider.
func (p *FileProvider) Name() string { return "file:" + p.path }

// Priority implements Provider.
func (p *FileProvider) Priority() int { return p.priority }

// Load implements Provider.
func (p *FileProvider) Load(_ context.Context) (map[string]any, error) {
	data, err := os.ReadFile(p.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("file provider: %w", err)
	}
	codec := decoder.CodecFromExt(strings.TrimPrefix(filepath.Ext(p.path), "."))
	if codec == "" {
		return nil, fmt.Errorf("file provider: unknown extension on %q", p.path)
	}
	dec, err := decoder.For(codec)
	if err != nil {
		return nil, err
	}
	return dec.Decode(data)
}

// Watch implements Provider. File watching is handled by the dedicated
// Watcher subsystem rather than the provider abstraction.
func (p *FileProvider) Watch(_ context.Context) (<-chan contracts.Event, error) { return nil, nil }
