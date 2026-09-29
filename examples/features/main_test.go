package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestFeatureEvaluation(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"alice checkout=true stable=true\n",
		"missing flag=false missing anchor=false\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in output:\n%s", want, &out)
		}
	}
	if strings.Count(out.String(), "stable=true") != 3 {
		t.Fatal("rollouts changed for the same request attributes")
	}
}
