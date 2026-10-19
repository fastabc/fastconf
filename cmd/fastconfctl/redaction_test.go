package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/internal/testutil"
)

func captureStdout(t *testing.T, run func() error) string {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = out
	defer func() { os.Stdout = old; _ = out.Close() }()
	if err := run(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCLISecretViews(t *testing.T) {
	dir := testutil.TempConf(t, map[string]string{
		"base/a.yaml":          "db:\n  password: base-pw\n",
		"overlays/prod/a.yaml": "db:\n  password: prod-pw\n",
	})
	for _, tc := range []struct {
		name string
		run  func([]string) error
		args []string
	}{
		{"dump-json", runDump, []string{"-format=json"}},
		{"dump-yaml", runDump, []string{"-format=yaml"}},
		{"diff", runDiff, []string{"-to=prod"}},
		{"diff-json", runDiff, []string{"-to=prod", "-json"}},
		{"explain", runExplain, []string{"-profile=prod", "db.password"}},
	} {
		for _, plain := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "-redacted", true: "-plaintext"}[plain], func(t *testing.T) {
				args := []string{"-dir=" + dir}
				if plain {
					args = append(args, "-unredacted")
				}
				args = append(args, tc.args...)
				out := captureStdout(t, func() error { return tc.run(args) })
				if plain {
					if !strings.Contains(out, "base-pw") && !strings.Contains(out, "prod-pw") {
						t.Fatalf("plaintext = %s", out)
					}
				} else if strings.Contains(out, "base-pw") || strings.Contains(out, "prod-pw") || !strings.Contains(out, "***REDACTED***") {
					t.Fatalf("redacted = %s", out)
				}
				if tc.name == "diff" && !strings.Contains(out, "~ db.password") {
					t.Fatalf("missing secret-only change: %s", out)
				}
			})
		}
	}
}

func TestDiffJSONSchemaUnchanged(t *testing.T) {
	dir := testutil.TempConf(t, map[string]string{
		"base/a.yaml":          "x: 1\nz: old\n",
		"overlays/prod/a.yaml": "x: 2\nw: added\n",
	})
	out := captureStdout(t, func() error { return runDiff([]string{"-dir=" + dir, "-to=prod", "-json"}) })
	var result struct {
		From    string           `json:"from"`
		To      string           `json:"to"`
		Changes []map[string]any `json:"changes"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.From != "" || result.To != "prod" || len(result.Changes) != 2 {
		t.Fatalf("schema: %s", out)
	}
	for _, c := range result.Changes {
		switch c["path"] {
		case "x":
			if len(c) != 4 || c["op"] != "~" || c["from"] != float64(1) || c["to"] != float64(2) {
				t.Fatalf("modified: %v", c)
			}
		case "w":
			if len(c) != 3 || c["op"] != "+" || c["to"] != "added" {
				t.Fatalf("added: %v", c)
			}
		default:
			t.Fatalf("unexpected change: %v", c)
		}
	}
}

func TestDiffJSONRemovedAndEmpty(t *testing.T) {
	for _, tc := range []struct {
		entries []fastconf.DiffEntry
		want    string
	}{
		{[]fastconf.DiffEntry{{Path: "nested.x", Change: fastconf.DiffRemoved, Before: "old"}}, `[{"from":"old","op":"-","path":"nested.x"}]`},
		{nil, "null"},
	} {
		out := captureStdout(t, func() error { return printJSONDiff("dev", "prod", tc.entries) })
		var result map[string]json.RawMessage
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatal(err)
		}
		// Compare decoded values so JSON indentation and map ordering do not matter.
		var got, want any
		if err := json.Unmarshal(result["changes"], &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("changes = %s, want %s", result["changes"], tc.want)
		}
	}
}
