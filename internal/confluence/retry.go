package confluence

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultRetryMaxAttempts = 3
	DefaultRetryBaseDelay   = 500 * time.Millisecond
	DefaultRetryMaxDelay    = 30 * time.Second
)

type retryPolicy struct {
	maxAttempts int
	baseDelay   time.Duration
	maxDelay    time.Duration
}

func newRetryPolicy(maxAttempts int, baseDelay, maxDelay time.Duration) retryPolicy {
	if maxAttempts < 0 {
		maxAttempts = 0
	}
	if baseDelay <= 0 {
		baseDelay = DefaultRetryBaseDelay
	}
	if maxDelay <= 0 {
		maxDelay = DefaultRetryMaxDelay
	}
	if maxDelay < baseDelay {
		maxDelay = baseDelay
	}

	return retryPolicy{
		maxAttempts: maxAttempts,
		baseDelay:   baseDelay,
		maxDelay:    maxDelay,
	}
}

func (p retryPolicy) canRetry(attempt int) bool {
	return attempt < p.maxAttempts
}

func (p retryPolicy) shouldRetry(req *http.Request, resp *http.Response, reqErr error, attempt int) bool {
	if !p.canRetry(attempt) || req == nil {
		return false
	}
	if !isRetryableRequest(req) {
		return false
	}

	if reqErr != nil {
		return isRetryableError(reqErr)
	}
	if resp == nil {
		return false
	}

	return isRetryableStatus(resp.StatusCode)
}

func isRetryableRequest(req *http.Request) bool {
	if req == nil {
		return false
	}

	switch strings.ToUpper(strings.TrimSpace(req.Method)) {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions, http.MethodTrace:
		return true
	case http.MethodPost:
		return strings.TrimSpace(req.Header.Get("Idempotency-Key")) != ""
	default:
		return false
	}
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}

	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Timeout() {
			return true
		}
		if urlErr.Err != nil {
			if isRetryableError(urlErr.Err) {
				return true
			}
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
		type temporary interface {
			Temporary() bool
		}
		if t, ok := any(netErr).(temporary); ok && t.Temporary() {
			return true
		}
	}

	lower := strings.ToLower(err.Error())
	for _, token := range []string{
		"connection reset by peer",
		"broken pipe",
		"connection refused",
		"connection aborted",
		"server closed idle connection",
		"http2: server sent goaway",
	} {
		if strings.Contains(lower, token) {
			return true
		}
	}

	return false
}

// isRetryableStatus returns true for status codes that warrant a retry.
func isRetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || (code >= 500 && code < 600)
}

// retryDelay returns how long to wait before the given attempt (1-indexed).
// It respects the Retry-After header when present, otherwise uses exponential
// backoff with full jitter capped at retryMaxDelay.
func (p retryPolicy) retryDelay(attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
				d := time.Duration(secs) * time.Second
				if d > p.maxDelay {
					d = p.maxDelay
				}
				return d
			}
			if t, err := http.ParseTime(ra); err == nil {
				d := time.Until(t)
				if d < 0 {
					d = 0
				}
				if d > p.maxDelay {
					d = p.maxDelay
				}
				return d
			}
		}
	}

	if attempt < 1 {
		attempt = 1
	}

	exp := p.baseDelay
	for i := 1; i < attempt; i++ {
		if exp >= p.maxDelay/2 {
			exp = p.maxDelay
			break
		}
		exp *= 2
	}
	if exp > p.maxDelay {
		exp = p.maxDelay
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
