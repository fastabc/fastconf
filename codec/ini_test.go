package codec

import (
	"reflect"
	"strings"
	"testing"
)

func TestINIDecoder(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        map[string]any
	}{
		{"empty", "", map[string]any{}},
		{"comments", " \n; comment\n# comment\n", map[string]any{}},
		{"root and section", "name = demo\n[server]\nport = 8080\nenabled = true\ntimeout = 5s\ncode = 00123\nempty =\n",
			map[string]any{"name": "demo", "server": map[string]any{"port": "8080", "enabled": "true", "timeout": "5s", "code": "00123", "empty": ""}}},
		{"default section", "name = first\n[DEFAULT]\nname = last\n[default]\nname = separate\n",
			map[string]any{"name": "last", "default": map[string]any{"name": "separate"}}},
		{"repeated keys and sections", "[server]\nport=80\nport=81\nhost=localhost\n[server]\nport=82\n",
			map[string]any{"server": map[string]any{"port": "82", "host": "localhost"}}},
		{"literal names and values", "root=base\n[Server]\nPort=80\nport=81\n[Server.child]\nhost.name=x\nref=%(root)s\nenv=${HOME}\nlist=a,b\n",
			map[string]any{"root": "base", "Server": map[string]any{"Port": "80", "port": "81"}, "Server.child": map[string]any{"host.name": "x", "ref": "%(root)s", "env": "${HOME}", "list": "a,b"}}},
		{"quotes and comments", "a=\"x # y ; z\"\nb='  spaced  '\nc=word ; note\nd=https://host/path#fragment\ne=abc;def\n",
			map[string]any{"a": "x # y ; z", "b": "  spaced  ", "c": "word", "d": "https://host/path#fragment", "e": "abc;def"}},
		{"BOM and CRLF", "\xef\xbb\xbfkey=value\r\n[server]\r\nport: 80\r\n",
			map[string]any{"key": "value", "server": map[string]any{"port": "80"}}},
		{"backslash is literal", "path=C:\\config\\\nnext=value\n", map[string]any{"path": "C:\\config\\", "next": "value"}},
		{"empty section", "[server]\n", map[string]any{"server": map[string]any{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := (iniDecoder{}).Decode([]byte(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Decode = %#v; want %#v", got, tc.want)
			}
		})
	}
}

func TestINIDecoder_Invalid(t *testing.T) {
	for _, input := range []string{
		"[unclosed\nkey=value", "=value", "key without delimiter", "[]\nx=y",
		"server=scalar\n[server]\nport=80", "[server]\nport=80\n[DEFAULT]\nserver=scalar",
		"key=\"\"\"unclosed\n", "key=\"unclosed\n",
	} {
		got, err := (iniDecoder{}).Decode([]byte(input))
		if err == nil || !strings.HasPrefix(err.Error(), "ini:") || got != nil {
			t.Fatalf("Decode(%q) = %v, %v; want nil map and INI error", input, got, err)
		}
	}
}

func TestINIDecoder_ConcurrentIndependentResults(t *testing.T) {
	decoder := iniDecoder{}
	for range 8 {
		t.Run("decode", func(t *testing.T) {
			t.Parallel()
			for range 10 {
				got, err := decoder.Decode([]byte("[server]\nport=80\n"))
				if err != nil {
					t.Fatal(err)
				}
				server, ok := got["server"].(map[string]any)
				if !ok {
					t.Fatalf("server is %T, want map[string]any", got["server"])
				}
				if server["port"] != "80" {
					t.Fatalf("shared decode result: %v", got)
				}
				server["port"] = "changed"
			}
		})
	}
}

func TestINIDecoder_Registry(t *testing.T) {
	if _, err := For("INI"); err != nil {
		t.Fatal(err)
	}
	if got := LookupExt(".INI"); got != "ini" {
		t.Fatalf("extension = %q", got)
	}
	for _, hint := range []string{"ini", ".ini", "application/ini", "text/ini; charset=utf-8", "application/x-ini"} {
		decoder, ok := ByContentType(hint)
		if !ok {
			t.Fatalf("missing decoder for %q", hint)
		}
		got, err := decoder.Decode([]byte("key=value"))
		if err != nil || got["key"] != "value" {
			t.Fatalf("%s: %v, %v", hint, got, err)
		}
	}
	for _, ext := range []string{".cfg", ".conf"} {
		if got := LookupExt(ext); got != "" {
			t.Fatalf("ambiguous extension %s registered as %s", ext, got)
		}
	}
}
