package codec

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
		got, err := dec.Decode(raw)
		if err == nil {
			if got == nil {
				t.Fatal("successful Decode returned nil map")
			}
			assertNormalized(t, got)
		}
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
		got, err := dec.Decode(raw)
		if err == nil {
			if got == nil {
				t.Fatal("successful Decode returned nil map")
			}
			assertNormalized(t, got)
		}
	})
}

// FuzzCodecTOML ensures the TOML decoder never panics on arbitrary input
// and either returns a normalised map or a clean error.
func FuzzCodecTOML(f *testing.F) {
	f.Add([]byte(`name = "x"`))
	f.Add([]byte(`[s]` + "\n" + `k = 1`))
	f.Add([]byte(`[[items]]` + "\n" + `id = 1`))
	f.Add([]byte(``))
	f.Add([]byte(`= not valid`))

	dec := tomlDecoder{}
	f.Fuzz(func(t *testing.T, raw []byte) {
		got, err := dec.Decode(raw)
		if err == nil {
			if got == nil {
				t.Fatal("successful Decode returned nil map")
			}
			assertNormalized(t, got)
		}
	})
}

func assertNormalized(t *testing.T, value any) {
	t.Helper()
	switch v := value.(type) {
	case map[any]any, []map[string]any:
		t.Fatalf("unnormalized container %T", v)
	case map[string]any:
		for _, child := range v {
			assertNormalized(t, child)
		}
	case []any:
		for _, child := range v {
			assertNormalized(t, child)
		}
	}
}

func FuzzCodecINI(f *testing.F) {
	for _, seed := range []string{
		"", "name=demo\n[server]\nport=8080\n", "[server]\nport=80\n[DEFAULT]\nserver=x",
		"\xef\xbb\xbfkey=value\r\n", "key=\"x # y\"\n", "[unclosed", "key=\"unclosed\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		got, err := (iniDecoder{}).Decode(raw)
		if err != nil {
			return
		}
		if got == nil {
			t.Fatal("successful Decode returned nil map")
		}
		assertNormalized(t, got)
	})
}
