package decoder

import (
	"testing"
)

// FuzzCodecYAML ensures the YAML decoder never panics on arbitrary input
// and either returns a normalised map or a clean error.
func FuzzCodecYAML(f *testing.F) {
	f.Add([]byte("a: 1\nb: 2\n"))
	f.Add([]byte("nested:\n  k: v\n  n: 3\n"))
	f.Add([]byte("list:\n  - 1\n  - 2\n"))
	f.Add([]byte(""))
	f.Add([]byte("not yaml: : :"))

	dec := yamlDecoder{}
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = dec.Decode(raw)
	})
}

// FuzzCodecJSON ensures the JSON decoder never panics on arbitrary input.
func FuzzCodecJSON(f *testing.F) {
	f.Add([]byte(`{"a":1,"b":"x"}`))
	f.Add([]byte(`{"nested":{"k":"v","n":3}}`))
	f.Add([]byte(`{"list":[1,2,3]}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(``))
	f.Add([]byte(`not json`))
	f.Add([]byte(`{{{`))

	dec := jsonDecoder{}
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = dec.Decode(raw)
	})
}
