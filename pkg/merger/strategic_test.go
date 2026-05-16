package merger_test

import (
	"reflect"
	"testing"

	"github.com/fastabc/fastconf/pkg/merger"
)

func TestStrategicMerge_ContainersByName(t *testing.T) {
	dst := map[string]any{
		"spec": map[string]any{
			"containers": []any{
				map[string]any{"name": "api", "image": "img:v1", "port": 8080},
				map[string]any{"name": "sidecar", "image": "side:v1"},
			},
		},
	}
	src := map[string]any{
		"spec": map[string]any{
			"containers": []any{
				map[string]any{"name": "api", "image": "img:v2"}, // change image, keep port
				map[string]any{"name": "new", "image": "newpod:v1"},
			},
		},
	}
	opt := merger.Options{
		MergeKeys: map[string]string{"spec.containers": "name"},
	}
	if err := merger.Deep(dst, src, opt); err != nil {
		t.Fatal(err)
	}
	containers := dst["spec"].(map[string]any)["containers"].([]any)
	if len(containers) != 3 {
		t.Fatalf("expected 3 containers, got %d: %v", len(containers), containers)
	}
	// Find api: image should be img:v2 (overlay won), port should remain 8080 (preserved).
	var api, sidecar, newone map[string]any
	for _, c := range containers {
		m := c.(map[string]any)
		switch m["name"] {
		case "api":
			api = m
		case "sidecar":
			sidecar = m
		case "new":
			newone = m
		}
	}
	if api["image"] != "img:v2" {
		t.Errorf("api.image = %v, want img:v2", api["image"])
	}
	if api["port"] != 8080 {
		t.Errorf("api.port should be preserved, got %v", api["port"])
	}
	if sidecar["image"] != "side:v1" {
		t.Errorf("sidecar.image = %v", sidecar["image"])
	}
	if newone["image"] != "newpod:v1" {
		t.Errorf("new.image = %v", newone["image"])
	}
}

func TestStrategicMerge_NoConfigFallsBackToReplace(t *testing.T) {
	dst := map[string]any{
		"items": []any{
			map[string]any{"id": "a", "v": 1},
		},
	}
	src := map[string]any{
		"items": []any{
			map[string]any{"id": "b", "v": 2},
		},
	}
	if err := merger.Deep(dst, src, merger.Options{}); err != nil {
		t.Fatal(err)
	}
	items := dst["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "b" {
		t.Errorf("without MergeKeys default behaviour is replace: got %v", items)
	}
}

func TestStrategicMerge_NonMapEntriesPassThrough(t *testing.T) {
	dst := map[string]any{"items": []any{"x", "y"}}
	src := map[string]any{"items": []any{"z"}}
	opt := merger.Options{MergeKeys: map[string]string{"items": "name"}}
	if err := merger.Deep(dst, src, opt); err != nil {
		t.Fatal(err)
	}
	got := dst["items"].([]any)
	want := []any{"x", "y", "z"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
