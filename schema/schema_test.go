package schema_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/providers/source"
	"github.com/fastabc/fastconf/schema"
)

type sampleConfig struct {
	Port    int           `json:"port" default:"8080" desc:"HTTP listen port" fc:"min=1,max=65535"`
	Timeout time.Duration `json:"timeout" default:"5s" desc:"Request timeout"`
	Level   string        `json:"level" fc:"default=info,oneof=debug|info|warn"`
	DB      struct {
		DSN   string   `json:"dsn" fc:"required,secret" desc:"Database DSN"`
		Hosts []string `json:"hosts" default:"a,b"`
	} `json:"db"`
}

func TestJSONSchema_Sample(t *testing.T) {
	b, err := schema.JSONSchema(reflect.TypeFor[sampleConfig]())
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	if root["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatal(root)
	}
	p := root["properties"].(map[string]any)
	want := map[string]any{"type": "integer", "default": float64(8080), "minimum": float64(1), "maximum": float64(65535), "description": "HTTP listen port"}
	if !reflect.DeepEqual(p["port"], want) {
		t.Fatalf("%#v", p["port"])
	}
	timeout := p["timeout"].(map[string]any)
	if timeout["type"] != "string" || timeout["default"] != "5s" || timeout["pattern"] == nil {
		t.Fatal(timeout)
	}
	level := p["level"].(map[string]any)
	if !reflect.DeepEqual(level["enum"], []any{"debug", "info", "warn"}) || level["default"] != "info" {
		t.Fatal(level)
	}
	db := p["db"].(map[string]any)
	if !reflect.DeepEqual(db["required"], []any{"dsn"}) {
		t.Fatal(db)
	}
	fields := db["properties"].(map[string]any)
	if fields["dsn"].(map[string]any)["writeOnly"] != true || !reflect.DeepEqual(fields["hosts"].(map[string]any)["default"], []any{"a", "b"}) {
		t.Fatal(fields)
	}
}

const wantYAML = "# HTTP listen port\nport: 8080\n# Request timeout\ntimeout: 5s\nlevel: info\ndb:\n  # Database DSN (required, secret)\n  dsn: \"\"\n  hosts: [a, b]\n"

func TestExampleYAML_Sample(t *testing.T) {
	b, err := schema.ExampleYAML(reflect.TypeFor[sampleConfig]())
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != wantYAML {
		t.Fatalf("got:\n%s\nwant:\n%s", b, wantYAML)
	}
}
func TestExampleYAML_LoadsBack(t *testing.T) {
	b, err := schema.ExampleYAML(reflect.TypeFor[sampleConfig]())
	if err != nil {
		t.Fatal(err)
	}
	m, err := fastconf.New[sampleConfig](context.Background(), fastconf.WithProvider(source.NewBytes("schema", "application/yaml", b)), fastconf.WithTransform(func(m map[string]any) error { m["db"].(map[string]any)["dsn"] = "pg://example"; return nil }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	if c := m.Get(); c.Port != 8080 || c.Timeout != 5*time.Second || c.Level != "info" || !reflect.DeepEqual(c.DB.Hosts, []string{"a", "b"}) {
		t.Fatal(c)
	}
}
func TestExampleEnv_Sample(t *testing.T) {
	b, err := schema.ExampleEnv(reflect.TypeFor[sampleConfig](), "APP_", "__")
	if err != nil {
		t.Fatal(err)
	}
	want := "# HTTP listen port\nAPP_PORT=8080\n# Request timeout\nAPP_TIMEOUT=5s\nAPP_LEVEL=info\n# Database DSN (required, secret)\nAPP_DB__DSN=\nAPP_DB__HOSTS=a,b\n"
	if string(b) != want {
		t.Fatalf("%s", b)
	}
}
func TestExampleEnv_AmbiguousUnderscore(t *testing.T) {
	type C struct {
		Max int `json:"max_conns" default:"5"`
	}
	b, err := schema.ExampleEnv(reflect.TypeFor[C](), "APP_", "_")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `# max_conns: not addressable with separator "_"; use "__"`) || strings.Contains(string(b), "APP_MAX_CONNS=") {
		t.Fatalf("%s", b)
	}
}
func TestJSONSchema_RecursiveType(t *testing.T) {
	type node struct {
		Children []node `json:"children"`
	}
	b, err := schema.JSONSchema(reflect.TypeFor[node]())
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	_ = json.Unmarshal(b, &root)
	items := root["properties"].(map[string]any)["children"].(map[string]any)["items"]
	if !reflect.DeepEqual(items, map[string]any{"type": "object"}) {
		t.Fatalf("%s", b)
	}
	if _, err := schema.ExampleYAML(reflect.TypeFor[node]()); err != nil {
		t.Fatal(err)
	}
}
