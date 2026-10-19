package integration_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/contracts"
)

type loadFileConfig struct {
	Port int    `json:"port" fc:"default=8080"`
	Name string `json:"name"`
}

func (c *loadFileConfig) Defaults() {
	if c.Name == "" {
		c.Name = "computed"
	}
}

func TestLoadFile_Errors(t *testing.T) {
	for _, tc := range []struct {
		name, file, body string
		want             []error
		detail           string
	}{
		{"unknown field", "cfg.yaml", "port: 1\nprot: 2\n", []error{fastconf.ErrDecode}, "prot"},
		{"unknown extension", "cfg.txt", "port: 1\n", []error{fastconf.ErrDecode, codec.ErrUnknownCodec}, ""},
		{"too large", "cfg.yaml", strings.Repeat(" ", int(contracts.DefaultMaxBodyBytes)+1), []error{fastconf.ErrTooLarge}, ""},
		{"missing", "missing.yaml", "", []error{fastconf.ErrProvider, fs.ErrNotExist}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.file)
			if tc.name != "missing" {
				if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			value, err := fastconf.LoadFile[loadFileConfig](path)
			if value != nil {
				t.Fatalf("value = %+v on error", value)
			}
			for _, want := range tc.want {
				if !errors.Is(err, want) {
					t.Fatalf("err = %v, want %v", err, want)
				}
			}
			if tc.detail != "" && !strings.Contains(err.Error(), tc.detail) {
				t.Fatalf("err = %v, want detail %q", err, tc.detail)
			}
		})
	}
}

func TestLoadFile_DefaultsAndDefaulter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	value, err := fastconf.LoadFile[loadFileConfig](path)
	if err != nil {
		t.Fatal(err)
	}
	if value.Port != 8080 || value.Name != "computed" {
		t.Fatalf("defaults = %+v", value)
	}
	if err := os.WriteFile(path, []byte("port: 1234\nname: explicit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	value, err = fastconf.LoadFile[loadFileConfig](path)
	if err != nil || value.Port != 1234 || value.Name != "explicit" {
		t.Fatalf("explicit values = %+v, %v", value, err)
	}
}
