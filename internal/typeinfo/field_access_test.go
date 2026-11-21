package typeinfo

import "testing"

type MetadataEmbedded struct {
	Port int `fc:"min=1"`
}

func TestMetadataNilParents(t *testing.T) {
	type embedded struct{ *MetadataEmbedded }
	type deep struct{ Parent **MetadataEmbedded }
	for _, value := range []any{&embedded{}, &deep{}, (*deep)(nil), map[string]any{}} {
		if got := CheckFieldMeta(value); len(got) != 0 {
			t.Fatal("absent parent was validated")
		}
	}
	d := deep{}
	p := &MetadataEmbedded{Port: -1}
	d.Parent = &p
	if got := CheckFieldMeta(&d); len(got) != 1 {
		t.Fatal("existing invalid child not validated")
	}
}
