package pflag

import (
	"reflect"
	"testing"

	flagpkg "github.com/spf13/pflag"
)

func TestFromChanged_OnlyChangedFlagsAppear(t *testing.T) {
	fs := flagpkg.NewFlagSet("app", flagpkg.ContinueOnError)
	fs.String("database.dsn", "default-dsn", "")
	fs.Int("server.port", 8080, "")
	fs.Bool("debug", false, "")

	if err := fs.Parse([]string{"--database.dsn=postgres://override", "--debug=true"}); err != nil {
		t.Fatalf("parse: %v", err)
	}

	got := FromChanged(fs)

	want := map[string]any{
		"database": map[string]any{"dsn": "postgres://override"},
		"debug":    "true",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FromChanged mismatch\n got=%#v\nwant=%#v", got, want)
	}
	if _, ok := got["server"]; ok {
		t.Fatalf("unset flag server.port leaked into changed map: %#v", got)
	}
}

func TestFromChanged_EmptyWhenNothingChanged(t *testing.T) {
	fs := flagpkg.NewFlagSet("app", flagpkg.ContinueOnError)
	fs.String("a", "x", "")
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := FromChanged(fs); len(got) != 0 {
		t.Fatalf("expected empty map, got %#v", got)
	}
}

// pflag stores typed values; verify that explicitly-set typed flags surface as
// their stringified form (the typed decoder coerces downstream).
func TestFromChanged_TypedValuesStringified(t *testing.T) {
	fs := flagpkg.NewFlagSet("app", flagpkg.ContinueOnError)
	fs.Int("server.port", 8080, "")
	fs.Float64("rate", 0.0, "")
	if err := fs.Parse([]string{"--server.port=9090", "--rate=1.5"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := FromChanged(fs)
	want := map[string]any{
		"server": map[string]any{"port": "9090"},
		"rate":   "1.5",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("typed-value mismatch\n got=%#v\nwant=%#v", got, want)
	}
}
