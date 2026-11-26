package fastconf

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"
)

// When patch paths cannot be read for provenance, the patch still applies and
// the missing origins are logged at Debug instead of disappearing silently.
func TestMergeLogsUnreadablePatchPaths(t *testing.T) {
	var logs bytes.Buffer
	m, err := New[map[string]any](context.Background(), WithFS(fstest.MapFS{
		"conf.d/base/00.yaml":       {Data: []byte("a: 1\n")},
		"conf.d/base/10.patch.json": {Data: []byte(`[{"op":"add","path":"/b","value":2,"PATH":5}]`)},
	}), WithProvenance(ProvenanceFull),
		WithLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	if _, ok := (*m.Get())["b"]; !ok {
		t.Fatalf("patch not applied: %v", *m.Get())
	}
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "level=DEBUG") && strings.Contains(line, "10.patch.json") {
			return
		}
	}
	t.Fatalf("no debug log naming the patch:\n%s", logs.String())
}
