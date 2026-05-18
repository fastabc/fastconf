package cliadapter

import (
	"flag"
	"reflect"
	"testing"
)

func TestFromStdFlag_OnlyChangedFlagsAppear(t *testing.T) {
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	fs.String("database.dsn", "default-dsn", "")
	fs.Int("server.port", 8080, "")
	fs.Bool("debug", false, "")

	if err := fs.Parse([]string{"-database.dsn=postgres://override", "-debug=true"}); err != nil {
		t.Fatalf("parse: %v", err)
	}

	got := FromStdFlag(fs)

	want := map[string]any{
		"database": map[string]any{"dsn": "postgres://override"},
		"debug":    "true",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FromStdFlag mismatch\n got=%#v\nwant=%#v", got, want)
	}
	// server.port was never set; it must not be in the map even though its
	// default (8080) would be readable via fs.Lookup. This is the whole point
	// of cliadapter.
	if _, ok := got["server"]; ok {
		t.Fatalf("unset flag server.port leaked into changed map: %#v", got)
	}
}

func TestFromStdFlag_EmptyWhenNothingChanged(t *testing.T) {
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	fs.String("a", "x", "")
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := FromStdFlag(fs); len(got) != 0 {
		t.Fatalf("expected empty map, got %#v", got)
	}
}

func TestFrom_DottedNesting(t *testing.T) {
	got := From(func(yield func(name, value string)) {
		yield("a.b.c", "1")
		yield("a.b.d", "2")
		yield("flat", "ok")
	})
	want := map[string]any{
		"a":    map[string]any{"b": map[string]any{"c": "1", "d": "2"}},
		"flat": "ok",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("nesting mismatch\n got=%#v\nwant=%#v", got, want)
	}
}

func TestFrom_LastWriteWinsOnTypeConflict(t *testing.T) {
	// First yield writes a leaf at "a"; second yield needs "a" to be a map.
	// The adapter overwrites the leaf rather than panic — matches the
	// "last write wins" rule documented on nest().
	got := From(func(yield func(name, value string)) {
		yield("a", "leaf")
		yield("a.b", "deeper")
	})
	want := map[string]any{"a": map[string]any{"b": "deeper"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("conflict resolution mismatch\n got=%#v\nwant=%#v", got, want)
	}
}

func TestFrom_EmptyNameIgnored(t *testing.T) {
	got := From(func(yield func(name, value string)) {
		yield("", "x")
		yield("real", "y")
	})
	want := map[string]any{"real": "y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("empty-name mismatch\n got=%#v\nwant=%#v", got, want)
	}
}
