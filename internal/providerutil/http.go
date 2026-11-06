package providerutil

import (
	"net/http"
	"time"
)

// Doer executes an HTTP request.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// HTTPClient keeps credentials and replayable request bodies at their original
// endpoint. A zero timeout permits protocol-specific long polling with context.
func HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
