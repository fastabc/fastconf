package fastconf

import (
	"context"
	"testing"
	"testing/fstest"
)

type runtimeConfig struct {
	A int    `json:"a" yaml:"a"`
	B string `json:"b" yaml:"b"`
	C struct {
		D bool   `json:"d" yaml:"d"`
		E string `json:"e" yaml:"e"`
	} `json:"c" yaml:"c"`
}

func newRuntimeManager(b testing.TB) *Manager[runtimeConfig] {
	b.Helper()
	mgr, err := New[runtimeConfig](context.Background(),
		WithFS(fstest.MapFS{
			"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("a: 1\nb: hello\nc:\n  d: true\n  e: world\n")},
		}),
	)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = mgr.Close() })
	return mgr
}
