// Package ratelimit provides a small, dependency-free rate limiter
// that combines a max-concurrency semaphore, a requests-per-minute
// token bucket, and an input-tokens-per-minute weighted bucket.
//
// It is designed for LLM providers where upstream APIs (Anthropic
// Pro/Max, OpenAI tier limits, etc.) enforce both per-minute request
// counts and per-minute token counts on a per-account basis. A single
// Limiter instance is shared by all callers of one provider, ensuring
// no goroutine in the AT process can exceed the configured budget.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/time/rate"
)

// DefaultWaitTimeout is used when Config.WaitTimeout is zero.
const DefaultWaitTimeout = 60 * time.Second

// Config describes the rate-limit behaviour for one provider.
//
// All fields are optional. A zero value disables that particular
// dimension. A Config with all zero fields produces a nil Limiter
// (i.e. no limiting at all).
type Config struct {
	// RequestsPerMinute caps the request rate (token-bucket).
	// 0 = unlimited.
	RequestsPerMinute int

	// InputTokensPerMin caps the weighted input-token rate
	// (token-bucket, weighted by estimated input tokens per call).
	// 0 = unlimited.
	InputTokensPerMin int

	// MaxConcurrent caps the number of in-flight requests.
	// 0 = unlimited.
	MaxConcurrent int

	// WaitTimeout bounds how long Acquire will block waiting for the
	// limiter to permit the call. 0 = use DefaultWaitTimeout.
	WaitTimeout time.Duration
}

// IsZero reports whether the config disables all limiting.
func (c Config) IsZero() bool {
	return c.RequestsPerMinute <= 0 && c.InputTokensPerMin <= 0 && c.MaxConcurrent <= 0
}

// Limiter combines an optional max-concurrency semaphore with
// optional RPM and ITPM token buckets.
//
// A nil *Limiter is valid — calls to Acquire on it are no-ops.
type Limiter struct {
	sem     chan struct{} // nil if MaxConcurrent == 0
	rpm     *rate.Limiter // nil if RequestsPerMinute == 0
	itpm    *rate.Limiter // nil if InputTokensPerMin == 0
	timeout time.Duration
}

// New builds a Limiter from cfg. Returns nil if cfg disables all
// limiting (so callers can keep a nil pointer and skip Acquire on
// the hot path with a single comparison).
func New(cfg Config) *Limiter {
	if cfg.IsZero() {
		return nil
	}
	l := &Limiter{
		timeout: cfg.WaitTimeout,
	}
	if l.timeout <= 0 {
		l.timeout = DefaultWaitTimeout
	}
	if cfg.MaxConcurrent > 0 {
		l.sem = make(chan struct{}, cfg.MaxConcurrent)
	}
	if cfg.RequestsPerMinute > 0 {
		// Per-second rate; burst = RequestsPerMinute (so a fresh limiter
		// allows up to RPM requests in the first minute, then refills at
		// RPM/60 per second).
		l.rpm = rate.NewLimiter(rate.Limit(float64(cfg.RequestsPerMinute)/60.0), cfg.RequestsPerMinute)
	}
	if cfg.InputTokensPerMin > 0 {
		l.itpm = rate.NewLimiter(rate.Limit(float64(cfg.InputTokensPerMin)/60.0), cfg.InputTokensPerMin)
	}
	return l
}

// Acquire blocks until all configured limits permit one call, or
// until the per-call wait timeout elapses (whichever happens first).
//
// estInputTokens is an approximate count of input tokens the call
// will consume; pass 0 if unknown or if InputTokensPerMin is not
// configured. The token budget is consumed up to the bucket's burst
// capacity (large single calls do not deadlock the bucket).
//
// release MUST be called when the request finishes (success or
// failure) to free the concurrency slot. release is idempotent and
// safe to defer. Token-bucket consumption is permanent — release
// only returns the concurrency slot.
//
// If l is nil, Acquire returns immediately with a no-op release.
func (l *Limiter) Acquire(ctx context.Context, estInputTokens int) (release func(), err error) {
	if l == nil {
		return func() {}, nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()

	// 1. RPM bucket — cheap, one token per call.
	if l.rpm != nil {
		if err := l.rpm.Wait(waitCtx); err != nil {
			return nil, &Error{Reason: ReasonRPM, Wait: l.timeout, Underlying: err}
		}
	}

	// 2. ITPM bucket — weighted by est tokens, capped at burst so
	//    single calls larger than the burst don't block forever.
	if l.itpm != nil && estInputTokens > 0 {
		n := estInputTokens
		if max := l.itpm.Burst(); n > max {
			n = max
		}
		if err := l.itpm.WaitN(waitCtx, n); err != nil {
			return nil, &Error{Reason: ReasonITPM, Wait: l.timeout, Underlying: err}
		}
	}

	// 3. Concurrency semaphore.
	if l.sem != nil {
		select {
		case l.sem <- struct{}{}:
			released := false
			return func() {
				if released {
					return
				}
				released = true
				<-l.sem
			}, nil
		case <-waitCtx.Done():
			return nil, &Error{Reason: ReasonConcurrent, Wait: l.timeout, Underlying: waitCtx.Err()}
		}
	}

	return func() {}, nil
}

// Reason identifies which dimension of the limiter caused a wait failure.
type Reason int

const (
	ReasonRPM        Reason = iota // requests-per-minute bucket exhausted
	ReasonITPM                     // input-tokens-per-minute bucket exhausted
	ReasonConcurrent               // max-concurrent semaphore saturated
)

func (r Reason) String() string {
	switch r {
	case ReasonRPM:
		return "rpm"
	case ReasonITPM:
		return "itpm"
	case ReasonConcurrent:
		return "max_concurrent"
	default:
		return "unknown"
	}
}

// Error is returned by Acquire when the wait timeout elapsed before
// the limiter permitted the call. Callers can check errors.Is(err, ctx.Err())
// or use errors.As(err, *ratelimit.Error) to inspect which bucket blocked.
type Error struct {
	Reason     Reason
	Wait       time.Duration
	Underlying error
}

func (e *Error) Error() string {
	return fmt.Sprintf("rate_limit: %s bucket saturated, waited %s: %v",
		e.Reason, e.Wait, e.Underlying)
}

func (e *Error) Unwrap() error { return e.Underlying }
