package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"time"

	"github.com/rakunlabs/logi"

	"github.com/rakunlabs/at/internal/service"
)

// NodeFailurePort is separate from node-native ports such as HTTP's "error".
const NodeFailurePort = "__error"

type NodeExecutionPolicy struct {
	MaxAttempts  int    `json:"max_attempts"`
	RetryDelayMS int    `json:"retry_delay_ms"`
	Backoff      string `json:"backoff"`
	OnError      string `json:"on_error"`
}

// ParseNodeExecutionPolicy preserves the historical single-attempt/fail-fast
// behavior when no execution settings are present. Invalid settings are rejected
// during graph validation, before any node executes.
func ParseNodeExecutionPolicy(data map[string]any) (NodeExecutionPolicy, error) {
	policy := NodeExecutionPolicy{MaxAttempts: 1, RetryDelayMS: 1000, Backoff: "fixed", OnError: "stop"}
	if data["execution"] == nil {
		return policy, nil
	}
	encoded, err := json.Marshal(data["execution"])
	if err != nil {
		return policy, fmt.Errorf("execution settings: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return policy, fmt.Errorf("execution settings: %w", err)
	}
	if policy.MaxAttempts < 1 || policy.MaxAttempts > 5 {
		return policy, fmt.Errorf("execution.max_attempts must be between 1 and 5 (including the first attempt)")
	}
	if policy.RetryDelayMS < 0 || policy.RetryDelayMS > 30000 {
		return policy, fmt.Errorf("execution.retry_delay_ms must be between 0 and 30000")
	}
	if policy.Backoff != "fixed" && policy.Backoff != "exponential" {
		return policy, fmt.Errorf("execution.backoff must be fixed or exponential")
	}
	if policy.OnError != "stop" && policy.OnError != "continue" && policy.OnError != "error_output" {
		return policy, fmt.Errorf("execution.on_error must be stop, continue or error_output")
	}
	return policy, nil
}

func RetryableHTTPStatus(status int) bool {
	return status == 408 || status == 429 || (status >= 500 && status <= 599 && status != 501)
}

func retryableNodeError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, ErrStopBranch) || errors.Is(err, service.ErrAccessDenied) || errors.Is(err, service.ErrExecutionDenied) {
		return false
	}
	var rateLimit *service.RateLimitError
	if errors.As(err, &rateLimit) {
		return rateLimit.StatusCode == 0 || RetryableHTTPStatus(rateLimit.StatusCode)
	}
	var upstream *service.UpstreamError
	if errors.As(err, &upstream) {
		return RetryableHTTPStatus(upstream.StatusCode)
	}
	var network net.Error
	if errors.As(err, &network) && (network.Timeout() || network.Temporary()) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE)
}

func retryDelay(policy NodeExecutionPolicy, attempt int, err error) (time.Duration, bool) {
	delay := time.Duration(policy.RetryDelayMS) * time.Millisecond
	if policy.Backoff == "exponential" {
		delay *= 1 << (attempt - 1)
	}
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	var rateLimit *service.RateLimitError
	suggested := time.Duration(0)
	if errors.As(err, &rateLimit) {
		suggested = rateLimit.RetryAfter
	}
	var hint interface{ RetryDelay() time.Duration }
	if errors.As(err, &hint) && hint.RetryDelay() > suggested {
		suggested = hint.RetryDelay()
	}
	if suggested > delay {
		// Do not retry earlier than upstream requested. An excessive suggested
		// delay ends retries rather than keeping a worker waiting indefinitely.
		if suggested > 5*time.Minute {
			return 0, false
		}
		delay = suggested
	}
	return delay, true
}

func (e *Engine) executeAttempts(ctx context.Context, st *nodeState, reg *Registry, inputs map[string]any, executionID string, policy NodeExecutionPolicy) (NodeResult, int, error) {
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, attempt - 1, err
		}
		if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "node", Name: st.noder.Type()}); err != nil {
			return nil, attempt - 1, err
		}
		if policy.MaxAttempts > 1 {
			e.emitEvent(NodeEvent{ExecutionID: executionID, NodeID: st.node.ID, NodeType: st.noder.Type(), EventType: "attempt_started", Attempt: attempt, MaxAttempts: policy.MaxAttempts})
		}
		started := time.Now()
		result, err := st.noder.Run(ctx, reg, inputs)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if err == nil {
			return result, attempt, nil
		}
		if errors.Is(err, ErrStopBranch) {
			return nil, attempt, err
		}
		if policy.MaxAttempts > 1 {
			e.emitEvent(NodeEvent{ExecutionID: executionID, NodeID: st.node.ID, NodeType: st.noder.Type(), EventType: "attempt_failed", Attempt: attempt, MaxAttempts: policy.MaxAttempts, Error: err.Error(), DurationMs: time.Since(started).Milliseconds()})
		}
		logi.Ctx(ctx).Warn("workflow node attempt failed", "node_id", st.node.ID, "execution_id", executionID, "attempt", attempt, "error", err.Error())
		if ctx.Err() != nil || attempt == policy.MaxAttempts || !retryableNodeError(err) {
			return nil, attempt, err
		}
		delay, retry := retryDelay(policy, attempt, err)
		if !retry {
			return nil, attempt, err
		}
		e.emitEvent(NodeEvent{ExecutionID: executionID, NodeID: st.node.ID, NodeType: st.noder.Type(), EventType: "retrying", Attempt: attempt, MaxAttempts: policy.MaxAttempts, RetryDelayMS: delay.Milliseconds(), Error: err.Error()})
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, attempt, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, 0, fmt.Errorf("execution attempts exhausted")
}

func nodeFailureData(st *nodeState, inputs map[string]any, attempts int, err error) map[string]any {
	failure := map[string]any{"node_id": st.node.ID, "node_type": st.noder.Type(), "message": err.Error(), "attempts": attempts, "input": inputs}
	var upstream *service.UpstreamError
	var rateLimit *service.RateLimitError
	if errors.As(err, &upstream) {
		failure["status_code"] = upstream.StatusCode
	}
	if errors.As(err, &rateLimit) {
		failure["status_code"] = rateLimit.StatusCode
	}
	return map[string]any{NodeFailurePort: failure}
}
