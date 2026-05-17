package source

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fastabc/fastconf/contracts"
)

// HTTPSource fetches a single URL on every Read, honouring ETag /
// If-None-Match for cheap unchanged responses. The framework's
// global watcher does not poll HTTP — callers that want change
// notifications should pair an HTTPSource with a separate event
// stream provider (e.g. a webhook bridge).
//
// HTTPSource caches the last successful body and ETag; on a 304 the
// cached body is replayed. The content-type hint comes from the
// response Content-Type header (lower-cased, parameter stripped).
type HTTPSource struct {
	url      string
	client   *http.Client
	priority int

	mu       sync.Mutex
	etag     string
	body     []byte
	ctHint   string
}

// NewHTTP constructs an HTTPSource pointing at url. The default
// http.Client has a 10-second timeout; override via WithClient if you
// need long-poll or different transport settings.
func NewHTTP(url string) *HTTPSource {
	return &HTTPSource{
		url:      url,
		client:   &http.Client{Timeout: 10 * time.Second},
		priority: 8500,
	}
}

// WithClient overrides the http.Client used for fetches.
func (h *HTTPSource) WithClient(c *http.Client) *HTTPSource {
	if c != nil {
		h.client = c
	}
	return h
}

// WithPriority overrides the default priority.
func (h *HTTPSource) WithPriority(p int) *HTTPSource { h.priority = p; return h }

// Name implements contracts.Source. Returns "http:<url>".
func (h *HTTPSource) Name() string { return "http:" + h.url }

// Priority implements contracts.Source.
func (h *HTTPSource) Priority() int { return h.priority }

// Read implements contracts.Source. Performs a GET with the cached
// ETag (when present) in If-None-Match. On 304 returns the cached
// payload with the same revision string; on 200 caches and returns
// the fresh payload.
func (h *HTTPSource) Read(ctx context.Context) ([]byte, string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.url, nil)
	if err != nil {
		return nil, "", "", fmt.Errorf("http source: build request: %w", err)
	}
	h.mu.Lock()
	cachedETag := h.etag
	h.mu.Unlock()
	if cachedETag != "" {
		req.Header.Set("If-None-Match", cachedETag)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, "", "", fmt.Errorf("http source: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.body, h.ctHint, h.etag, nil
	case http.StatusOK:
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, "", "", fmt.Errorf("http source: read body: %w", err)
		}
		ct := stripParams(resp.Header.Get("Content-Type"))
		etag := resp.Header.Get("ETag")
		h.mu.Lock()
		h.body = body
		h.etag = etag
		h.ctHint = ct
		h.mu.Unlock()
		return body, ct, etag, nil
	default:
		return nil, "", "", fmt.Errorf("http source: unexpected status %d", resp.StatusCode)
	}
}

// Watch implements contracts.Source. HTTP polling is not provided by
// the Source contract; pair with a dedicated event-stream provider
// (or wrap with a poller) when change notifications are required.
func (h *HTTPSource) Watch(_ context.Context) (<-chan contracts.Event, error) {
	return nil, nil
}

func stripParams(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}
