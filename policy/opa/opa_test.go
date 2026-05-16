package opa_test

import (
	"context"
	"testing"

	"github.com/fastabc/fastconf/policy"
	"github.com/fastabc/fastconf/policy/opa"
)

type cfg struct {
	Profile string `json:"profile"`
	Debug   bool   `json:"debug"`
}

func TestOPA_DenyDebugInProd(t *testing.T) {
	module := `package fastconf
deny[msg] { input.config.debug == true; input.config.profile == "prod"; msg := "no debug in prod" }`
	p, err := opa.New[cfg]("no-debug", module)
	if err != nil {
		t.Fatal(err)
	}
	vs, err := p.Evaluate(context.Background(), policy.Input[cfg]{
		Config: &cfg{Profile: "prod", Debug: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Severity != policy.SeverityError {
		t.Fatalf("got %+v", vs)
	}
}
