// Package transform provides post-merge, pre-decode map transformations. Functions run in
// registration order; errors abort publication. Dotted paths address maps, not slice indices; use
// RFC 6902 patches for indexed edits. Defaults belong in struct tags or Defaulter, and labels in
// providers/labels.
package transform

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/fastabc/fastconf/confmap"
)

// Func mutates the merged configuration tree; returning an error aborts the reload. It is the
// function type fastconf.WithTransform accepts. Funcs run serially within one reload.
type Func = func(root map[string]any) error

// Aliases returns a transform that rewrites old keys to their new home. If the target path already
// has a value the new world wins and the alias is dropped.
func Aliases(mapping map[string]string) Func {
	return func(root map[string]any) error {
		for from, to := range mapping {
			v, ok := confmap.GetDotted(root, from)
			if !ok {
				continue
			}
			if _, exists := confmap.GetDotted(root, to); !exists {
				confmap.SetDotted(root, to, v)
			}
			confmap.DeleteDotted(root, from)
		}
		return nil
	}
}

// DeletePaths returns a transform that removes the specified dotted-path keys from the tree.
// Missing paths are silently ignored.
func DeletePaths(paths ...string) Func {
	return func(root map[string]any) error {
		for _, p := range paths {
			confmap.DeleteDotted(root, p)
		}
		return nil
	}
}

// RawCapture captures selected paths as JSON before typed decoding. Register its Transform method
// with WithTransform. Get and All are safe for concurrent reads after reload; treat returned bytes
// as read-only.
type RawCapture struct {
	paths  []string
	mu     sync.RWMutex
	values map[string]json.RawMessage
}

// CaptureRaw returns a new RawCapture transformer that will snapshot the values at the given
// dotted paths on every reload.
func CaptureRaw(paths ...string) *RawCapture {
	return &RawCapture{
		paths:  paths,
		values: make(map[string]json.RawMessage),
	}
}

// Transform is the transform to pass to fastconf.WithTransform. It snapshots the current value at
// each registered path as JSON bytes. Missing paths are silently skipped and their previous
// captured value is cleared.
func (r *RawCapture) Transform(root map[string]any) error {
	newValues := make(map[string]json.RawMessage, len(r.paths))
	for _, p := range r.paths {
		v, ok := confmap.GetDotted(root, p)
		if !ok {
			continue
		}
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("CaptureRaw path %q: %w", p, err)
		}
		newValues[p] = b
	}
	r.mu.Lock()
	r.values = newValues
	r.mu.Unlock()
	return nil
}

// Get returns the most recently captured JSON bytes for the given path. Returns false if the path
// was not registered or was missing at last reload.
func (r *RawCapture) Get(path string) (json.RawMessage, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.values[path]
	return v, ok
}

// All returns a snapshot of all captured path → JSON bytes. The returned map is a copy and is safe
// for the caller to retain.
func (r *RawCapture) All() map[string]json.RawMessage {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]json.RawMessage, len(r.values))
	for k, v := range r.values {
		out[k] = v
	}
	return out
}
