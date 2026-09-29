package watcher

import (
	"github.com/fsnotify/fsnotify"
	"os"
	"path/filepath"
	"testing"
)

func TestK8sSwapCommit(t *testing.T) {
	for _, tc := range []struct {
		name string
		op   fsnotify.Op
		want bool
	}{
		{"..data", fsnotify.Create, true}, {"..data", fsnotify.Rename, true},
		{"..data_tmp_123", fsnotify.Create, true}, {"..data", fsnotify.Chmod, false},
		{"..data", fsnotify.Remove, false}, {"config.yaml", fsnotify.Create, false},
	} {
		if got := isK8sSwapCommit(tc.op, tc.name); got != tc.want {
			t.Errorf("%s/%v = %v", tc.name, tc.op, got)
		}
	}
}

func TestAddPathWatchesParentOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("x: 1"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := New([]string{path, path, filepath.Join(dir, "missing.yaml")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	watched := w.fsw.WatchList()
	if len(watched) != 1 || watched[0] != dir {
		t.Fatalf("watched = %v", watched)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTrackReRegistersRecreatedDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "base")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := New([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	w.track(fsnotify.Event{Name: dir, Op: fsnotify.Remove})
	if got := w.fsw.WatchList(); len(got) != 1 || got[0] != root {
		t.Fatalf("after remove, watched = %v, want ancestor %s", got, root)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	w.track(fsnotify.Event{Name: dir, Op: fsnotify.Create})
	if _, ok := w.dirs[dir]; !ok {
		t.Fatalf("recreated dir not re-registered: %v", w.fsw.WatchList())
	}
}
