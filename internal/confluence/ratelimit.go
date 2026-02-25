package confluence

import (
	"context"
	"sync"
	"time"
)

const DefaultRateLimitRPS = 5 // requests per second

// rateLimiter is a simple token-bucket rate limiter backed by a time.Ticker.
// It allows up to rps requests per second by consuming one token per request.
type rateLimiter struct {
	tokens   chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// newRateLimiter creates a rate limiter that allows rps requests per second.
func newRateLimiter(rps int) *rateLimiter {
	if rps <= 0 {
		rps = DefaultRateLimitRPS
	}

	rl := &rateLimiter{
		tokens: make(chan struct{}, rps),
		done:   make(chan struct{}),
	}

	// Pre-fill the bucket so the first burst of rps requests is not delayed.
	for i := 0; i < rps; i++ {
		rl.tokens <- struct{}{}
	}

	ticker := time.NewTicker(time.Second / time.Duration(rps))
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				select {
				case rl.tokens <- struct{}{}:
				default:
					// Bucket is full; discard the tick.
				}
			case <-rl.done:
				return
			}
		}
	}()

	return rl
}

// wait blocks until a token is available or ctx is cancelled.
func (rl *rateLimiter) wait(ctx context.Context) error {
	select {
	case <-rl.tokens:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stop shuts down the background ticker goroutine.
func (rl *rateLimiter) stop() {
	rl.stopOnce.Do(func() {
		close(rl.done)
	})
}
