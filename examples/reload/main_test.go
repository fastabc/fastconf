package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/policy"
)

func TestPreviewReloadRollback(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatal(err)
	}
	want := "invalid preview: violations=1 live=8080\n" +
		"rejected: preserved=true\n" +
		"policy preview: rejected=true live=8080\n" +
		"content changed=true warnings=1\n" +
		"valid preview: proposed=9090 live=8080 changes=1\n" +
		"committed: port=9090 generation=2\n" +
		"rollback: port=8080 generation=3\n" +
		"unchanged source: port=9090 generation=4\n" +
		"source repaired: port=8080 generation=5\n"
	if out.String() != want {
		t.Fatalf("got:\n%swant:\n%s", &out, want)
	}
}

func TestCheckPlanReports(t *testing.T) {
	for _, tc := range []struct {
		name     string
		p        fastconf.PlanResult[Config]
		rejected bool
	}{
		{"valid", fastconf.PlanResult[Config]{}, false},
		{"validator", fastconf.PlanResult[Config]{Validators: []fastconf.ValidatorReport{{Err: errors.New("bad")}}}, true},
		{"policy-error", fastconf.PlanResult[Config]{Policies: []policy.Violation{{Severity: policy.SeverityError}}}, true},
		{"policy-warning", fastconf.PlanResult[Config]{Policies: []policy.Violation{{Severity: policy.SeverityWarning}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if (checkPlan(&tc.p) != nil) != tc.rejected {
				t.Fatal("incorrect approval")
			}
		})
	}
}

func TestPreviewInvisibleChangeAndSourceReread(t *testing.T) {
	type config struct {
		Port   int    `json:"port" yaml:"port"`
		Secret string `json:"-" yaml:"secret"`
	}
	files := fstest.MapFS{"conf.d/base/a.yaml": {Data: []byte("port: 8080\nsecret: original")}}
	m, err := fastconf.New[config](context.Background(), fastconf.WithFS(files), fastconf.WithDecodeFormat(fastconf.YAML))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	before := m.Snapshot()
	files["conf.d/base/a.yaml"].Data = []byte("port: 8080\nsecret: changed")
	p, err := m.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := printChange(&out, before, p); err != nil {
		t.Fatal(err)
	}
	if out.String() != "configuration changed; no visible JSON field differences\n" {
		t.Fatal(out.String())
	}
	if strings.Contains(out.String(), "original") {
		t.Fatal("hidden value exposed")
	}
	files["conf.d/base/a.yaml"].Data = []byte("port: 9090\nsecret: later")
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Get().Port != 9090 || m.Snapshot().Hash() == p.Proposed.Hash() {
		t.Fatal("reload did not reread source")
	}
	out.Reset()
	p, err = m.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := printChange(&out, m.Snapshot(), p); err != nil {
		t.Fatal(err)
	}
	if out.String() != "content changed=false warnings=0\n" {
		t.Fatal(out.String())
	}
}
