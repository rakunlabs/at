package ratelimit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNew_ZeroConfigReturnsNil(t *testing.T) {
	l := New(Config{})
	if l != nil {
		t.Fatalf("expected nil limiter for zero config, got %#v", l)
	}
}

func TestAcquire_NilLimiterIsNoop(t *testing.T) {
	var l *Limiter
	release, err := l.Acquire(context.Background(), 100)
	if err != nil {
		t.Fatalf("nil limiter Acquire: %v", err)
	}
	if release == nil {
		t.Fatal("expected non-nil release for nil limiter")
	}
	release() // must not panic
}

func TestAcquire_MaxConcurrentSerializes(t *testing.T) {
	l := New(Config{MaxConcurrent: 2, WaitTimeout: time.Second})
	if l == nil {
		t.Fatal("expected limiter")
	}

	var inflight atomic.Int32
	var maxSeen atomic.Int32

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := l.Acquire(context.Background(), 0)
			if err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			defer release()
			n := inflight.Add(1)
			defer inflight.Add(-1)
			for {
				cur := maxSeen.Load()
				if n <= cur || maxSeen.CompareAndSwap(cur, n) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
		}()
	}
	wg.Wait()

	if got := maxSeen.Load(); got > 2 {
		t.Fatalf("max in-flight %d exceeded MaxConcurrent=2", got)
	}
}

func TestAcquire_MaxConcurrentTimeout(t *testing.T) {
	l := New(Config{MaxConcurrent: 1, WaitTimeout: 50 * time.Millisecond})
	// Hold the only slot.
	rel1, err := l.Acquire(context.Background(), 0)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer rel1()

	// Second Acquire must time out.
	start := time.Now()
	_, err = l.Acquire(context.Background(), 0)
	dur := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	var rle *Error
	if !errors.As(err, &rle) || rle.Reason != ReasonConcurrent {
		t.Fatalf("expected ReasonConcurrent error, got %v", err)
	}
	if dur < 40*time.Millisecond {
		t.Fatalf("expected to wait ~50ms, waited %s", dur)
	}
}

func TestAcquire_RPMBucketBlocks(t *testing.T) {
	// 60 RPM = 1 per second. With burst=60, we can do 60 immediately,
	// then must wait. Use a tiny wait timeout to verify that's
	// surfaced as an error when we exceed the burst.
	l := New(Config{RequestsPerMinute: 60, WaitTimeout: 10 * time.Millisecond})

	// Drain the bucket.
	for i := 0; i < 60; i++ {
		release, err := l.Acquire(context.Background(), 0)
		if err != nil {
			t.Fatalf("burst Acquire %d: %v", i, err)
		}
		release()
	}

	// 61st should fail (refill rate is 1/s, wait timeout is 10ms).
	_, err := l.Acquire(context.Background(), 0)
	if err == nil {
		t.Fatal("expected RPM bucket exhaustion, got nil")
	}
	var rle *Error
	if !errors.As(err, &rle) || rle.Reason != ReasonRPM {
		t.Fatalf("expected ReasonRPM error, got %v", err)
	}
}

func TestAcquire_ITPMWeighted(t *testing.T) {
	// 6000 tokens/min = 100/sec, burst 6000.
	// First call asks for 5000 — fits.
	// Second call asks for 2000 — must wait (only 1000 left).
	l := New(Config{InputTokensPerMin: 6000, WaitTimeout: 10 * time.Millisecond})

	rel1, err := l.Acquire(context.Background(), 5000)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	rel1()

	_, err = l.Acquire(context.Background(), 2000)
	if err == nil {
		t.Fatal("expected ITPM bucket exhaustion, got nil")
	}
	var rle *Error
	if !errors.As(err, &rle) || rle.Reason != ReasonITPM {
		t.Fatalf("expected ReasonITPM error, got %v", err)
	}
}

func TestAcquire_ITPMOversizeRequestCappedAtBurst(t *testing.T) {
	// 6000 tokens/min, burst 6000.
	// A request asking for 100000 tokens must NOT block forever — it
	// gets capped to the burst size and consumes the full bucket.
	l := New(Config{InputTokensPerMin: 6000, WaitTimeout: 100 * time.Millisecond})

	rel, err := l.Acquire(context.Background(), 100000)
	if err != nil {
		t.Fatalf("oversize Acquire should succeed (capped): %v", err)
	}
	rel()
}

func TestAcquire_DefaultWaitTimeout(t *testing.T) {
	l := New(Config{MaxConcurrent: 1})
	if l.timeout != DefaultWaitTimeout {
		t.Fatalf("expected default %s timeout, got %s", DefaultWaitTimeout, l.timeout)
	}
}

func TestAcquire_ContextCancelled(t *testing.T) {
	l := New(Config{MaxConcurrent: 1, WaitTimeout: time.Hour})
	rel, _ := l.Acquire(context.Background(), 0)
	defer rel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	_, err := l.Acquire(ctx, 0)
	if err == nil {
		t.Fatal("expected error from cancelled ctx")
	}
}

func TestRelease_Idempotent(t *testing.T) {
	l := New(Config{MaxConcurrent: 1, WaitTimeout: 50 * time.Millisecond})
	release, err := l.Acquire(context.Background(), 0)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()
	release() // second call should not panic and should not over-release

	// Third Acquire must succeed (slot is free).
	rel2, err := l.Acquire(context.Background(), 0)
	if err != nil {
		t.Fatalf("re-Acquire after double-release: %v", err)
	}
	rel2()
}

func TestReason_String(t *testing.T) {
	cases := []struct {
		r    Reason
		want string
	}{
		{ReasonRPM, "rpm"},
		{ReasonITPM, "itpm"},
		{ReasonConcurrent, "max_concurrent"},
		{Reason(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.r.String(); got != c.want {
			t.Errorf("Reason(%d).String() = %q, want %q", c.r, got, c.want)
		}
	}
}
