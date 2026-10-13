package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestHoldRunReceiptRenewsChecksAndReleasesOnce(t *testing.T) {
	var renewals, checks, releases atomic.Int32
	ctx, release := holdRunReceipt(t.Context(), runReceipt{
		Label:   "test",
		Renew:   func(context.Context) error { renewals.Add(1); return nil },
		Check:   func(context.Context) error { checks.Add(1); return nil },
		Release: func(ctx context.Context) error { releases.Add(1); return ctx.Err() },
	})
	release()
	release()
	if releases.Load() != 1 {
		t.Fatalf("release ran %d times", releases.Load())
	}
	if !errors.Is(ctx.Err(), context.Canceled) || errors.Is(context.Cause(ctx), errRunReceiptLost) {
		t.Fatalf("normal release reported a lost receipt: %v", context.Cause(ctx))
	}
}

func TestHoldRunReceiptCancelsWhenOwnershipIsLost(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for one poll interval")
	}
	lost := errors.New("cancel requested elsewhere")
	ctx, release := holdRunReceipt(t.Context(), runReceipt{
		Label:   "test",
		Renew:   func(context.Context) error { return nil },
		Check:   func(context.Context) error { return lost },
		Release: func(context.Context) error { return nil },
	})
	defer release()
	select {
	case <-ctx.Done():
	case <-time.After(runReceiptPoll + 5*time.Second):
		t.Fatal("lost receipt did not stop the run")
	}
	if !errors.Is(context.Cause(ctx), errRunReceiptLost) {
		t.Fatalf("cause = %v", context.Cause(ctx))
	}
}
