package typeinfo

import (
	"reflect"
	"testing"
)

func TestCheckFieldMeta_PointerFields(t *testing.T) {
	type cfg struct {
		Mode  *string `fc:"oneof=a|b"`
		Level **int   `fc:"min=1,max=5"`
		Must  *string `fc:"required,oneof=x"`
	}
	s, x, invalid, high, low, valid := "a", "x", "c", 7, 0, 3
	highPtr, lowPtr, validPtr := &high, &low, &valid
	var nilPtr *int
	for _, tc := range []struct {
		name  string
		value cfg
		path  string
	}{
		{"valid", cfg{Mode: &s, Level: &validPtr, Must: &x}, ""},
		{"max", cfg{Level: &highPtr, Must: &x}, "level"},
		{"min", cfg{Level: &lowPtr, Must: &x}, "level"},
		{"oneof", cfg{Mode: &invalid, Must: &x}, "mode"},
		{"nil optional", cfg{Must: &x}, ""},
		{"nested nil", cfg{Level: &nilPtr, Must: &x}, ""},
		{"required only", cfg{}, "must"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := CheckFieldMeta(&tc.value)
			if tc.path == "" {
				if len(v) != 0 {
					t.Fatalf("valid pointer values rejected: %v", v)
				}
			} else if len(v) != 1 || v[0].Path != tc.path {
				t.Fatalf("violations=%v, want only %s", v, tc.path)
			}
		})
	}
}

type withDefaults struct {
	Host  string  `fc:"default=localhost"`
	Port  int     `fc:"default=8080"`
	Debug bool    `fc:"default=true"`
	Ratio float64 `fc:"default=0.5"`
	Set   string  // no default
}

func TestApplyStructDefaults_FillsZero(t *testing.T) {
	v := &withDefaults{}
	if err := ApplyStructDefaults(v); err != nil {
		t.Fatal(err)
	}
	if v.Host != "localhost" || v.Port != 8080 || !v.Debug || v.Ratio != 0.5 {
		t.Fatalf("defaults not applied: %+v", v)
	}
	if v.Set != "" {
		t.Fatalf("Set should remain zero: %q", v.Set)
	}
}

func TestApplyStructDefaults_PreservesUserValues(t *testing.T) {
	v := &withDefaults{Host: "user", Port: 9999, Ratio: 0.1}
	if err := ApplyStructDefaults(v); err != nil {
		t.Fatal(err)
	}
	if v.Host != "user" || v.Port != 9999 || v.Ratio != 0.1 {
		t.Fatalf("user values overwritten: %+v", v)
	}
	if !v.Debug {
		t.Fatal("Debug zero-default should fire")
	}
}

func TestApplyStructDefaults_InvalidValue(t *testing.T) {
	type bad struct {
		P int `fc:"default=notanumber"`
	}
	v := &bad{}
	if err := ApplyStructDefaults(v); err == nil {
		t.Fatal("expected parse error")
	}
}

type withMeta struct {
	Mode string  `fc:"required,oneof=on|off"`
	Pct  float64 `fc:"min=0,max=100"`
	Lvl  int     `fc:"oneof=1|2|3"`
}

func TestFieldMetaFor_Caches(t *testing.T) {
	specs1 := fieldMetaPlanFor(reflect.TypeFor[withMeta]())
	specs2 := fieldMetaPlanFor(reflect.TypeFor[withMeta]())
	if len(specs1) != 3 {
		t.Fatalf("want 3 specs, got %d: %+v", len(specs1), specs1)
	}
	// Same backing slice on cache hit.
	if &specs1[0] != &specs2[0] {
		t.Fatal("fieldMetaPlanFor should reuse cached plan")
	}
}

func TestCheckFieldMeta_RequiredEmpty(t *testing.T) {
	v := &withMeta{Pct: 50, Lvl: 1} // Mode missing
	vs := CheckFieldMeta(v)
	if len(vs) != 1 {
		t.Fatalf("want 1 violation, got %+v", vs)
	}
	if vs[0].Path != "mode" {
		t.Fatalf("violation path: %q", vs[0].Path)
	}
}

func TestCheckFieldMeta_RangeViolations(t *testing.T) {
	v := &withMeta{Mode: "on", Pct: 150, Lvl: 1}
	vs := CheckFieldMeta(v)
	if len(vs) == 0 {
		t.Fatal("expected violation for Pct > max")
	}
	found := false
	for _, vio := range vs {
		if vio.Path == "pct" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing pct violation: %+v", vs)
	}
}

func TestCheckFieldMeta_OneOfMismatch(t *testing.T) {
	v := &withMeta{Mode: "weird", Pct: 50, Lvl: 7}
	vs := CheckFieldMeta(v)
	if len(vs) < 2 {
		t.Fatalf("expected 2+ violations (Mode + Lvl), got %+v", vs)
	}
}

func TestParseFieldTag_AllTokens(t *testing.T) {
	fs := parseFieldTag("required,min=1,max=10,oneof=a|b|c,default=foo,desc=help")
	if !fs.Required || fs.Min == nil || fs.Max == nil || len(fs.OneOf) != 3 {
		t.Fatalf("parsed wrong: %+v", fs)
	}
}

func TestParseFieldTag_IgnoresUnknown(t *testing.T) {
	fs := parseFieldTag("notatoken,required,bogus=x")
	if !fs.Required {
		t.Fatal("required should still parse")
	}
}

func TestFieldMetaFor_IgnoresNonConstraintTags(t *testing.T) {
	type config struct {
		Host string `fc:"default=localhost,desc=host,secret"`
	}
	if specs := fieldMetaPlanFor(reflect.TypeFor[config]()); len(specs) != 0 {
		t.Fatalf("non-constraint tags produced metadata: %+v", specs)
	}
	var value config
	if err := ApplyStructDefaults(&value); err != nil || value.Host != "localhost" {
		t.Fatalf("defaults must still apply: value=%+v, err=%v", value, err)
	}
}

type embeddedInner struct {
	Port int    `fc:"default=8080"`
	Name string `fc:"required"`
}
type embeddedOuter struct {
	embeddedInner
	Mode string
}
type embeddedPtrOuter struct{ *embeddedInner }

func TestDefaults_UnexportedEmbedPromotedFields(t *testing.T) {
	var c embeddedOuter
	if err := ApplyStructDefaults(&c); err != nil {
		t.Fatal(err)
	}
	if c.Port != 8080 {
		t.Fatalf("Port=%d", c.Port)
	}
	if v := CheckFieldMeta(&c); len(v) != 1 || v[0].Path != "name" {
		t.Fatalf("violations=%v", v)
	}
}
func TestDefaults_NilEmbeddedPointerUntouched(t *testing.T) {
	var c embeddedPtrOuter
	if err := ApplyStructDefaults(&c); err != nil {
		t.Fatal(err)
	}
	if c.embeddedInner != nil {
		t.Fatal("allocated optional pointer")
	}
	if v := CheckFieldMeta(&c); len(v) != 0 {
		t.Fatalf("violations=%v", v)
	}
}
