package source_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/providers/source"
)

func TestSource_LoadRejectsOversizedDocuments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.yaml")
	if err := os.WriteFile(path, []byte("a: 12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []contracts.Provider{
		source.NewBytes("large", "yaml", []byte("a: 12345")).WithMaxBodyBytes(4),
		source.NewFile(path).WithMaxBodyBytes(4),
	} {
		t.Run(p.Name(), func(t *testing.T) {
			_, err := p.Load(context.Background())
			if !errors.Is(err, contracts.ErrTooLarge) || !errors.Is(err, fastconf.ErrTooLarge) {
				t.Fatalf("Load error = %v; want both public ErrTooLarge sentinels", err)
			}
		})
	}
}

func TestSource_DecodesByContentType(t *testing.T) {
	snap, err := source.NewBytes("inline", ".yaml", []byte("a: 1\nb: two\n")).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Map["a"] != 1 || snap.Map["b"] != "two" {
		t.Errorf("decoded %v", snap.Map)
	}
	if snap.Revision == "" {
		t.Error("bytes source must carry a content revision")
	}
}

func TestSource_UnknownContentTypeNamesSource(t *testing.T) {
	_, err := source.NewBytes("inline", "application/no-such-format", []byte("garbage")).Load(context.Background())
	if !errors.Is(err, codec.ErrUnknownCodec) {
		t.Fatalf("want ErrUnknownCodec, got %v", err)
	}
	if !strings.Contains(err.Error(), "inline") {
		t.Errorf("error missing source name: %v", err)
	}
}

func TestSource_EmptyPayloadIsEmptyLayer(t *testing.T) {
	snap, err := source.NewBytes("empty", "", nil).Load(context.Background())
	if err != nil || len(snap.Map) != 0 {
		t.Fatalf("Load = %v, %v; want empty map", snap.Map, err)
	}
}

func TestSource_DescribeCarriesPriorityAndWatchPaths(t *testing.T) {
	b := source.NewBytes("seed", ".yaml", []byte("a: 1")).WithPriority(contracts.PriorityKV)
	if got := contracts.Describe(b).Priority; got != contracts.PriorityKV {
		t.Errorf("bytes priority = %d", got)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := source.NewFile(path)
	info := contracts.Describe(f)
	if len(info.WatchPaths) != 1 || info.WatchPaths[0] != path {
		t.Fatalf("file WatchPaths = %v", info.WatchPaths)
	}
	snap, err := f.Load(context.Background())
	if err != nil || snap.Revision == "" || snap.Map["a"] != 1 {
		t.Fatalf("file Load = %#v, %v", snap, err)
	}
}
