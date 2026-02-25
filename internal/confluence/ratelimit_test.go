package confluence

import (
	"context"
	"testing"
	"time"
)

func TestRateLimiter_AllowsBurstUpToRPS(t *testing.T) {
	const rps = 5
	rl := newRateLimiter(rps)
	defer rl.stop()

	// The bucket is pre-filled so the first rps requests should not block.
	ctx := context.Background()
	start := time.Now()
	for i := 0; i < rps; i++ {
		if err := rl.wait(ctx); err != nil {
			t.Fatalf("wait() error on request %d: %v", i+1, err)
		}
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("initial burst took %v, want <50ms", elapsed)
	}
}

func TestRateLimiter_ContextCancellationUnblocks(t *testing.T) {
	const rps = 1
	rl := newRateLimiter(rps)
	defer rl.stop()

	ctx := context.Background()

	// Drain the token.
	if err := rl.wait(ctx); err != nil {
		t.Fatalf("first wait() error: %v", err)
	}

	// Now the bucket is empty. Cancel before the next token arrives.
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := rl.wait(cancelCtx)
	if err == nil {
		t.Fatal("wait() expected error from cancelled context, got nil")
	}
}

func TestRateLimiter_ThrottlesRequestsOverTime(t *testing.T) {
	const rps = 10
	rl := newRateLimiter(rps)
	defer rl.stop()

	ctx := context.Background()
	// Drain the pre-filled burst.
	for i := 0; i < rps; i++ {
		_ = rl.wait(ctx)
	}

	// After draining, the next token should arrive within ~2 tick intervals.
	tickInterval := time.Second / time.Duration(rps) // 100ms at rps=10
	timeout := tickInterval * 3
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	if err := rl.wait(waitCtx); err != nil {
		t.Fatalf("wait() after burst drained: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < tickInterval/2 {
		t.Fatalf("token arrived too quickly (%v), expected ~%v", elapsed, tickInterval)
	}
}

func TestRateLimiter_StopIsIdempotent(t *testing.T) {
	rl := newRateLimiter(1)
	rl.stop()
	rl.stop()
}
