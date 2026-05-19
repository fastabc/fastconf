package fastconf_test

import (
	"context"
	"sort"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
)

type cfg122 struct {
	Server struct {
		Addr    string `json:"addr"`
		Timeout int    `json:"timeout"`
	} `json:"server"`
	Database struct {
		DSN  string `json:"dsn"`
		Pool int    `json:"pool"`
	} `json:"database"`
}

func newMgr122(t *testing.T) *fastconf.Manager[cfg122] {
	t.Helper()
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(`
server:
  addr: ":8080"
  timeout: 30
database:
  dsn: "postgres://prod"
  pool: 10
`)},
	}
	mgr, err := fastconf.New[cfg122](context.Background(), fastconf.WithFS(fs))
	if err != nil {
		t.Fatal(err)
	}
	return mgr
}

func TestIntrospect_KeysSorted(t *testing.T) {
	mgr := newMgr122(t)
	defer mgr.Close()
	got := mgr.Snapshot().Introspect().Keys()
	want := []string{"database.dsn", "database.pool", "server.addr", "server.timeout"}
	if !sort.StringsAreSorted(got) {
		t.Fatalf("not sorted: %v", got)
	}
	if len(got) != len(want) {
		t.Fatalf("len got %d want %d (%v)", len(got), len(want), got)
	}
	for i, k := range want {
		if got[i] != k {
			t.Fatalf("at %d: got %s want %s", i, got[i], k)
		}
	}
}

func TestIntrospect_SettingsFreshCopy(t *testing.T) {
	mgr := newMgr122(t)
	defer mgr.Close()
	ins := mgr.Snapshot().Introspect()
	a := ins.Settings()
	a["server.addr"] = "tampered"
	b := ins.Settings()
	if b["server.addr"] == "tampered" {
		t.Fatalf("Settings should return a fresh copy: got %v", b["server.addr"])
	}
}

func TestIntrospect_AtPrefixStripped(t *testing.T) {
	mgr := newMgr122(t)
	defer mgr.Close()
	got := mgr.Snapshot().Introspect().At("server")
	if got["addr"] != ":8080" {
		t.Fatalf("addr got %v", got["addr"])
	}
	if _, ok := got["timeout"]; !ok {
		t.Fatalf("timeout missing: %v", got)
	}
	if _, ok := got["server.addr"]; ok {
		t.Fatalf("prefix should be stripped: %v", got)
	}
}

func TestIntrospect_AtEmptyEqualsSettings(t *testing.T) {
	mgr := newMgr122(t)
	defer mgr.Close()
	ins := mgr.Snapshot().Introspect()
	sub := ins.At("")
	all := ins.Settings()
	if len(sub) != len(all) {
		t.Fatalf("len differ: sub=%d all=%d", len(sub), len(all))
	}
}

func TestExtract_ReturnsLivePointer(t *testing.T) {
	mgr := newMgr122(t)
	defer mgr.Close()
	dbView := fastconf.Extract(mgr.Snapshot(), func(c *cfg122) *struct {
		DSN  string `json:"dsn"`
		Pool int    `json:"pool"`
	} {
		return &c.Database
	})
	if dbView == nil || dbView.DSN != "postgres://prod" {
		t.Fatalf("Extract failed: %#v", dbView)
	}
}
