package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSingleFile(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		fail       bool
	}{
		{"valid", "port: 8080", false},
		{"typo", "prot: 8080", true},
		{"invalid", "port: 0", true},
		{"missing", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "app.yaml")
			if tc.data != "" {
				if err := os.WriteFile(path, []byte(tc.data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			err := run(context.Background(), path, &out)
			if (err != nil) != tc.fail {
				t.Fatalf("error=%v", err)
			}
			if !tc.fail && out.String() != "port=8080\n" {
				t.Fatal(out.String())
			}
		})
	}
}
