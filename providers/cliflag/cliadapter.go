// Package cliflag converts explicitly set CLI flags into nested maps. Defaults are skipped so they
// cannot override file or environment values. Dots delimit nested keys; values remain strings for
// conversion by the typed decoder.
package cliflag

import (
	"flag"
	"strings"

	"github.com/fastabc/fastconf/confmap"
)

// From builds a nested map from visit; the caller must yield only explicitly set flags.
func From(visit func(yield func(name, value string))) map[string]any {
	out := map[string]any{}
	visit(func(name, value string) {
		if name == "" {
			return
		}
		// Conflicting leaves are replaced (last write wins).
		confmap.Set(out, strings.Split(name, "."), value)
	})
	return out
}

// FromStdFlag uses flag.FlagSet.Visit to collect explicitly set flags for NewCLI.
func FromStdFlag(fs *flag.FlagSet) map[string]any {
	return From(func(yield func(name, value string)) {
		fs.Visit(func(f *flag.Flag) {
			yield(f.Name, f.Value.String())
		})
	})
}
