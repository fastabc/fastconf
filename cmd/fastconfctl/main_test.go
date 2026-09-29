package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildJSONChanges(t *testing.T) {
	a := map[string]any{"x": 1.0, "y": "old", "z": "same"}
	b := map[string]any{"x": 2.0, "y": "new", "w": "added"}
	changes := buildJSONChanges("", a, b)
	ops := map[string]string{}
	for _, c := range changes {
		ops[c["path"].(string)] = c["op"].(string)
	}
	if ops["x"] != "~" {
		t.Errorf("x: want ~, got %q", ops["x"])
	}
	if ops["y"] != "~" {
		t.Errorf("y: want ~, got %q", ops["y"])
	}
	if ops["z"] != "-" {
		t.Errorf("z: want -, got %q", ops["z"])
	}
	if ops["w"] != "+" {
		t.Errorf("w: want +, got %q", ops["w"])
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
