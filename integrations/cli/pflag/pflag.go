// Package pflag adapts a spf13/pflag.FlagSet into a fastconf-compatible
// nested map containing only the flags the user explicitly set on the
// command line. Defaults are skipped.
//
// See github.com/fastabc/fastconf/pkg/cliadapter for the underlying design:
// this package is a thin layer that walks pflag's "changed" set and feeds
// each pair into cliadapter.From, which performs the dotted-key nesting.
//
// Living in its own sub-module keeps the root fastconf module free of the
// pflag dependency for callers who use stdlib `flag` or a different CLI
// library.
//
// # Usage
//
//	import (
//	    flagpkg "github.com/spf13/pflag"
//	    cliflag "github.com/fastabc/fastconf/integrations/cli/pflag"
//	    "github.com/fastabc/fastconf/pkg/provider"
//	)
//
//	fs := flagpkg.NewFlagSet("app", flagpkg.ExitOnError)
//	fs.String("database.dsn", "", "db connection string")
//	fs.Int("server.port", 8080, "listen port")
//	_ = fs.Parse(os.Args[1:])
//
//	mgr.Add(provider.NewCLI(cliflag.FromChanged(fs)))
//
// When wired through a spf13/cobra command, pass cmd.Flags() (or
// PersistentFlags()) to FromChanged.
package pflag

import (
	flagpkg "github.com/spf13/pflag"

	"github.com/fastabc/fastconf/pkg/cliadapter"
)

// FromChanged returns a nested map of only the pflag flags whose value was
// explicitly provided on the command line. It uses (*pflag.FlagSet).Visit,
// which by contract walks changed flags only — never defaults.
//
// Flag names containing "." nest into sub-maps (see cliadapter docs).
// Values are taken from f.Value.String() and remain strings; the downstream
// typed decoder coerces them at *T decode time.
func FromChanged(fs *flagpkg.FlagSet) map[string]any {
	return cliadapter.From(func(yield func(name, value string)) {
		fs.Visit(func(f *flagpkg.Flag) {
			yield(f.Name, f.Value.String())
		})
	})
}
