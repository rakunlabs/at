package server

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service/executiontest"
	"time"
)

func TestResolveBashTimeout(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		deadline time.Duration
		args     map[string]any
		want     time.Duration
		wantErr  bool
	}{
		{name: "unbounded default", want: time.Minute},
		{name: "inherit agent deadline", deadline: time.Hour, want: time.Hour},
		{name: "inherit short deadline", deadline: 20 * time.Second, want: 20 * time.Second},
		{name: "inherited maximum", deadline: 2 * time.Hour, want: time.Hour},
		{name: "explicit shorter", deadline: time.Hour, args: map[string]any{"timeout": float64(30)}, want: 30 * time.Second},
		{name: "explicit long unbounded", args: map[string]any{"timeout": float64(3600)}, want: time.Hour},
		{name: "explicit maximum", args: map[string]any{"timeout": float64(7200)}, want: time.Hour},
		{name: "overflow clamp", args: map[string]any{"timeout": math.MaxFloat64}, want: time.Hour},
		{name: "outer deadline wins", deadline: 20 * time.Second, args: map[string]any{"timeout": float64(3600)}, want: 20 * time.Second},
		{name: "expired", deadline: -time.Second, wantErr: true},
		{name: "expired explicit", deadline: -time.Second, args: map[string]any{"timeout": float64(3600)}, wantErr: true},
		{name: "zero", args: map[string]any{"timeout": float64(0)}, wantErr: true},
		{name: "negative", args: map[string]any{"timeout": float64(-1)}, wantErr: true},
		{name: "fractional", args: map[string]any{"timeout": 1.5}, wantErr: true},
		{name: "nan", args: map[string]any{"timeout": math.NaN()}, wantErr: true},
		{name: "positive infinity", args: map[string]any{"timeout": math.Inf(1)}, wantErr: true},
		{name: "negative infinity", args: map[string]any{"timeout": math.Inf(-1)}, wantErr: true},
		{name: "string", args: map[string]any{"timeout": "3600"}, wantErr: true},
		{name: "null", args: map[string]any{"timeout": nil}, wantErr: true},
		{name: "unsupported alias", args: map[string]any{"timeout_seconds": float64(3600)}, wantErr: true},
		{name: "alias alongside timeout", args: map[string]any{"timeout": float64(30), "timeout_seconds": float64(3600)}, wantErr: true},
		{name: "other unknown fields ignored", args: map[string]any{"unknown": true}, want: time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.deadline != 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, now.Add(tt.deadline))
				defer cancel()
			}
			got, err := resolveBashTimeout(ctx, tt.args, now)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("resolveBashTimeout() = %v, %v; want %v, error=%v", got, err, tt.want, tt.wantErr)
			}
			if tt.deadline < 0 && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected deadline exceeded, got %v", err)
			}
		})
	}
}

func TestExecBashTimeoutValidation(t *testing.T) {
	s := &Server{}
	_, err := s.execBash(executiontest.Context(t), map[string]any{"command": "exit 0", "timeout_seconds": float64(3600)})
	if err == nil || !strings.Contains(err.Error(), "use timeout in seconds") {
		t.Fatalf("expected actionable timeout_seconds error, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.execBash(ctx, map[string]any{"command": "exit 0", "timeout": float64(3600)})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
