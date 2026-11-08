package typeinfo

import (
	"reflect"
	"testing"
)

func TestFieldMeta_Precedence(t *testing.T) {
	type C struct {
		X int `desc:"new" default:"3" fc:"desc=old,default=2,required,secret,min=1,max=5,oneof=1|3|5"`
	}
	meta := FieldMeta(reflect.TypeFor[C]().Field(0))
	if meta.Desc != "new" || meta.Default != "3" || !meta.HasDefault || !meta.Required || !meta.Secret || *meta.Min != 1 || *meta.Max != 5 || len(meta.OneOf) != 3 {
		t.Fatalf("%+v", meta)
	}
}
