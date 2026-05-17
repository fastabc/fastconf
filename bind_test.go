package fastconf_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/parser"
	"github.com/fastabc/fastconf/pkg/source"
)

func TestBind_AutoByContentType(t *testing.T) {
	src := source.NewBytes("inline", ".yaml", []byte("a: 1\nb: two\n"))
	p := fastconf.Bind(src, nil)
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got["a"] != 1 || got["b"] != "two" {
		t.Errorf("decoded %v", got)
	}
}

func TestBind_ExplicitParserWinsOverHint(t *testing.T) {
	// Source claims toml content-type, but we bind YAML — explicit wins.
	src := source.NewBytes("misnamed", "toml", []byte("a: 1\n"))
	p := fastconf.Bind(src, parser.YAML())
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("explicit parser should override hint: %v", err)
	}
	if got["a"] != 1 {
		t.Errorf("decoded %v", got)
	}
}

func TestBind_UnknownParserContentType(t *testing.T) {
	src := source.NewBytes("inline", "application/no-such-format", []byte("garbage"))
	p := fastconf.Bind(src, nil)
	_, err := p.Load(context.Background())
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, fastconf.ErrParserUnknown) {
		t.Fatalf("want ErrParserUnknown, got %v", err)
	}
	if !strings.Contains(err.Error(), "inline") {
		t.Errorf("error missing source name: %v", err)
	}
}

func TestBind_EmptyPayloadReturnsEmptyMap(t *testing.T) {
	// Missing/empty Source should not require a Parser to be configured.
	src := source.NewBytes("empty", "", nil)
	p := fastconf.Bind(src, nil)
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("want empty map, got %v", got)
	}
}

func TestBind_ForwardsNameAndPriority(t *testing.T) {
	src := source.NewBytes("seed", ".yaml", []byte("a: 1")).WithPriority(contracts.PriorityKV)
	p := fastconf.Bind(src, nil)
	if p.Name() != "seed" {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Priority() != contracts.PriorityKV {
		t.Errorf("Priority = %d", p.Priority())
	}
}

func TestWithSource_NilSrcIsNoop(t *testing.T) {
	// Should not panic and should not append a provider entry.
	opt := fastconf.WithSource(nil, nil)
	if opt == nil {
		t.Fatal("WithSource returned nil")
	}
}
