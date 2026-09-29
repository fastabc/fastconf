package cue_test

import (
	"context"
	"strings"
	"testing"

	cuepol "github.com/fastabc/fastconf/cue/policy"
	"github.com/fastabc/fastconf/policy"
)

type cfg struct {
	Port int `json:"port"`
}

func TestPolicy_Evaluate(t *testing.T) {
	p, err := cuepol.New[cfg]("port-range", `port: >0 & <65536`)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "port-range" {
		t.Fatalf("Name=%q", p.Name())
	}
	for _, tc := range []struct {
		name    string
		port    int
		invalid bool
	}{
		{"valid", 8080, false}, {"lower boundary", 0, true}, {"upper boundary", 65536, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			violations, err := p.Evaluate(context.Background(), policy.Input[cfg]{Config: &cfg{Port: tc.port}})
			if err != nil {
				t.Fatal(err)
			}
			if !tc.invalid {
				if len(violations) != 0 {
					t.Fatalf("violations=%v", violations)
				}
				return
			}
			if len(violations) != 1 || violations[0].Rule != "port-range" || violations[0].Severity != policy.SeverityError || !strings.Contains(violations[0].Message, "port") {
				t.Fatalf("violations=%+v", violations)
			}
		})
	}
}

func TestNew_InvalidSchema(t *testing.T) {
	if p, err := cuepol.New[cfg]("invalid", `port: >`); p != nil || err == nil {
		t.Fatalf("New=%v, %v; want nil and compile error", p, err)
	}
}

func TestPolicy_RejectsUnserializableConfig(t *testing.T) {
	p, err := cuepol.New[chan int]("channel", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	channel := make(chan int)
	if _, err := p.Evaluate(context.Background(), policy.Input[chan int]{Config: &channel}); err == nil || !strings.Contains(err.Error(), "cue: marshal:") {
		t.Fatalf("Evaluate=%v; want marshal error", err)
	}
}
