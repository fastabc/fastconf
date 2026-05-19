package manager

import (
	"reflect"

	"github.com/fastabc/fastconf/pkg/flog"
)

// warnIfYAMLOnlyTags scans T's exported fields once at construction time
// and emits a single warn log when *T has only `yaml:` tags but no
// `json:` / `fastconf:` tags. The default CodecBridge is BridgeJSON,
// which silently ignores `yaml:` tags — a common new-user trap when
// migrating from Koanf/Viper. The warning steers operators toward either
// adding json tags or selecting BridgeYAML.
//
// Cheap: walks fields with reflect.Type, no value introspection, no
// recursion. Runs once per Manager construction.
func warnIfYAMLOnlyTags[T any](logger *flog.Logger) {
	if logger == nil {
		return
	}
	t := reflect.TypeFor[T]()
	if t == nil {
		return
	}
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	var (
		hasYAML  bool
		hasOther bool
	)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if f.Tag.Get("json") != "" || f.Tag.Get("fastconf") != "" {
			hasOther = true
			break
		}
		if f.Tag.Get("yaml") != "" {
			hasYAML = true
		}
	}
	if hasOther || !hasYAML {
		return
	}
	logger.Warn().
		Str("type", t.String()).
		Msg("fastconf: T has yaml tags but no json/fastconf tags; default BridgeJSON ignores yaml tags. " +
			"Add WithCodecBridge(BridgeYAML) or json struct tags.")
}
