package confluence

import (
	"context"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

const (
	maxRetryAttempts = 3
	retryBaseDelay   = 500 * time.Millisecond
	retryMaxDelay    = 30 * time.Second
)

// isRetryableStatus returns true for status codes that warrant a retry.
func isRetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || (code >= 500 && code < 600)
}

// retryDelay returns how long to wait before the given attempt (1-indexed).
// It respects the Retry-After header when present, otherwise uses exponential
// backoff with full jitter capped at retryMaxDelay.
func retryDelay(attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			// Retry-After may be a delay in seconds or an HTTP-date.
			if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
				d := time.Duration(secs) * time.Second
				if d > retryMaxDelay {
					d = retryMaxDelay
				}
				return d
			}
			if t, err := http.ParseTime(ra); err == nil {
				d := time.Until(t)
				if d < 0 {
					d = 0
				}
				if d > retryMaxDelay {
					d = retryMaxDelay
				}
				return d
			}
		}
	}

	// Exponential backoff with full jitter: rand(0, base * 2^attempt)
	if attempt < 1 {
		attempt = 1
	}

	exp := retryBaseDelay
	for i := 1; i < attempt; i++ {
		if exp >= retryMaxDelay/2 {
			exp = retryMaxDelay
			break
		}
		exp *= 2
	}
	if exp > retryMaxDelay {
		exp = retryMaxDelay
	}
	//nolint:gosec // jitter does not need cryptographic randomness
	jitter := time.Duration(rand.Int63n(int64(exp) + 1))
	return jitter
}

// contextSleep sleeps for d or until ctx is done.
func contextSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
