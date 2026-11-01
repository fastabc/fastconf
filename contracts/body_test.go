package contracts

import (
	"errors"
	"math"
	"os"
	"strings"
	"testing"
)

func TestReadAllMax(t *testing.T) {
	for _, tc := range []struct {
		limit int64
		large bool
	}{
		{limit: 2, large: true},
		{limit: 3},
		{limit: 0},
		{limit: math.MaxInt64},
	} {
		body, err := ReadAllMax(strings.NewReader("abc"), tc.limit)
		if tc.large {
			if !errors.Is(err, ErrTooLarge) {
				t.Fatalf("limit=%d: error=%v; want ErrTooLarge", tc.limit, err)
			}
		} else if err != nil || string(body) != "abc" {
			t.Fatalf("limit=%d: body=%q, error=%v", tc.limit, body, err)
		}
	}
}

func TestSafeErrorHidesCauseButUnwraps(t *testing.T) {
	cause := &os.PathError{Op: "read", Path: "token=secret", Err: os.ErrPermission}
	err := SafeError("fetch failed", cause)
	var found *os.PathError
	if err.Error() != "fetch failed" || !errors.Is(err, os.ErrPermission) || !errors.As(err, &found) || found != cause {
		t.Fatalf("lost safe message or cause: %v", err)
	}
}
