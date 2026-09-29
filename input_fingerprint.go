package fastconf

import (
	"crypto/sha256"
	"encoding"
	"encoding/binary"
	"encoding/json"
	"reflect"

	"github.com/fastabc/fastconf/codec"
	"gopkg.in/yaml.v3"
)

var customEncodingTypes = []reflect.Type{
	reflect.TypeFor[json.Marshaler](), reflect.TypeFor[json.Unmarshaler](),
	reflect.TypeFor[encoding.TextMarshaler](), reflect.TypeFor[encoding.TextUnmarshaler](),
	reflect.TypeFor[yaml.Marshaler](), reflect.TypeFor[yaml.Unmarshaler](),
	reflect.TypeFor[interface{ UnmarshalYAML(func(any) error) error }](),
}

// Inspect once per manager. Nested hooks can make identical file inputs
// produce different typed values, so they disable the input shortcut.
func hasCustomEncoding(t reflect.Type, seen map[reflect.Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	for _, iface := range customEncodingTypes {
		if t.Implements(iface) || reflect.PointerTo(t).Implements(iface) {
			return true
		}
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return hasCustomEncoding(t.Elem(), seen)
	case reflect.Map:
		return hasCustomEncoding(t.Key(), seen) || hasCustomEncoding(t.Elem(), seen)
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if f := t.Field(i); f.IsExported() && hasCustomEncoding(f.Type, seen) {
				return true
			}
		}
	}
	return false
}

// inputFingerprint hashes the ordered file layers (path, rank, codec,
// content), _meta.yaml and the codec generation. It returns the zero value
// — never short-circuit — whenever the pipeline could produce a different
// result from identical files: a non-file layer (provider, generator,
// override), any user code in the pipeline (transforms, secret resolver,
// typed hooks, validators, policies, a Defaulter on *T), or registered
// observers (they are owed StageFinished events). Conservative on purpose.
func (m *Manager[T]) inputFingerprint(staged []stagedLayer, metaSum [32]byte, generation uint64) [32]byte {
	o := &m.opts
	// A concurrent registration may have changed a decoder during assembly.
	// Do not stamp that mixed result with the newer registry generation.
	if generation != codec.Generation() {
		return [32]byte{}
	}
	if len(o.Transforms) > 0 || o.SecretResolver != nil || len(o.TypedHooks) > 0 ||
		len(o.Validators) > 0 || len(o.Policies) > 0 || len(o.Observers) > 0 {
		return [32]byte{}
	}
	if _, ok := any(new(T)).(Defaulter); ok || m.customEncoding {
		return [32]byte{}
	}
	for _, l := range staged {
		if l.sum == ([32]byte{}) {
			return [32]byte{}
		}
	}
	buf := make([]byte, 0, 64+len(staged)*96)
	buf = append(buf, metaSum[:]...)
	buf = binary.LittleEndian.AppendUint64(buf, generation)
	for _, l := range staged {
		buf = append(append(buf, l.src.Path...), 0)
		buf = binary.LittleEndian.AppendUint64(buf, uint64(int64(l.src.Priority)))
		buf = append(append(buf, l.src.Profile...), 0)
		buf = append(append(buf, l.src.Codec...), 0)
		buf = append(buf, l.sum[:]...)
	}
	return sha256.Sum256(buf)
}
