package fastconf

import (
	"crypto/sha256"
	"encoding"
	"encoding/binary"
	"encoding/json"
	"reflect"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/internal/typeinfo"
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
func hasCustomEncoding(t reflect.Type) bool {
	return typeinfo.Reaches(t, func(t reflect.Type) bool {
		for _, iface := range customEncodingTypes {
			if t.Implements(iface) || reflect.PointerTo(t).Implements(iface) {
				return true
			}
		}
		return false
	})
}

// inputFingerprint hashes ordered file inputs, metadata and codec generation.
// Non-file layers, user code and observers disable this shortcut; see
// docs/design/perf.md for the full eligibility rules.
func (m *Manager[T]) inputFingerprint(staged []stagedLayer, metaSum [32]byte, generation uint64) [32]byte {
	o := &m.opts
	// A concurrent registration may have changed a decoder during assembly.
	// Do not stamp that mixed result with the newer registry generation.
	if generation != codec.Generation() {
		return [32]byte{}
	}
	if len(o.Transforms) > 0 || len(o.SecretResolvers) > 0 || len(o.TypedHooks) > 0 ||
		len(o.Validators) > 0 || len(o.Policies) > 0 || len(o.Observers) > 0 {
		return [32]byte{}
	}
	if _, ok := any(new(T)).(Defaulter); ok || m.customEncoding {
		return [32]byte{}
	}
	for _, l := range staged {
		if l.fileSum == ([32]byte{}) {
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
		buf = append(buf, l.fileSum[:]...)
	}
	return sha256.Sum256(buf)
}
