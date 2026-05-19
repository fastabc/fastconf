package fastconf_test

import (
	"context"
	"os"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
)

// testEnvVar is a unique env var name used in overlay-axis hostname tests to
// avoid accidentally interfering with real HOST, REGION, or ZONE variables.
const testHostAxisEnv = "FASTCONF_TEST_HOST_AXIS_VAR"

// unsetEnvForTest ensures the given env var is absent for the duration of the
// test, restoring the original value (or absence) on cleanup.
func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	if orig, present := os.LookupEnv(key); present {
		t.Cleanup(func() { os.Setenv(key, orig) })
		os.Unsetenv(key)
	} else {
		t.Cleanup(func() { os.Unsetenv(key) })
	}
}

// Note: the os.Hostname() error path in pipeline.go (which logs a Warn and
// skips the axis) is not covered by these tests because Go provides no
// portable way to mock os.Hostname(). The error path is intentionally
// defensive code for unusual system states (e.g., container misconfiguration).

// TestOverlayAxis_DefaultFromHostname_UsesHostname verifies that when the env
// var is absent and DefaultFromHostname is true, os.Hostname() is used as the
// axis value and the matching overlay directory is loaded.
func TestOverlayAxis_DefaultFromHostname_UsesHostname(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Skip("os.Hostname() failed, skipping:", err)
	}

	type cfg struct {
		Name string `yaml:"name"`
	}

	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":                   {Data: []byte("name: base\n")},
		"conf.d/hosts/" + hostname + "/00.yaml": {Data: []byte("name: from-hostname\n")},
	}

	unsetEnvForTest(t, testHostAxisEnv)

	mgr, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithMultiAxisOverlays(
			fastconf.OverlayAxis{
				Dir:                 "hosts",
				EnvVar:              testHostAxisEnv,
				Priority:            3200,
				DefaultFromHostname: true,
			},
		),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()

	if got := mgr.Get().Name; got != "from-hostname" {
		t.Errorf("name = %q; want %q (hostname overlay should be loaded)", got, "from-hostname")
	}
}

// TestOverlayAxis_DefaultFromHostname_EnvVarWins verifies that an explicit
// non-empty env var value takes precedence over os.Hostname().
func TestOverlayAxis_DefaultFromHostname_EnvVarWins(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Skip("os.Hostname() failed, skipping:", err)
	}

	type cfg struct {
		Name string `yaml:"name"`
	}

	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":                   {Data: []byte("name: base\n")},
		"conf.d/hosts/" + hostname + "/00.yaml": {Data: []byte("name: from-hostname\n")},
		"conf.d/hosts/explicit-host/00.yaml":    {Data: []byte("name: from-explicit\n")},
	}

	t.Setenv(testHostAxisEnv, "explicit-host")

	mgr, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithMultiAxisOverlays(
			fastconf.OverlayAxis{
				Dir:                 "hosts",
				EnvVar:              testHostAxisEnv,
				Priority:            3200,
				DefaultFromHostname: true,
			},
		),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()

	if got := mgr.Get().Name; got != "from-explicit" {
		t.Errorf("name = %q; want %q (explicit env var should win over hostname)", got, "from-explicit")
	}
}

// TestOverlayAxis_DefaultFromHostname_EmptyEnvVarSkips verifies that when the
// env var is explicitly set to an empty string, the axis is skipped even when
// DefaultFromHostname is true. This is the operator opt-out path.
func TestOverlayAxis_DefaultFromHostname_EmptyEnvVarSkips(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Skip("os.Hostname() failed, skipping:", err)
	}

	type cfg struct {
		Name string `yaml:"name"`
	}

	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":                   {Data: []byte("name: base\n")},
		"conf.d/hosts/" + hostname + "/00.yaml": {Data: []byte("name: from-hostname\n")},
	}

	// Set env var to empty string to disable the axis.
	t.Setenv(testHostAxisEnv, "")

	mgr, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithMultiAxisOverlays(
			fastconf.OverlayAxis{
				Dir:                 "hosts",
				EnvVar:              testHostAxisEnv,
				Priority:            3200,
				DefaultFromHostname: true,
			},
		),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()

	if got := mgr.Get().Name; got != "base" {
		t.Errorf("name = %q; want %q (empty env var should disable host overlay)", got, "base")
	}
}

// TestOverlayAxis_DefaultFromHostname_FalseSkips verifies the default behavior
// (DefaultFromHostname: false): when the env var is absent, the axis is skipped.
func TestOverlayAxis_DefaultFromHostname_FalseSkips(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Skip("os.Hostname() failed, skipping:", err)
	}

	type cfg struct {
		Name string `yaml:"name"`
	}

	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":                   {Data: []byte("name: base\n")},
		"conf.d/hosts/" + hostname + "/00.yaml": {Data: []byte("name: from-hostname\n")},
	}

	unsetEnvForTest(t, testHostAxisEnv)

	mgr, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithMultiAxisOverlays(
			fastconf.OverlayAxis{
				Dir:                 "hosts",
				EnvVar:              testHostAxisEnv,
				Priority:            3200,
				DefaultFromHostname: false, // explicit false
			},
		),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()

	if got := mgr.Get().Name; got != "base" {
		t.Errorf("name = %q; want %q (no hostname fallback when DefaultFromHostname is false)", got, "base")
	}
}

// TestPresetHierarchical_HostsAxisHasDefaultFromHostname verifies that the
// PresetHierarchical preset sets DefaultFromHostname: true on the hosts axis.
func TestPresetHierarchical_HostsAxisHasDefaultFromHostname(t *testing.T) {
	// Access internal options via a white-box test approach: verify preset behavior
	// by testing the observable outcome rather than the struct field directly.
	hostname, err := os.Hostname()
	if err != nil {
		t.Skip("os.Hostname() failed, skipping:", err)
	}

	type cfg struct {
		Name string `yaml:"name"`
	}

	// Ensure the default HOST env var is absent so hostname fallback can activate.
	unsetEnvForTest(t, "HOST")

	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":                   {Data: []byte("name: base\n")},
		"conf.d/hosts/" + hostname + "/00.yaml": {Data: []byte("name: from-hostname\n")},
	}

	mgr, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.PresetHierarchical(fastconf.HierarchicalOpts{}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()

	if got := mgr.Get().Name; got != "from-hostname" {
		t.Errorf("name = %q; want %q (PresetHierarchical hosts axis should use hostname fallback)", got, "from-hostname")
	}
}
