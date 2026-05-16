package validate_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/fastabc/fastconf/pkg/validate"
)

type fakeSchema struct{ wantErr bool }

func (f fakeSchema) ValidateJSON(b []byte) error {
	if f.wantErr {
		return errors.New("nope")
	}
	if !strings.Contains(string(b), "name") {
		return errors.New("missing name")
	}
	return nil
}

type cfg struct {
	Name string `json:"name"`
}

func TestNewValidator_OK(t *testing.T) {
	v := validate.NewValidator[cfg](fakeSchema{})
	if err := v(&cfg{Name: "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestNewValidator_NilSchema(t *testing.T) {
	v := validate.NewValidator[cfg](nil)
	if err := v(&cfg{Name: "x"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewValidator_NilConfig(t *testing.T) {
	v := validate.NewValidator[cfg](fakeSchema{})
	if err := v(nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewValidator_SchemaError(t *testing.T) {
	v := validate.NewValidator[cfg](fakeSchema{wantErr: true})
	if err := v(&cfg{Name: "x"}); err == nil {
		t.Fatal("expected error")
	}
}
