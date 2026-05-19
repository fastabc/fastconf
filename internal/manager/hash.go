package manager

// Canonical hashing + layer-kind mapping + provider-revision extraction
// shared by commit() and Plan(). decodeInto picks JSON by default so the
// same byte stream feeds canonicalHashBytes without a second marshal.

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"sync"

	"gopkg.in/yaml.v3"

	iopts "github.com/fastabc/fastconf/internal/options"
	istate "github.com/fastabc/fastconf/internal/state"
	"github.com/fastabc/fastconf/pkg/discovery"
)

// jsonBufPool reuses the byte slice that backs decodeInto's
// encoder/decoder pair so a steady reload cadence does not balloon
// allocations on every cycle. Buffers grow to whatever the merged map
// needs and are recycled at 64 KiB — anything larger is released to GC
// instead of pinning peak memory forever.
var jsonBufPool = sync.Pool{
	New: func() any {
		buf := bytes.NewBuffer(make([]byte, 0, 4096))
		return buf
	},
}

const jsonBufRetainMax = 64 * 1024

// decodeInto round-trips a map through json to populate *T. We pick json
// (not yaml) so the same byte stream feeds canonicalHash without a
// second marshal. Users whose struct only has yaml tags can opt back
// into the yaml path with WithCodecBridge(BridgeYAML).
//
// Returns a freshly-allocated copy of the marshalled bytes; the working
// buffer is recycled via jsonBufPool. The copy is necessary because
// callers (canonicalHashBytes, ReloadCause audit) retain the slice past
// the lifetime of one stage.
func decodeInto[T any](m map[string]any, target *T, bridge iopts.CodecBridge) ([]byte, error) {
	if bridge == iopts.BridgeYAML {
		b, err := yaml.Marshal(m)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(b, target); err != nil {
			return nil, err
		}
		return b, nil
	}
	buf := jsonBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer func() {
		if buf.Cap() <= jsonBufRetainMax {
			jsonBufPool.Put(buf)
		}
	}()
	enc := json.NewEncoder(buf)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	raw := buf.Bytes()
	// json.Encoder.Encode appends "\n" to terminate the document;
	// json.Marshal did not. Drop it so the returned slice matches the
	// pre-pool behaviour and canonicalHashBytes sees identical input.
	raw = bytes.TrimRight(raw, "\n")
	if err := json.Unmarshal(raw, target); err != nil {
		return nil, err
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return out, nil
}

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
func canonicalHashBytes[T any](mergedJSON []byte, v *T, bridge iopts.CodecBridge) ([32]byte, error) {
	if bridge == iopts.BridgeJSON && len(mergedJSON) > 0 {
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

const providerPathPrefix = "provider://"

// mapLayerKind translates an internal discovery.Kind into the public
// LayerKind enum reported via istate.SourceRef.
func mapLayerKind(k discovery.Kind) istate.LayerKind {
	switch k {
	case discovery.KindMerge:
		return istate.LayerMerge
	case discovery.KindPatch:
		return istate.LayerPatch
	default:
		return istate.LayerUnknown
	}
}

// collectRevisions extracts the per-provider revision map from sources;
// only istate.LayerProvider entries with non-empty Revision are included.
func collectRevisions(sources []istate.SourceRef) map[string]string {
	var out map[string]string
	for _, s := range sources {
		if s.Kind != istate.LayerProvider || s.Revision == "" {
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
