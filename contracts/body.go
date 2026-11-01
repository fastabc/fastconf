package contracts

import (
	"fmt"
	"io"
	"math"
)

// ReadAllMax reads at most max+1 bytes and rejects oversized documents with ErrTooLarge.
// Non-positive max uses DefaultMaxBodyBytes.
func ReadAllMax(r io.Reader, max int64) ([]byte, error) {
	if max <= 0 {
		max = DefaultMaxBodyBytes
	}
	limit := max
	if limit < math.MaxInt64 {
		limit++
	}
	b, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%w: limit=%d bytes", ErrTooLarge, max)
	}
	return b, nil
}

// SafeError displays only a caller-chosen message while preserving the cause
// for errors.Is/As. SDK errors can contain credentials or document contents.
func SafeError(message string, cause error) error { return &safeError{message, cause} }

type safeError struct {
	message string
	cause   error
}

func (e *safeError) Error() string { return e.message }
func (e *safeError) Unwrap() error { return e.cause }
