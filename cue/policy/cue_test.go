package cue_test

import (
	"context"
	"strings"
	"testing"

	"github.com/fastabc/fastconf/policy"
	cuepol "github.com/fastabc/fastconf/cue/policy"
)

type cfg struct {
	Port int `json:"port"`
}

func TestCUE_PortRange(t *testing.T) {
	p, err := cuepol.New[cfg]("port-range", `port: >0 & <65536`)
	if err != nil {
		t.Fatal(err)
	}
	vs, err := p.Evaluate(context.Background(), policy.Input[cfg]{Config: &cfg{Port: 99999}})
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) == 0 || !strings.Contains(vs[0].Message, "port") {
		t.Fatalf("expected violation, got %+v", vs)
	}
	vs2, _ := p.Evaluate(context.Background(), policy.Input[cfg]{Config: &cfg{Port: 8080}})
	if len(vs2) != 0 {
		t.Fatalf("unexpected violation: %+v", vs2)
	}
}
