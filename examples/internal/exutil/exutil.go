package exutil

import (
	"os"
	"path/filepath"
)

func TempDir(pattern string) (string, func()) {
	dir, err := os.MkdirTemp(".", pattern)
	if err != nil {
		panic(err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		panic(err)
	}
	return abs, func() { _ = os.RemoveAll(abs) }
}

func WriteFile(path, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		panic(err)
	}
}

func SetEnv(key, value string) func() {
	old, ok := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		panic(err)
	}
	return func() {
		if !ok {
			_ = os.Unsetenv(key)
			return
		}
		_ = os.Setenv(key, old)
	}
}
