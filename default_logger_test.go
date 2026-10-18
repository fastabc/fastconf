package fastconf

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"testing"
	"testing/fstest"
)

func TestDefaultUnknownWarningIsSilent(t *testing.T) {
	if os.Getenv("FASTCONF_TEST_SILENT_CHILD") == "1" {
		s, err := Load[struct{ Port int }](context.Background(), WithFS(fstest.MapFS{
			"conf.d/base/a.yaml": {Data: []byte("port: 8080\nprot: 1")},
		}))
		if err != nil || s.Value().Port != 8080 {
			t.Fatalf("default load: %v %v", s, err)
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDefaultUnknownWarningIsSilent$")
	cmd.Env = append(os.Environ(), "FASTCONF_TEST_SILENT_CHILD=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("child: %v: %s", err, &stderr)
	}
	if stderr.Len() != 0 {
		t.Fatalf("default logger wrote to stderr: %s", &stderr)
	}
}
