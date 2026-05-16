package transform_test

import (
	"testing"

	"github.com/fastabc/fastconf/pkg/mappath"
	"github.com/fastabc/fastconf/pkg/transform"
)

func TestExpandLabels_ComposeDeployLabels(t *testing.T) {
	root := map[string]any{
		"deploy": map[string]any{
			"labels": []any{
				"traefik.http.services.dummy-svc.loadbalancer.server.port=9999",
				"traefik.enable=true",
			},
		},
	}
	tr := transform.ExpandLabels("deploy.labels", "", transform.LabelExpandOptions{})
	if err := tr.Transform(root); err != nil {
		t.Fatalf("transform err: %v", err)
	}
	port, _ := mappath.GetDotted(root, "traefik.http.services.dummy-svc.loadbalancer.server.port")
	if port != "9999" {
		t.Fatalf("port got %v want \"9999\"", port)
	}
	if v, _ := mappath.GetDotted(root, "traefik.enable"); v != "true" {
		t.Fatalf("enable got %v", v)
	}
	if _, ok := mappath.GetDotted(root, "deploy.labels"); ok {
		t.Fatalf("source labels should have been removed by default")
	}
}

func TestExpandLabels_KeepSource(t *testing.T) {
	root := map[string]any{
		"deploy": map[string]any{"labels": []any{"a.b=c"}},
	}
	tr := transform.ExpandLabels("deploy.labels", "", transform.LabelExpandOptions{KeepSource: true})
	if err := tr.Transform(root); err != nil {
		t.Fatalf("err: %v", err)
	}
	if _, ok := mappath.GetDotted(root, "deploy.labels"); !ok {
		t.Fatalf("KeepSource=true should retain the source slice")
	}
	if v, _ := mappath.GetDotted(root, "a.b"); v != "c" {
		t.Fatalf("got %v", v)
	}
}

func TestExpandLabels_PrefixAndStrip(t *testing.T) {
	root := map[string]any{
		"meta": map[string]any{
			"annotations": map[string]string{
				"traefik.enable":            "true",
				"traefik.http.routers.api":  "Host(`api`)",
				"unrelated.k":               "v",
			},
		},
	}
	tr := transform.ExpandLabels("meta.annotations", "", transform.LabelExpandOptions{
		Prefix:      "traefik.",
		StripPrefix: true,
	})
	if err := tr.Transform(root); err != nil {
		t.Fatalf("err: %v", err)
	}
	if v, _ := mappath.GetDotted(root, "enable"); v != "true" {
		t.Fatalf("enable got %v", v)
	}
	if _, ok := root["unrelated"]; ok {
		t.Fatalf("non-prefix keys must be filtered")
	}
}

func TestExpandLabels_TargetPath(t *testing.T) {
	root := map[string]any{
		"deploy": map[string]any{"labels": []any{"http.port=9999", "enable=true"}},
	}
	tr := transform.ExpandLabels("deploy.labels", "traefik", transform.LabelExpandOptions{})
	if err := tr.Transform(root); err != nil {
		t.Fatalf("err: %v", err)
	}
	if v, _ := mappath.GetDotted(root, "traefik.http.port"); v != "9999" {
		t.Fatalf("got %v", v)
	}
	if v, _ := mappath.GetDotted(root, "traefik.enable"); v != "true" {
		t.Fatalf("got %v", v)
	}
}

func TestExpandLabels_OverlayMerge(t *testing.T) {
	root := map[string]any{
		"traefik": map[string]any{
			"enable": "false",
			"static": "preserve-me",
		},
		"deploy": map[string]any{"labels": []any{"enable=true"}},
	}
	tr := transform.ExpandLabels("deploy.labels", "traefik", transform.LabelExpandOptions{
		MergeMode: transform.ExpandOverlay,
	})
	if err := tr.Transform(root); err != nil {
		t.Fatalf("err: %v", err)
	}
	if v, _ := mappath.GetDotted(root, "traefik.enable"); v != "true" {
		t.Fatalf("overlay should let label win: got %v", v)
	}
	if v, _ := mappath.GetDotted(root, "traefik.static"); v != "preserve-me" {
		t.Fatalf("overlay must preserve pre-existing keys: got %v", v)
	}
}

func TestExpandLabels_UnderlayMerge(t *testing.T) {
	root := map[string]any{
		"traefik": map[string]any{"enable": "false"},
		"deploy":  map[string]any{"labels": []any{"enable=true", "fresh=v"}},
	}
	tr := transform.ExpandLabels("deploy.labels", "traefik", transform.LabelExpandOptions{
		MergeMode: transform.ExpandUnderlay,
	})
	if err := tr.Transform(root); err != nil {
		t.Fatalf("err: %v", err)
	}
	if v, _ := mappath.GetDotted(root, "traefik.enable"); v != "false" {
		t.Fatalf("underlay must keep pre-existing scalar: got %v", v)
	}
	if v, _ := mappath.GetDotted(root, "traefik.fresh"); v != "v" {
		t.Fatalf("underlay must still add new keys: got %v", v)
	}
}

func TestExpandLabels_MissingSourceIsNoop(t *testing.T) {
	root := map[string]any{"unrelated": 1}
	tr := transform.ExpandLabels("deploy.labels", "", transform.LabelExpandOptions{})
	if err := tr.Transform(root); err != nil {
		t.Fatalf("missing source should not error: %v", err)
	}
	if len(root) != 1 || root["unrelated"] != 1 {
		t.Fatalf("missing source should not mutate root: %v", root)
	}
}

func TestExpandLabels_CoerceTrue(t *testing.T) {
	root := map[string]any{
		"deploy": map[string]any{"labels": []any{"server.port=9999", "feature.x=true"}},
	}
	tr := transform.ExpandLabels("deploy.labels", "", transform.LabelExpandOptions{Coerce: true})
	if err := tr.Transform(root); err != nil {
		t.Fatalf("err: %v", err)
	}
	if v, _ := mappath.GetDotted(root, "server.port"); v != int64(9999) {
		t.Fatalf("port should be int64 with Coerce=true: got %v (%T)", v, v)
	}
	if v, _ := mappath.GetDotted(root, "feature.x"); v != true {
		t.Fatalf("feature.x should be bool: got %v (%T)", v, v)
	}
}
