package typeinfo

import "testing"

type MetadataEmbedded struct {
	Port int `fc:"min=1,default=9"`
}

func TestMetadataNilParents(t *testing.T) {
	type embedded struct{ *MetadataEmbedded }
	type deep struct{ Parent **MetadataEmbedded }
	for _, value := range []any{&embedded{}, &deep{}, (*deep)(nil), map[string]any{}} {
		if got := CheckFieldMeta(value); len(got) != 0 {
			t.Fatal("absent parent was validated")
		}
	}
	e := embedded{}
	if err := ApplyStructDefaults(&e); err != nil || e.MetadataEmbedded != nil {
		t.Fatal("optional embed allocated")
	}
	d := deep{}
	if err := ApplyStructDefaults(&d); err != nil || d.Parent != nil {
		t.Fatal("optional parent allocated")
	}
	p := &MetadataEmbedded{Port: -1}
	d.Parent = &p
	if got := CheckFieldMeta(&d); len(got) != 1 {
		t.Fatal("existing invalid child not validated")
	}
}

func TestDefaultRejectsNumericOverflow(t *testing.T) {
	type signed struct {
		Value int8 `fc:"default=128"`
	}
	type unsigned struct {
		Value uint8 `fc:"default=256"`
	}
	type floating struct {
		Value float32 `fc:"default=1e100"`
	}
	if err := ApplyStructDefaults(&signed{}); err == nil {
		t.Fatal("signed overflow accepted")
	}
	if err := ApplyStructDefaults(&unsigned{}); err == nil {
		t.Fatal("unsigned overflow accepted")
	}
	if err := ApplyStructDefaults(&floating{}); err == nil {
		t.Fatal("float overflow accepted")
	}
}
