package transform

import (
	"encoding/json"
	"fmt"
	"sync"
)

// RawCapture is a Transformer that snapshots one or more dotted-path values
// as json.RawMessage after the merge phase but before decoding into *T.
// This is the recommended solution for map[string][]json.RawMessage protocol
// blocks: register a RawCapture transformer, decode *T normally, then call
// rawCapture.Get(path) to retrieve the opaque bytes.
//
// RawCapture is safe for concurrent reads (Get/All) after a reload.
//
// Usage:
//
//	rc := transform.CaptureRaw("listeners", "upstreams")
//	mgr, _ := fastconf.New[Config](ctx,
//	    fastconf.WithTransformers(rc),
//	    // ... other options
//	)
//	cfg := mgr.Get()
//	raw, _ := rc.Get("listeners")  // raw is json.RawMessage
type RawCapture struct {
	paths  []string
	mu     sync.RWMutex
	values map[string]json.RawMessage
}

// CaptureRaw returns a new RawCapture transformer that will snapshot the
// values at the given dotted paths on every reload.
func CaptureRaw(paths ...string) *RawCapture {
	return &RawCapture{
		paths:  paths,
		values: make(map[string]json.RawMessage),
	}
}

// Name implements Transformer.
func (r *RawCapture) Name() string { return "CaptureRaw(" + fmt.Sprint(r.paths) + ")" }

// Transform implements Transformer. It snapshots the current value at each
// registered path as JSON bytes. Missing paths are silently skipped and their
// previous captured value is cleared.
func (r *RawCapture) Transform(root map[string]any) error {
	newValues := make(map[string]json.RawMessage, len(r.paths))
	for _, p := range r.paths {
		v, ok := getPath(root, p)
		if !ok {
			continue
		}
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("%w: CaptureRaw path %q: %v", ErrTransform, p, err)
		}
		newValues[p] = b
	}
	r.mu.Lock()
	r.values = newValues
	r.mu.Unlock()
	return nil
}

// Get returns the most recently captured JSON bytes for the given path.
// Returns false if the path was not registered or was missing at last reload.
func (r *RawCapture) Get(path string) (json.RawMessage, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.values[path]
	return v, ok
}

// All returns a snapshot of all captured path → JSON bytes. The returned map
// is a copy and is safe for the caller to retain.
func (r *RawCapture) All() map[string]json.RawMessage {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]json.RawMessage, len(r.values))
	for k, v := range r.values {
		out[k] = v
	}
	return out
}
