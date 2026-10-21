package source_test

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/providers/source"
)

func TestFSLoadAndReload(t *testing.T) {
	files := fstest.MapFS{"config/app.yaml": &fstest.MapFile{Data: []byte("server:\n  host: localhost\n")}}
	p := source.NewFS(files, "config/app.yaml").WithPriority(23)
	first, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first.Map["server"].(map[string]any)["host"] = "mutated"
	second, err := p.Load(context.Background())
	if err != nil || second.Revision != first.Revision || second.Map["server"].(map[string]any)["host"] != "localhost" {
		t.Fatalf("Load = %+v, %v", second, err)
	}
	files["config/app.yaml"].Data = []byte("server:\n  host: production\n")
	third, err := p.Load(context.Background())
	if err != nil || third.Revision == first.Revision || third.Map["server"].(map[string]any)["host"] != "production" {
		t.Fatalf("reload = %+v, %v", third, err)
	}
	if info := p.Describe(); info.Priority != 23 || len(info.WatchPaths) != 0 {
		t.Fatalf("Describe = %+v", info)
	}
	if _, ok := any(p).(contracts.Watcher); ok {
		t.Fatal("FS must not implement a native watcher")
	}
}

func TestFSErrorsAndLimits(t *testing.T) {
	files := fstest.MapFS{
		"app.json": &fstest.MapFile{Data: []byte(`{"a":1}`)},
		"bad.json": &fstest.MapFile{Data: []byte(`invalid`)},
		"app.xyz":  &fstest.MapFile{Data: []byte(`a`)},
		"empty":    &fstest.MapFile{},
	}
	for _, tc := range []struct {
		name string
		p    *source.FS
		want error
	}{
		{"missing", source.NewFS(files, "missing.yaml"), fs.ErrNotExist},
		{"absolute", source.NewFS(files, "/app.json"), fs.ErrInvalid},
		{"parent", source.NewFS(files, "../app.json"), fs.ErrInvalid},
		{"codec", source.NewFS(files, "app.xyz"), codec.ErrUnknownCodec},
		{"limit", source.NewFS(files, "app.json").WithMaxBodyBytes(6), contracts.ErrTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.p.Load(context.Background()); !errors.Is(err, tc.want) {
				t.Fatalf("Load = %v, want %v", err, tc.want)
			}
		})
	}
	for _, p := range []*source.FS{source.NewFS(nil, "app.json"), source.NewFS(files, "bad.json")} {
		if _, err := p.Load(context.Background()); err == nil {
			t.Fatal("expected load error")
		}
	}
	if snap, err := source.NewFS(files, "empty").Load(context.Background()); err != nil || len(snap.Map) != 0 || snap.Revision == "" {
		t.Fatalf("empty = %+v, %v", snap, err)
	}
	for _, limit := range []int64{0, -1, 7} {
		if _, err := source.NewFS(files, "app.json").WithMaxBodyBytes(limit).Load(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.NewFS(files, "app.json").Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Load = %v", err)
	}
}

func TestFSManagerIntegration(t *testing.T) {
	type config struct {
		Port int `json:"port"`
	}
	files := fstest.MapFS{"app.json": &fstest.MapFile{Data: []byte(`{"port":8080}`)}}
	snap, err := fastconf.Load[config](context.Background(), fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(source.NewFS(files, "app.json")))
	if err != nil || snap.Value().Port != 8080 {
		t.Fatalf("Load = %+v, %v", snap, err)
	}
}
