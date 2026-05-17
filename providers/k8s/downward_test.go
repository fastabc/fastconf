package k8s_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/mappath"
	"github.com/fastabc/fastconf/providers/k8s"
)

// Realistic K8s downward sample with recommended labels, a custom
// label, and a multi-line annotation. Verifies that the multi-separator
// split decomposes "app.kubernetes.io/name" coherently and that
// quoted-string escapes are preserved.
func TestDownwardProvider_LoadEndToEnd(t *testing.T) {
	dir := t.TempDir()
	labels := `# kubelet header
app.kubernetes.io/name="web"
app.kubernetes.io/component="frontend"
app.kubernetes.io/version="1.2.3"
custom="hello"
`
	annotations := `kubectl.kubernetes.io/last-applied-configuration="{\"a\":1}"
description="line1\nline2"
`
	labelsPath := filepath.Join(dir, "labels")
	annPath := filepath.Join(dir, "annotations")
	mustWrite(t, labelsPath, labels)
	mustWrite(t, annPath, annotations)

	p := k8s.New(k8s.Options{
		LabelsPath:      labelsPath,
		AnnotationsPath: annPath,
	})
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if v, _ := mappath.GetDotted(got, "labels.app.kubernetes.io.name"); v != "web" {
		t.Errorf("labels.app.kubernetes.io.name = %v want \"web\"", v)
	}
	if v, _ := mappath.GetDotted(got, "labels.app.kubernetes.io.component"); v != "frontend" {
		t.Errorf("labels.app.kubernetes.io.component = %v", v)
	}
	if v, _ := mappath.GetDotted(got, "labels.custom"); v != "hello" {
		t.Errorf("labels.custom = %v", v)
	}
	if v, _ := mappath.GetDotted(got, "annotations.description"); v != "line1\nline2" {
		t.Errorf("description escape lost: %v", v)
	}
	if v, _ := mappath.GetDotted(got, "annotations.kubectl.kubernetes.io.last-applied-configuration"); v != `{"a":1}` {
		t.Errorf("last-applied-configuration: got %v", v)
	}
}

// Missing files are silently skipped when MustExist=false (default).
func TestDownwardProvider_MissingFilesSilent(t *testing.T) {
	p := k8s.New(k8s.Options{
		LabelsPath:      "/nonexistent/labels",
		AnnotationsPath: "/nonexistent/annotations",
	})
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("Load with missing files: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

// MustExist=true surfaces a clear read error when a configured file
// is absent — important for production pods where the volume mount
// is mandatory.
func TestDownwardProvider_MustExistErrors(t *testing.T) {
	p := k8s.New(k8s.Options{
		LabelsPath: "/definitely/missing",
		MustExist:  true,
	})
	if _, err := p.Load(context.Background()); err == nil {
		t.Fatal("expected error for missing required file")
	}
}

// NewDefault picks up DefaultLabelsPath / DefaultAnnotationsPath.
func TestDownwardProvider_NewDefaultPaths(t *testing.T) {
	p := k8s.NewDefault()
	if p.Name() != "k8s-downward" {
		t.Errorf("Name() = %q", p.Name())
	}
	if p.Priority() != contracts.PriorityK8s {
		t.Errorf("Priority() = %d want PriorityK8s %d", p.Priority(), contracts.PriorityK8s)
	}
}

// At() grafts the whole tree under a dotted root.
func TestDownwardProvider_AtNamespaces(t *testing.T) {
	dir := t.TempDir()
	labelsPath := filepath.Join(dir, "labels")
	mustWrite(t, labelsPath, `app="web"`+"\n")

	p := k8s.New(k8s.Options{
		LabelsPath: labelsPath,
		At:         "k8s.metadata",
	})
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := mappath.GetDotted(got, "k8s.metadata.labels.app"); v != "web" {
		t.Errorf("expected k8s.metadata.labels.app=\"web\", got %#v", got)
	}
}

// Corrupted input (missing '=', missing quotes) fails the reload
// rather than silently dropping lines.
func TestDownwardProvider_CorruptInputErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing equals", "broken-line-no-equals\n"},
		{"unquoted value", "key=unquoted\n"},
		{"unterminated quote", `key="missing-close-quote` + "\n"},
		{"empty key", `="oops"` + "\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "labels")
			mustWrite(t, path, c.body)
			p := k8s.New(k8s.Options{LabelsPath: path})
			if _, err := p.Load(context.Background()); err == nil {
				t.Fatal("expected parse error")
			}
		})
	}
}

// Custom Separators override the default {"/", "."} — useful when
// labels use other delimiters.
func TestDownwardProvider_CustomSeparators(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "labels")
	mustWrite(t, path, `foo:bar:baz="v"`+"\n")
	p := k8s.New(k8s.Options{
		LabelsPath: path,
		Separators: []string{":"},
	})
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := mappath.Get(got, "labels", "foo", "bar", "baz"); v != "v" {
		t.Errorf("got %#v", got)
	}
}

// Watch returns (nil, nil) — see package doc; operators trigger
// Reload externally.
func TestDownwardProvider_WatchReturnsNil(t *testing.T) {
	p := k8s.NewDefault()
	ch, err := p.Watch(context.Background())
	if err != nil || ch != nil {
		t.Fatalf("Watch should be (nil, nil); got ch=%v err=%v", ch, err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
