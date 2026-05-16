// Package playground adapts go-playground/validator/v10 to the FastConf
// validator contract: a function (*T) error called by Manager.commit before
// publishing a new state.
//
// Usage:
//
//	mgr, err := fastconf.New[appCfg](ctx,
//	    fastconf.WithDir("conf.d"),
//	    fastconf.WithValidator(playground.New[appCfg]()),
//	)
package playground

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	gpv "github.com/go-playground/validator/v10"
)

var (
	defaultOnce sync.Once
	defaultV    *gpv.Validate
)

func defaultValidator() *gpv.Validate {
	defaultOnce.Do(func() { defaultV = gpv.New(gpv.WithRequiredStructEnabled()) })
	return defaultV
}

// New returns a FastConf-compatible validator that runs Struct() against *T.
// Any validation failure is wrapped into a single error whose message lists
// every offending field for fast diagnosis in CI.
func New[T any]() func(*T) error {
	v := defaultValidator()
	return func(t *T) error {
		if t == nil {
			return errors.New("playground: nil config")
		}
		if err := v.Struct(t); err != nil {
			return wrap(err)
		}
		return nil
	}
}

// NewWith allows callers to pass a pre-configured *validator.Validate (with
// custom validators / translators registered).
func NewWith[T any](v *gpv.Validate) func(*T) error {
	if v == nil {
		v = defaultValidator()
	}
	return func(t *T) error {
		if t == nil {
			return errors.New("playground: nil config")
		}
		if err := v.Struct(t); err != nil {
			return wrap(err)
		}
		return nil
	}
}

func wrap(err error) error {
	var ves gpv.ValidationErrors
	if !errors.As(err, &ves) {
		return err
	}
	parts := make([]string, 0, len(ves))
	for _, fe := range ves {
		parts = append(parts, fmt.Sprintf("%s: failed %q (got=%v)", fe.Namespace(), fe.Tag(), fe.Value()))
	}
	return fmt.Errorf("playground: %s", strings.Join(parts, "; "))
}
