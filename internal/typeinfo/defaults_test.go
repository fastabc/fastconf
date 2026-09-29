package typeinfo_test

import (
	"reflect"
	"testing"

	"github.com/fastabc/fastconf/internal/typeinfo"
)

type withDefaults struct {
	Host  string  `fc:"default=localhost"`
	Port  int     `fc:"default=8080"`
	Debug bool    `fc:"default=true"`
	Ratio float64 `fc:"default=0.5"`
	Set   string  // no default
}

func TestApplyStructDefaults_FillsZero(t *testing.T) {
	v := &withDefaults{}
	if err := typeinfo.ApplyStructDefaults(v); err != nil {
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
	if err := typeinfo.ApplyStructDefaults(v); err != nil {
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
	if err := typeinfo.ApplyStructDefaults(v); err == nil {
		t.Fatal("expected parse error")
	}
}

type withMeta struct {
	Mode string  `fc:"required,oneof=on|off"`
	Pct  float64 `fc:"min=0,max=100"`
	Lvl  int     `fc:"oneof=1|2|3"`
}

func TestFieldMetaFor_Caches(t *testing.T) {
	specs1 := typeinfo.FieldMetaFor(reflect.TypeFor[withMeta]())
	specs2 := typeinfo.FieldMetaFor(reflect.TypeFor[withMeta]())
	if len(specs1) != 3 {
		t.Fatalf("want 3 specs, got %d: %+v", len(specs1), specs1)
	}
	// Same backing slice on cache hit.
	if &specs1[0] != &specs2[0] {
		t.Fatal("FieldMetaFor should reuse cached plan")
	}
}

func TestCheckFieldMeta_RequiredEmpty(t *testing.T) {
	v := &withMeta{Pct: 50, Lvl: 1} // Mode missing
	vs := typeinfo.CheckFieldMeta(v)
	if len(vs) != 1 {
		t.Fatalf("want 1 violation, got %+v", vs)
	}
	if vs[0].Path != "mode" {
		t.Fatalf("violation path: %q", vs[0].Path)
	}
}

func TestCheckFieldMeta_RangeViolations(t *testing.T) {
	v := &withMeta{Mode: "on", Pct: 150, Lvl: 1}
	vs := typeinfo.CheckFieldMeta(v)
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
	vs := typeinfo.CheckFieldMeta(v)
	if len(vs) < 2 {
		t.Fatalf("expected 2+ violations (Mode + Lvl), got %+v", vs)
	}
}

func TestParseFieldTag_AllTokens(t *testing.T) {
	fs := typeinfo.ParseFieldTag("required,min=1,max=10,oneof=a|b|c,default=foo,desc=help")
	if !fs.Required || fs.Min == nil || fs.Max == nil || len(fs.OneOf) != 3 {
		t.Fatalf("parsed wrong: %+v", fs)
	}
}

func TestParseFieldTag_IgnoresUnknown(t *testing.T) {
	fs := typeinfo.ParseFieldTag("notatoken,required,bogus=x")
	if !fs.Required {
		t.Fatal("required should still parse")
	}
}

func TestFieldMetaFor_IgnoresNonConstraintTags(t *testing.T) {
	type config struct {
		Host string `fc:"default=localhost,desc=host,secret"`
	}
	if specs := typeinfo.FieldMetaFor(reflect.TypeFor[config]()); len(specs) != 0 {
		t.Fatalf("non-constraint tags produced metadata: %+v", specs)
	}
	var value config
	if err := typeinfo.ApplyStructDefaults(&value); err != nil || value.Host != "localhost" {
		t.Fatalf("defaults must still apply: value=%+v, err=%v", value, err)
	}
}
