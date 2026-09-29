package providerutil

import (
	"github.com/fastabc/fastconf/contracts"
	"testing"
)

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
