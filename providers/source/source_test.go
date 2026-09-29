package source_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/providers/source"
)

func TestBytes_OwnsImmutableDocument(t *testing.T) {
	for _, mutate := range []string{"constructor input", "read result"} {
		t.Run(mutate, func(t *testing.T) {
			input := []byte("a: 1")
			b := source.NewBytes("inline", "yaml", input)
			data, _, rev, err := b.Read(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if mutate == "constructor input" {
				input[3] = '2'
			} else {
				data[3] = '2'
			}
			got, ct, nextRev, err := b.Read(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "a: 1" || ct != "yaml" || rev == "" || nextRev != rev {
				t.Fatalf("Read = %q, %q, %q; want original document and revision %q", got, ct, nextRev, rev)
			}
			snap, err := b.Load(context.Background())
			if err != nil || snap.Map["a"] != 1 || snap.Revision != rev {
				t.Fatalf("Load = %#v, %v; want original document and revision %q", snap, err, rev)
			}
		})
	}
}

func TestBytes_DifferentDataDifferentRev(t *testing.T) {
	r1Bytes := source.NewBytes("a", "yaml", []byte("a: 1"))
	r2Bytes := source.NewBytes("b", "yaml", []byte("a: 2"))
	_, _, r1, _ := r1Bytes.Read(context.Background())
	_, _, r2, _ := r2Bytes.Read(context.Background())
	if r1 == r2 {
		t.Errorf("expected different revs, got %q == %q", r1, r2)
	}
}

func TestBytes_RejectsOversizedResponse(t *testing.T) {
	b := source.NewBytes("large", "yaml", []byte("abcdef")).WithMaxBodyBytes(4)
	if _, _, _, err := b.Read(context.Background()); !errors.Is(err, contracts.ErrConfigTooLarge) {
		t.Fatalf("Read error = %v, want ErrConfigTooLarge", err)
	}
}

func TestFile_ReadsAndDerivesContentType(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := source.NewFile(p)
	data, ct, rev, err := f.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "a: 1\n" {
		t.Errorf("data = %q", data)
	}
	if ct != ".yaml" {
		t.Errorf("contentType = %q", ct)
	}
	if rev == "" {
		t.Error("rev empty")
	}
}

func TestFile_MissingFileReturnsEmpty(t *testing.T) {
	f := source.NewFile(filepath.Join(t.TempDir(), "absent.yaml"))
	data, ct, rev, err := f.Read(context.Background())
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(data) != 0 || rev != "" {
		t.Errorf("missing file should yield empty data + rev, got data=%q rev=%q", data, rev)
	}
	if ct != ".yaml" {
		t.Errorf("contentType lost on missing file: %q", ct)
	}
}

func TestFile_RejectsOversizedResponse(t *testing.T) {
	p := filepath.Join(t.TempDir(), "large.yaml")
	if err := os.WriteFile(p, []byte("abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := source.NewFile(p).WithMaxBodyBytes(4).Read(context.Background()); !errors.Is(err, contracts.ErrConfigTooLarge) {
		t.Fatalf("Read error = %v, want ErrConfigTooLarge", err)
	}
}

func TestFile_RevChangesOnModification(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.yaml")
	if err := os.WriteFile(p, []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := source.NewFile(p)
	_, _, r1, _ := f.Read(context.Background())
	// A different size changes the revision even on filesystems with coarse mtimes.
	if err := os.WriteFile(p, []byte("a: 1\nb: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, r2, _ := f.Read(context.Background())
	if r1 == r2 {
		t.Errorf("rev did not change after edit: %q", r1)
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
