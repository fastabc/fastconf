package integration_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
	httpprov "github.com/fastabc/fastconf/providers/http"
	"github.com/fastabc/fastconf/providers/source"
)

type iniConfig struct {
	Name   string `json:"name" yaml:"name"`
	Server struct {
		Port    int           `json:"port" yaml:"port"`
		Enabled bool          `json:"enabled" yaml:"enabled"`
		Timeout time.Duration `json:"timeout" yaml:"timeout"`
		Code    string        `json:"code" yaml:"code"`
	} `json:"server" yaml:"server"`
}

func TestINICodec_LayersAndTypedFields(t *testing.T) {
	for _, format := range []fastconf.Format{fastconf.JSON, fastconf.YAML} {
		t.Run(format.String(), func(t *testing.T) {
			files := fstest.MapFS{
				"conf.d/base/00.INI":          {Data: []byte("name=demo\n[server]\nport=8080\nenabled=true\ntimeout=5s\ncode=00123\n")},
				"conf.d/overlays/prod/00.ini": {Data: []byte("[server]\nport=9090\n")},
			}
			state, err := fastconf.Load[iniConfig](context.Background(),
				fastconf.WithFS(files), fastconf.WithProfile(fastconf.Profile{Names: []string{"prod"}}),
				fastconf.WithDecodeFormat(format), fastconf.WithStrictMerge(true),
			)
			if err != nil {
				t.Fatal(err)
			}
			got := state.Value()
			if got.Name != "demo" || got.Server.Port != 9090 || !got.Server.Enabled || got.Server.Timeout != 5*time.Second || got.Server.Code != "00123" {
				t.Fatalf("incorrect INI conversion: %+v", got)
			}
			if sources := state.Sources(); len(sources) != 2 || sources[0].Codec != "ini" || sources[1].Codec != "ini" {
				t.Fatalf("incorrect sources: %v", sources)
			}
		})
	}
}

func TestINICodec_MixedFormatStrictMerge(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(fmt.Sprint(strict), func(t *testing.T) {
			files := fstest.MapFS{
				"conf.d/base/00.yaml": {Data: []byte("server:\n  port: 80\n")},
				"conf.d/base/10.ini":  {Data: []byte("[server]\nport=8080\n")},
			}
			state, err := fastconf.Load[iniConfig](context.Background(), fastconf.WithFS(files), fastconf.WithStrictMerge(strict))
			if strict {
				if !errors.Is(err, fastconf.ErrMerge) || state != nil {
					t.Fatalf("strict merge = %v, %v; want ErrMerge and no state", state, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if state.Value().Server.Port != 8080 {
				t.Fatalf("INI did not override YAML: %+v", state.Value())
			}
		})
	}
}

func TestINICodec_Providers(t *testing.T) {
	const document = "[server]\nport=8080\n"
	path := filepath.Join(t.TempDir(), "app.ini")
	writeFile(t, path, document)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/ini; charset=utf-8")
		_, _ = fmt.Fprint(w, document)
	}))
	defer server.Close()
	remote, err := httpprov.New("ini", server.URL, nil, httpprov.WithInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []contracts.Provider{
		source.NewBytes("inline", "ini", []byte(document)), source.NewFile(path), remote,
	} {
		t.Run(provider.Name(), func(t *testing.T) {
			state, err := fastconf.Load[iniConfig](context.Background(), fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(provider))
			if err != nil {
				t.Fatal(err)
			}
			if state.Value().Server.Port != 8080 {
				t.Fatalf("incorrect provider value: %+v", state.Value())
			}
		})
	}
}

func TestINICodec_WatchRejectsInvalidAndRecovers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "base", "app.ini")
	writeFile(t, path, "[server]\nport=8080\n")
	mgr, err := fastconf.New[iniConfig](context.Background(), fastconf.WithDir(dir),
		fastconf.WithWatch(fastconf.Watch{Quiet: 20 * time.Millisecond}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	before := mgr.Snapshot()
	writeFile(t, path, "[broken\nport=9090\n")
	if failure := await(t, mgr.Errors()); !errors.Is(failure.Err, fastconf.ErrDecode) {
		t.Fatalf("INI syntax error = %v; want ErrDecode", failure.Err)
	}
	if mgr.Snapshot() != before {
		t.Fatal("invalid INI replaced the live snapshot")
	}
	writeFile(t, path, "[server]\nport=9090\n")
	waitFor(t, func() bool { return mgr.Get().Server.Port == 9090 }, "INI watch recovery")
	if mgr.Snapshot().Generation() <= before.Generation() {
		t.Fatal("recovery did not publish a new generation")
	}
}

// TestINICodec_DottedSectionIsLiteral verifies that a dotted section name like
// [server.tls] is treated as a single literal key, not expanded into nested maps.
func TestINICodec_DottedSectionIsLiteral(t *testing.T) {
	type dottedConfig struct {
		ServerTLS struct {
			Cert string `json:"cert"`
		} `json:"server.tls"`
	}
	files := fstest.MapFS{
		"conf.d/base/00.ini": {Data: []byte("[server.tls]\ncert=/etc/ssl/cert.pem\n")},
	}
	state, err := fastconf.Load[dottedConfig](context.Background(), fastconf.WithFS(files))
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Value().ServerTLS.Cert; got != "/etc/ssl/cert.pem" {
		t.Fatalf("server.tls cert = %q; want /etc/ssl/cert.pem", got)
	}
	// Verify the raw map has a literal "server.tls" key, not nested "server" → "tls".
	m := state.Map()
	if _, ok := m["server.tls"]; !ok {
		t.Fatalf("raw map has no literal \"server.tls\" key: %v", m)
	}
	if nested, ok := m["server"]; ok {
		t.Fatalf("raw map unexpectedly expanded dotted section into nested map: server=%v", nested)
	}
}
