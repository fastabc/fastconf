// Package pflag converts explicitly changed spf13/pflag flags to nested maps;
// parser defaults are skipped. Pass Cobra's Flags or PersistentFlags to FromChanged.
// See examples/pflag and docs/cookbook/sources/env-replacer.md. This optional
// module keeps pflag outside the root dependency graph.
package pflag

import (
	flagpkg "github.com/spf13/pflag"

	"github.com/fastabc/fastconf/providers/cliflag"
)

// FromChanged nests explicitly set dotted flags using cliflag.From, skipping defaults.
// Values remain strings for typed decoding.
func FromChanged(fs *flagpkg.FlagSet) map[string]any {
	return cliflag.From(func(yield func(name, value string)) {
		fs.Visit(func(f *flagpkg.Flag) {
			yield(f.Name, f.Value.String())
		})
	})
}
