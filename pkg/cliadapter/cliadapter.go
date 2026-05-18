// Package cliadapter converts CLI flag state into a fastconf-compatible
// nested map containing only the flags the user explicitly set on the
// command line. Defaults are skipped.
//
// This exists to avoid the well-known spf13/viper BindPFlag footgun: binding
// every flag (including unset ones still holding their default) causes the
// default to silently override file / env config the user did set. By only
// emitting flags whose value was actually provided, the resulting map can
// safely be wrapped by provider.NewCLIChanged at PriorityCLI without
// poisoning lower-priority layers.
//
// # Adapters
//
//   - FromStdFlag — adapter for the stdlib `flag` package.
//   - From        — extensibility hook for any flag library that can iterate
//     "set" / "changed" flags. The integrations/cli/* sub-modules use this to
//     adapt spf13/pflag etc. without dragging their dependencies into the
//     root module.
//
// # Dotted-key nesting
//
// Flag names containing "." are split into nested maps so they merge
// naturally with file / env layers that use the same dotted hierarchy:
//
//	--database.dsn=postgres://...
//	→ {"database":{"dsn":"postgres://..."}}
//
// Flag names without "." remain flat keys. Values are always strings; the
// downstream typed decoder (DefaultTypedHooks) coerces them to the
// destination field type.
package cliadapter

import (
	"flag"
	"strings"
)

// From builds a nested map by invoking visit, which must call yield once per
// explicitly-set flag. Use this to adapt any flag library that exposes
// iteration over set / changed flags (pflag, urfave/cli, kong, …).
//
// Implementations of visit are responsible for filtering out unset flags;
// From itself performs no such filtering.
func From(visit func(yield func(name, value string))) map[string]any {
	out := map[string]any{}
	visit(func(name, value string) {
		if name == "" {
			return
		}
		nest(out, strings.Split(name, "."), value)
	})
	return out
}

// FromStdFlag returns a nested map of only the flags explicitly set on fs.
// It uses (*flag.FlagSet).Visit, which by contract walks set flags only.
//
// Pass the result to provider.NewCLIChanged at the call site:
//
//	fs := flag.NewFlagSet("app", flag.ExitOnError)
//	fs.String("database.dsn", "", "db connection string")
//	_ = fs.Parse(os.Args[1:])
//	mgr.Add(provider.NewCLIChanged(cliadapter.FromStdFlag(fs)))
func FromStdFlag(fs *flag.FlagSet) map[string]any {
	return From(func(yield func(name, value string)) {
		fs.Visit(func(f *flag.Flag) {
			yield(f.Name, f.Value.String())
		})
	})
}

// nest sets path into m, building intermediate maps as needed. When an
// intermediate position already holds a non-map value, the existing leaf is
// overwritten with a fresh sub-map so the path can be completed; this keeps
// the nesting rule "last write wins" consistent with the rest of fastconf.
func nest(m map[string]any, path []string, value any) {
	for i, seg := range path {
		if i == len(path)-1 {
			m[seg] = value
			return
		}
		child, ok := m[seg].(map[string]any)
		if !ok {
			child = map[string]any{}
			m[seg] = child
		}
		m = child
	}
}
