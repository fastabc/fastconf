package providerutil

import (
	"errors"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/fastabc/fastconf/contracts"
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
			if !errors.Is(err, contracts.ErrTooLarge) {
				t.Fatalf("limit=%d: error=%v; want ErrTooLarge", tc.limit, err)
			}
		} else if err != nil || string(body) != "abc" {
			t.Fatalf("limit=%d: body=%q, error=%v", tc.limit, body, err)
		}
	}
}

func TestLatestAndOffer(t *testing.T) {
	var latest Latest
	if snap := latest.Snapshot(); !snap.Stale || snap.Map == nil {
		t.Fatalf("initial snapshot: %#v", snap)
	}
	latest.Store(map[string]any{"value": 1}, "rev")
	snap := latest.Snapshot()
	snap.Map["value"] = 99
	if got := latest.Snapshot(); got.Stale || got.Revision != "rev" || got.Map["value"] != 1 {
		t.Fatalf("snapshot: %#v", got)
	}
	latest.Store(map[string]any{"value": 2}, "")
	if latest.Snapshot().Revision != "rev" {
		t.Fatal("empty revision replaced resume token")
	}
	ch := make(chan contracts.Event, 1)
	if !Offer(ch, contracts.Event{Source: "first"}) || Offer(ch, contracts.Event{Source: "second"}) {
		t.Fatal("offer should drop only when full")
	}
	if (<-ch).Source != "first" {
		t.Fatal("queued event was replaced")
	}
}

func TestSafeErrorPreservesCause(t *testing.T) {
	cause := &os.PathError{Op: "read", Path: "credential-canary", Err: os.ErrPermission}
	err := SafeError("provider read failed", cause)
	var found *os.PathError
	if err.Error() != "provider read failed" || !errors.Is(err, cause) || !errors.As(err, &found) || found != cause {
		t.Fatalf("lost safe message or cause: %v", err)
	}
}
