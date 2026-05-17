package source_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/fastabc/fastconf/pkg/source"
)

func TestBytes_RevIsStableAcrossReads(t *testing.T) {
	b := source.NewBytes("inline", "yaml", []byte("a: 1"))
	_, _, r1, err := b.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, _, r2, _ := b.Read(context.Background())
	if r1 == "" || r1 != r2 {
		t.Errorf("rev unstable: %q vs %q", r1, r2)
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

func TestFile_RevChangesOnModification(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.yaml")
	if err := os.WriteFile(p, []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := source.NewFile(p)
	_, _, r1, _ := f.Read(context.Background())
	// Force a different mtime + size.
	if err := os.WriteFile(p, []byte("a: 1\nb: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Ensure mtime resolution registers a change.
	now := os.Getenv("UNUSED") // no-op; just want to flush thoughts
	_ = now
	_, _, r2, _ := f.Read(context.Background())
	if r1 == r2 {
		t.Errorf("rev did not change after edit: %q", r1)
	}
}

func TestHTTP_ETagShortCircuit(t *testing.T) {
	var hits atomic.Int64
	body := []byte("a: 1\n")
	etag := `"v1"`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	h := source.NewHTTP(srv.URL)
	data1, ct1, rev1, err := h.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(data1) != "a: 1\n" || ct1 != "application/yaml" || rev1 != etag {
		t.Errorf("first read: data=%q ct=%q rev=%q", data1, ct1, rev1)
	}
	data2, ct2, rev2, err := h.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(data2) != "a: 1\n" || rev2 != etag || ct2 != "application/yaml" {
		t.Errorf("304 path should replay cached body+ETag: data=%q ct=%q rev=%q", data2, ct2, rev2)
	}
	if hits.Load() != 2 {
		t.Errorf("expected 2 server hits, got %d", hits.Load())
	}
}
