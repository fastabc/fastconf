package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/cmd/internal/cli"
)

func TestRunDiff_EmptyFromIgnoresProfileEnv(t *testing.T) {
	for _, profileEnv := range []string{"prod", ""} {
		t.Run("APP_PROFILE="+profileEnv, func(t *testing.T) {
			t.Setenv("APP_PROFILE", profileEnv)
			dir := t.TempDir()
			for path, body := range map[string]string{
				"_meta.yaml":              "spec: {defaultProfile: prod}\n",
				"base/a.yaml":             "level: base\n",
				"overlays/prod/a.yaml":    "level: prod\n",
				"overlays/neg/_meta.yaml": "match: \"!dev\"\n",
				"overlays/neg/b.yaml":     "neg: true\n",
			} {
				file := filepath.Join(dir, path)
				if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			out, err := os.CreateTemp(t.TempDir(), "stdout")
			if err != nil {
				t.Fatal(err)
			}
			old := os.Stdout
			os.Stdout = out
			defer func() { os.Stdout = old; _ = out.Close() }()
			err = runDiff([]string{"-dir", dir, "-to", "prod"})
			os.Stdout = old
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(out.Name())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "~ level : base -> prod") || !strings.Contains(string(data), "+ neg = true") {
				t.Fatalf("diff must start from base only: %s", data)
			}
		})
	}
}

func TestCommands(t *testing.T) {
	dir := t.TempDir()
	for path, body := range map[string]string{"base/a.yaml": "name: base\n", "overlays/prod/a.yaml": "name: prod\n"} {
		file := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		run  func([]string) error
		args []string
		want string
	}{
		{"dump", runDump, []string{"-pretty=false"}, "{\"name\":\"base\"}\n"},
		{"yaml", runDump, []string{"-format=yaml"}, "name: base\n"},
		{"validate", runValidate, nil, "OK\n"},
		{"diff", runDiff, []string{"-to=prod"}, "~ name : base -> prod\n"},
		{"explain", runExplain, []string{"name"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := os.CreateTemp(t.TempDir(), "stdout")
			if err != nil {
				t.Fatal(err)
			}
			old := os.Stdout
			os.Stdout = out
			defer func() { os.Stdout = old; _ = out.Close() }()
			err = tc.run(append([]string{"-dir", dir}, tc.args...))
			os.Stdout = old
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(out.Name())
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "explain" {
				if !strings.Contains(string(data), `"value": "base"`) || !strings.Contains(string(data), `"priority": 1000`) || !strings.Contains(string(data), `"winner": {`) {
					t.Fatalf("explain = %s", data)
				}
			} else if string(data) != tc.want {
				t.Fatalf("got %q, want %q", data, tc.want)
			}
		})
	}
}

func TestRunDumpFormatsRedactPasswords(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "base"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "base", "app.yaml"), []byte("password: canary\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			out, err := os.CreateTemp(t.TempDir(), "stdout")
			if err != nil {
				t.Fatal(err)
			}
			old := os.Stdout
			os.Stdout = out
			defer func() { os.Stdout = old; _ = out.Close() }()
			if err := runDump([]string{"-dir", dir, "-format", format}); err != nil {
				t.Fatal(err)
			}
			os.Stdout = old
			data, err := os.ReadFile(out.Name())
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "canary") || !strings.Contains(string(data), "***REDACTED***") {
				t.Fatalf("dump=%s", data)
			}
			if format == "json" {
				var v map[string]any
				if err := json.Unmarshal(data, &v); err != nil {
					t.Fatal(err)
				}
			} else {
				st, err := loadSnapshot(cli.Flags{Dir: dir})
				if err != nil {
					t.Fatal(err)
				}
				want, err := st.Dump(fastconf.YAML)
				if err != nil || string(data) != string(want) {
					t.Fatalf("yaml=%s want=%s err=%v", data, want, err)
				}
			}
		})
	}
}
