package http

import (
	"context"
	"errors"
	"io"
	nethttp "net/http"
	"strings"
	"testing"
	"time"
)

func TestWatch_ReportsPolledRevision(t *testing.T) {
	client := &pollClient{status: 200, body: `{"value":1}`, etag: "v1"}
	p, err := New("poll", "https://config.invalid", nil, WithClient(client), WithInterval(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	*client = pollClient{status: 200, body: `{"value":2}`, etag: "v2"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	events, err := p.Watch(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		for range events {
		}
	}()
	select {
	case event, ok := <-events:
		if !ok || event.Revision != "v2" {
			t.Fatalf("event revision = %q; want v2", event.Revision)
		}
	case <-ctx.Done():
		t.Fatal("no change event")
	}
	if p.etag != "v1" {
		t.Fatal("watch accepted the polled revision before Load")
	}
}

type pollClient struct {
	status     int
	body, etag string
	err        error
}

func (c *pollClient) Do(*nethttp.Request) (*nethttp.Response, error) {
	if c.err != nil {
		return nil, c.err
	}
	return &nethttp.Response{StatusCode: c.status, Body: io.NopCloser(strings.NewReader(c.body)), Header: nethttp.Header{"Content-Type": []string{"application/json"}, "Etag": []string{c.etag}}}, nil
}

func TestPoll_PreservesAcceptedResponse(t *testing.T) {
	transportErr := errors.New("transport unavailable")
	for _, tc := range []struct {
		name             string
		response         pollClient
		changed, invalid bool
	}{
		{"unchanged body", pollClient{status: 200, body: `{"value":1}`, etag: "v1"}, false, false},
		{"not modified", pollClient{status: 304}, false, false},
		{"changed body", pollClient{status: 200, body: `{"value":2}`, etag: "v1"}, true, false},
		{"changed etag", pollClient{status: 200, body: `{"value":1}`, etag: "v2"}, true, false},
		{"server error", pollClient{status: 500}, false, true},
		{"transport error", pollClient{err: transportErr}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &pollClient{status: 200, body: `{"value":1}`, etag: "v1"}
			p, err := New("poll", "https://config.invalid", nil, WithClient(client))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.Load(context.Background()); err != nil {
				t.Fatal(err)
			}
			hash, etag := p.bodyHash, p.etag
			*client = tc.response
			for range 2 {
				changed, revision, err := p.poll(context.Background())
				if changed != tc.changed || (err != nil) != tc.invalid {
					t.Fatalf("poll=%v, %v; want changed=%v, error=%v", changed, err, tc.changed, tc.invalid)
				}
				if tc.response.err != nil && !errors.Is(err, transportErr) {
					t.Fatalf("poll lost transport cause: %v", err)
				}
				if changed && revision != tc.response.etag {
					t.Fatalf("polled revision = %q; want %q", revision, tc.response.etag)
				}
				if p.bodyHash != hash || p.etag != etag {
					t.Fatal("poll accepted a response before Load")
				}
			}
			if tc.changed {
				if _, err := p.Load(context.Background()); err != nil {
					t.Fatal(err)
				}
				if changed, _, err := p.poll(context.Background()); changed || err != nil {
					t.Fatalf("accepted response still changed: %v, %v", changed, err)
				}
			}
		})
	}
}
