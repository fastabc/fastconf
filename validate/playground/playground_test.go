package playground_test

import (
	"strings"
	"testing"

	pg "github.com/fastabc/fastconf/validate/playground"
)

type cfg struct {
	Name string `validate:"required,min=2"`
	Port int    `validate:"required,gte=1,lte=65535"`
}

func TestNew_OK(t *testing.T) {
	v := pg.New[cfg]()
	if err := v(&cfg{Name: "ok", Port: 8080}); err != nil {
		t.Fatal(err)
	}
}

func TestNew_ErrorOmitsFieldValue(t *testing.T) {
	type config struct {
		Token string `validate:"len=40" fc:"secret"`
	}
	err := pg.New[config]()(&config{Token: "s3cr3t-value"})
	if err == nil || strings.Contains(err.Error(), "s3cr3t-value") {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(err.Error(), "Token") || !strings.Contains(err.Error(), "len") {
		t.Fatalf("missing field or rule: %v", err)
	}
}

func TestNew_Fail(t *testing.T) {
	v := pg.New[cfg]()
	err := v(&cfg{Name: "", Port: 0})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "playground:") {
		t.Fatalf("expected playground prefix: %v", err)
	}
}

func TestNew_NilGuard(t *testing.T) {
	v := pg.New[cfg]()
	if err := v(nil); err == nil {
		t.Fatal("expected error on nil")
	}
}
