package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fastabc/fastconf/contracts"
)

func TestBytes_OwnsImmutableDocument(t *testing.T) {
	for _, mutate := range []string{"constructor input", "read result"} {
		t.Run(mutate, func(t *testing.T) {
			input := []byte("a: 1")
			b := NewBytes("inline", "yaml", input)
			data, _, rev, err := b.read(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if mutate == "constructor input" {
				input[3] = '2'
			} else {
				data[3] = '2'
			}
			got, ct, nextRev, err := b.read(context.Background())
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
	r1Bytes := NewBytes("a", "yaml", []byte("a: 1"))
	r2Bytes := NewBytes("b", "yaml", []byte("a: 2"))
	_, _, r1, _ := r1Bytes.read(context.Background())
	_, _, r2, _ := r2Bytes.read(context.Background())
	if r1 == r2 {
		t.Errorf("expected different revs, got %q == %q", r1, r2)
	}
}

func TestBytes_RejectsOversizedResponse(t *testing.T) {
	b := NewBytes("large", "yaml", []byte("abcdef")).WithMaxBodyBytes(4)
	if _, _, _, err := b.read(context.Background()); !errors.Is(err, contracts.ErrTooLarge) {
		t.Fatalf("Read error = %v, want ErrTooLarge", err)
	}
}

func TestFile_ReadsAndDerivesContentType(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := NewFile(p)
	data, ct, rev, err := f.read(context.Background())
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
	f := NewFile(filepath.Join(t.TempDir(), "absent.yaml"))
	data, ct, rev, err := f.read(context.Background())
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
	if _, _, _, err := NewFile(p).WithMaxBodyBytes(4).read(context.Background()); !errors.Is(err, contracts.ErrTooLarge) {
		t.Fatalf("Read error = %v, want ErrTooLarge", err)
	}
}

func TestFile_RevChangesOnModification(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.yaml")
	if err := os.WriteFile(p, []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := NewFile(p)
	_, _, r1, _ := f.read(context.Background())
	// A different size changes the revision even on filesystems with coarse mtimes.
	if err := os.WriteFile(p, []byte("a: 1\nb: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, r2, _ := f.read(context.Background())
	if r1 == r2 {
		t.Errorf("rev did not change after edit: %q", r1)
	}
}
