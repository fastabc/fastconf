package opa_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/fastabc/fastconf/policy"
	"github.com/fastabc/fastconf/policy/opa"
)

type cfg struct {
	Profile string `json:"profile"`
	Debug   bool   `json:"debug"`
}

func TestPolicy_Evaluate(t *testing.T) {
	module := `package fastconf
 deny[msg] { input.config.debug == true; input.config.profile == "prod"; msg := "no debug in prod" }`
	p, err := opa.New[cfg]("no-debug", module)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "no-debug" {
		t.Fatalf("Name=%q", p.Name())
	}
	for _, tc := range []struct {
		name   string
		config cfg
		denied bool
	}{
		{"deny production debug", cfg{Profile: "prod", Debug: true}, true},
		{"allow production", cfg{Profile: "prod"}, false},
		{"allow development debug", cfg{Profile: "dev", Debug: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := p.Evaluate(context.Background(), policy.Input[cfg]{Config: &tc.config})
			if err != nil {
				t.Fatal(err)
			}
			var want []policy.Violation
			if tc.denied {
				want = []policy.Violation{{Rule: "no-debug", Message: "no debug in prod", Severity: policy.SeverityError}}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Evaluate=%+v; want %+v", got, want)
			}
		})
	}
}

func TestNewWithQuery_InputAndResult(t *testing.T) {
	for _, tc := range []struct {
		name, module, query string
		want                []string
	}{
		{"context string", `package fastconf
 message := sprintf("%s/%s",[input.reason,input.tenant])`, "data.fastconf.message", []string{"preview/acme"}},
		{"array", `package fastconf
 deny := ["first","second"]`, "data.fastconf.deny", []string{"first", "second"}},
		{"undefined", `package fastconf
 deny := []`, "data.fastconf.missing", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := opa.NewWithQuery[cfg]("rules", tc.module, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.Evaluate(context.Background(), policy.Input[cfg]{Config: &cfg{}, Reason: "preview", Tenant: "acme"})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("violations=%v; want %v", got, tc.want)
			}
			for i, message := range tc.want {
				if got[i].Message != message || got[i].Rule != "rules" || got[i].Severity != policy.SeverityError {
					t.Fatalf("violation=%+v; want %q", got[i], message)
				}
			}
		})
	}
}

func TestPolicy_Errors(t *testing.T) {
	if p, err := opa.New[cfg]("invalid", `package [`); p != nil || err == nil {
		t.Fatalf("New=%v, %v; want compile error", p, err)
	}
	p, err := opa.New[chan int]("channel", `package fastconf
 deny := []`)
	if err != nil {
		t.Fatal(err)
	}
	channel := make(chan int)
	if _, err := p.Evaluate(context.Background(), policy.Input[chan int]{Config: &channel}); err == nil || !strings.Contains(err.Error(), "opa: marshal config:") {
		t.Fatalf("Evaluate=%v; want marshal error", err)
	}
}
