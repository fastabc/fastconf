package fastconf

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// gorelease treats exported fields of unexported types as API when an exported
// type reaches them (Option is func(*options)). Renaming such a field fails the
// patch-release API check, so the reachable field names are pinned here.
func TestOptionReachableFieldNamesStable(t *testing.T) {
	want := map[string]string{
		"fastconf.options":       "Coalesce DefaultProf DeferredErrs Dir FS Generators HistoryCap Logger MergeKeys ObserverTimeout Observers OverlayAxes Policies ProfileEnv ProfileMatch ProfileNames Provenance Providers SecretPaths SecretRedactor SecretResolver SkipDiscovery Strict Tenant Tracer Transforms TypedFor TypedHooks TypedHooksOff UnknownFields Validators Watch WatchPaths YAMLBridge",
		"fastconf.providerEntry": "Info Provider",
		"scan.AxisSpec":          "DefaultFromHostname Dir EnvVar Priority",
		"coalesce.Options":       "MaxLag Quiet SwapHint",
	}
	got := map[string]string{}
	seen := map[reflect.Type]bool{}
	var visit func(reflect.Type)
	visit = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
			if t.Kind() == reflect.Map {
				visit(t.Key())
			}
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || seen[t] || !strings.HasPrefix(t.PkgPath(), "github.com/fastabc/fastconf") {
			return
		}
		seen[t] = true
		var names []string
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.IsExported() {
				names = append(names, f.Name)
				visit(f.Type)
			}
		}
		slices.Sort(names)
		if _, pinned := want[t.String()]; pinned {
			got[t.String()] = strings.Join(names, " ")
		}
	}
	visit(reflect.TypeFor[options]())
	for typ, fields := range want {
		if got[typ] != fields {
			t.Errorf("%s exported fields changed (breaks gorelease):\n got: %s\nwant: %s", typ, got[typ], fields)
		}
	}
}
