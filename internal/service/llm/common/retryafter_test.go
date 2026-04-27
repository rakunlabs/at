package common

import (
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfter_Integer(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "30")
	got := ParseRetryAfter(h)
	if got != 30*time.Second {
		t.Fatalf("got %v, want 30s", got)
	}
}

func TestParseRetryAfter_Zero(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "0")
	if got := ParseRetryAfter(h); got != 0 {
		t.Fatalf("got %v, want 0", got)
	}
}

func TestParseRetryAfter_Negative(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "-5")
	if got := ParseRetryAfter(h); got != 0 {
		t.Fatalf("got %v, want 0 for negative", got)
	}
}

func TestParseRetryAfter_HTTPDate(t *testing.T) {
	h := http.Header{}
	future := time.Now().Add(10 * time.Second).UTC()
	h.Set("Retry-After", future.Format(http.TimeFormat))
	got := ParseRetryAfter(h)
	if got < 5*time.Second || got > 12*time.Second {
		t.Fatalf("got %v, expected ~10s", got)
	}
}

func TestParseRetryAfter_PastDate(t *testing.T) {
	h := http.Header{}
	past := time.Now().Add(-10 * time.Second).UTC()
	h.Set("Retry-After", past.Format(http.TimeFormat))
	if got := ParseRetryAfter(h); got != 0 {
		t.Fatalf("got %v, want 0 for past date", got)
	}
}

func TestParseRetryAfter_Missing(t *testing.T) {
	h := http.Header{}
	if got := ParseRetryAfter(h); got != 0 {
		t.Fatalf("got %v, want 0 for missing header", got)
	}
}

func TestParseRetryAfter_Garbage(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "not-a-number")
	if got := ParseRetryAfter(h); got != 0 {
		t.Fatalf("got %v, want 0 for garbage", got)
	}
}
