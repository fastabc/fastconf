package integration_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
)

type tagDefaultsConfig struct {
	Port    int           `json:"port" default:"8080"`
	Timeout time.Duration `json:"timeout" default:"5s"`
	Enabled bool          `json:"enabled" default:"true"`
	Hosts   []string      `json:"hosts" default:"a,b"`
}

func TestTagDefaults_AbsentKeysOnly(t *testing.T) {
	st, err := fastconf.Load[tagDefaultsConfig](context.Background(), fastconf.WithFS(fstest.MapFS{"conf.d/base/app.yaml": {Data: []byte("enabled: false\n")}}), fastconf.WithProvenance(fastconf.ProvenanceFull))
	if err != nil {
		t.Fatal(err)
	}
	if want := (tagDefaultsConfig{8080, 5 * time.Second, false, []string{"a", "b"}}); !reflect.DeepEqual(*st.Value(), want) {
		t.Fatalf("%+v", st.Value())
	}
	o := st.Explain("port")
	if len(o) == 0 || o[len(o)-1].Source.Kind != fastconf.LayerDefault || o[len(o)-1].Source.Path != "default://tag" {
		t.Fatalf("%+v", o)
	}
	for _, o := range st.Explain("enabled") {
		if o.Source.Kind == fastconf.LayerDefault {
			t.Fatal("explicit value defaulted")
		}
	}
}
func TestTagDefaults_ExplicitZeroAndNullPreserved(t *testing.T) {
	st, err := fastconf.Load[tagDefaultsConfig](context.Background(), fastconf.WithFS(fstest.MapFS{"conf.d/base/app.yaml": {Data: []byte("port: 0\nhosts: null\nenabled: false\n")}}))
	if err != nil {
		t.Fatal(err)
	}
	if c := st.Value(); c.Port != 0 || c.Hosts != nil || c.Enabled || c.Timeout != 5*time.Second {
		t.Fatalf("%+v", c)
	}
}
func TestTagDefaults_NestedAndPointer(t *testing.T) {
	type DB struct {
		Pool int `json:"pool" default:"10"`
	}
	type TLS struct {
		Verify bool `json:"verify" default:"true"`
	}
	type C struct {
		DB  DB   `json:"db"`
		TLS *TLS `json:"tls"`
	}
	for _, raw := range []string{"{}", "tls: {}", "tls: null", "db: null"} {
		st, err := fastconf.Load[C](context.Background(), fastconf.WithFS(fstest.MapFS{"conf.d/base/app.yaml": {Data: []byte(raw)}}))
		if err != nil {
			t.Fatal(err)
		}
		c := st.Value()
		if raw != "db: null" && c.DB.Pool != 10 {
			t.Fatalf("%+v", c)
		}
		if raw == "tls: {}" {
			if c.TLS == nil || !c.TLS.Verify {
				t.Fatalf("%+v", c)
			}
		} else if c.TLS != nil {
			t.Fatal("allocated absent/null pointer")
		}
	}
}
func TestTagDefaults_InvalidDeclarations(t *testing.T) {
	type Conflict struct {
		X int `default:"1" fc:"default=2"`
	}
	type Invalid struct {
		X int `default:"abc"`
	}
	type Collection struct {
		Items []struct {
			X int `default:"1"`
		}
	}
	type Object struct {
		X struct{ V int } `default:"x"`
	}
	for _, err := range []error{newTagDefaults[Conflict](), newTagDefaults[Invalid](), newTagDefaults[Collection](), newTagDefaults[Object]()} {
		if !errors.Is(err, fastconf.ErrFastConf) || !strings.Contains(err.Error(), "default tag") {
			t.Fatalf("%v", err)
		}
	}
}
func newTagDefaults[T any]() error {
	m, err := fastconf.New[T](context.Background())
	if m != nil {
		_ = m.Close()
	}
	return err
}
func TestTagDefaults_YAMLBridge(t *testing.T) {
	type C struct {
		MaxConns int `yaml:"max_conns" default:"5"`
	}
	st, err := fastconf.Load[C](context.Background(), fastconf.WithDecodeFormat(fastconf.YAML), fastconf.WithFS(fstest.MapFS{"conf.d/base/app.yaml": {Data: []byte("{}")}}))
	if err != nil || st.Value().MaxConns != 5 {
		t.Fatalf("%v %v", st, err)
	}
}
func TestTagDefaults_ReloadKeepsExplicitValue(t *testing.T) {
	m, err := fastconf.New[tagDefaultsConfig](context.Background(), fastconf.WithFS(fstest.MapFS{"conf.d/base/app.yaml": {Data: []byte("{}")}}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	if err := m.Reload(context.Background(), fastconf.WithOverride(map[string]any{"port": 9090})); err != nil {
		t.Fatal(err)
	}
	if m.Get().Port != 9090 {
		t.Fatal(m.Get())
	}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Get().Port != 8080 {
		t.Fatal(m.Get())
	}
}

func TestTagDefaults_CaseInsensitiveJSONPresence(t *testing.T) {
	type C struct {
		Port int `json:"port" default:"8080"`
	}
	st, err := fastconf.Load[C](context.Background(), fastconf.WithProvider(&snapshotProvider{name: "p", data: map[string]any{"PORT": 9090}}))
	if err != nil {
		t.Fatal(err)
	}
	if st.Value().Port != 9090 {
		t.Fatal(st.Value())
	}
}
