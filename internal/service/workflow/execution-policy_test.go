package workflow

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
)

type retryTestNode struct {
	calls int
	run   func(context.Context, int) (NodeResult, error)
}

func (*retryTestNode) Type() string                              { return characterizationNodeType }
func (*retryTestNode) Validate(context.Context, *Registry) error { return nil }
func (n *retryTestNode) Run(ctx context.Context, _ *Registry, _ map[string]any) (NodeResult, error) {
	n.calls++
	return n.run(ctx, n.calls)
}

func TestNodeExecutionPolicyDefaultsAndValidation(t *testing.T) {
	policy, err := ParseNodeExecutionPolicy(nil)
	if err != nil || policy.MaxAttempts != 1 || policy.OnError != "stop" {
		t.Fatalf("historical defaults changed: %+v %v", policy, err)
	}
	for _, invalid := range []any{
		"bad", []any{}, map[string]any{"max_attempts": 0}, map[string]any{"max_attempts": 6},
		map[string]any{"max_attempts": 2.5}, map[string]any{"retry_delay_ms": -1},
		map[string]any{"retry_delay_ms": 30001}, map[string]any{"on_error": "ignore"},
		map[string]any{"backoff": "random"}, map[string]any{"max_attempt": 3},
	} {
		if _, err := ParseNodeExecutionPolicy(map[string]any{"execution": invalid}); err == nil {
			t.Errorf("accepted invalid policy: %#v", invalid)
		}
	}
}

func TestNodeRetriesTransientFailuresAndCorrelatesAttempts(t *testing.T) {
	node := &retryTestNode{run: func(_ context.Context, attempt int) (NodeResult, error) {
		if attempt < 3 {
			return nil, &service.UpstreamError{StatusCode: 503}
		}
		return NewResult(map[string]any{"value": "ok"}), nil
	}}
	st := &nodeState{node: service.WorkflowNode{ID: "retry", Data: map[string]any{"execution": map[string]any{"max_attempts": 3, "retry_delay_ms": 0}}}, noder: node}
	e := NewEngineWithDependencies(Dependencies{})
	events := make(chan NodeEvent, 32)
	e.SetEventChannel(events)
	result, stopped, err := e.executeNode(executiontest.Context(t), st, &Registry{}, nil, func(map[string]any, error) {})
	if err != nil || stopped || node.calls != 3 || result.Data()["value"] != "ok" {
		t.Fatalf("result=%v stopped=%v error=%v calls=%d", result, stopped, err, node.calls)
	}
	counts := map[string]int{}
	id := ""
	for len(events) > 0 {
		event := <-events
		counts[event.EventType]++
		if id == "" {
			id = event.ExecutionID
		}
		if id == "" || event.ExecutionID != id {
			t.Fatal("attempts were confused with separate invocations")
		}
		if event.EventType == "completed" && event.Attempt != 3 {
			t.Fatalf("wrong final attempt: %d", event.Attempt)
		}
	}
	if counts["started"] != 1 || counts["attempt_started"] != 3 || counts["attempt_failed"] != 2 || counts["retrying"] != 2 || counts["completed"] != 1 {
		t.Fatalf("events: %v", counts)
	}
}

func TestNodeRetriesDoNotRetryPermanentOrAuthorityFailures(t *testing.T) {
	for _, failure := range []error{errors.New("invalid data"), &service.UpstreamError{StatusCode: 400}, &service.UpstreamError{StatusCode: 401}, fmt.Errorf("denied: %w", service.ErrExecutionDenied), errors.Join(service.ErrAccessDenied, &service.UpstreamError{StatusCode: 503}), context.Canceled} {
		node := &retryTestNode{run: func(context.Context, int) (NodeResult, error) { return nil, failure }}
		st := &nodeState{node: service.WorkflowNode{ID: "test", Data: map[string]any{"execution": map[string]any{"max_attempts": 5, "retry_delay_ms": 0}}}, noder: node}
		_, _, err := NewEngineWithDependencies(Dependencies{}).executeNode(executiontest.Context(t), st, &Registry{}, nil, func(map[string]any, error) {})
		if !errors.Is(err, failure) || node.calls != 1 {
			t.Fatalf("error=%v, calls=%d for %v", err, node.calls, failure)
		}
	}
}

func TestNodeErrorPoliciesNeverSwallowCancellationOrAuthorization(t *testing.T) {
	for _, mode := range []string{"continue", "error_output"} {
		for _, failure := range []error{service.ErrExecutionDenied, service.ErrAccessDenied, context.Canceled} {
			node := &retryTestNode{run: func(context.Context, int) (NodeResult, error) { return nil, failure }}
			st := &nodeState{node: service.WorkflowNode{ID: "test", Data: map[string]any{"execution": map[string]any{"on_error": mode}}}, noder: node}
			if _, _, err := NewEngineWithDependencies(Dependencies{}).executeNode(executiontest.Context(t), st, &Registry{}, nil, func(map[string]any, error) {}); !errors.Is(err, failure) {
				t.Fatalf("%s swallowed %v: %v", mode, failure, err)
			}
		}
	}
}

func TestNodeRetryWaitIsCancellationAware(t *testing.T) {
	ctx, cancel := context.WithCancel(executiontest.Context(t))
	defer cancel()
	node := &retryTestNode{run: func(context.Context, int) (NodeResult, error) { return nil, &service.UpstreamError{StatusCode: 503} }}
	st := &nodeState{node: service.WorkflowNode{ID: "test", Data: map[string]any{"execution": map[string]any{"max_attempts": 5, "retry_delay_ms": 30000, "on_error": "error_output"}}}, noder: node}
	e := NewEngineWithDependencies(Dependencies{})
	events := make(chan NodeEvent, 32)
	e.SetEventChannel(events)
	done := make(chan error, 1)
	go func() {
		_, _, err := e.executeNode(ctx, st, &Registry{}, nil, func(map[string]any, error) {})
		done <- err
	}()
	for {
		select {
		case event := <-events:
			if event.EventType == "retrying" {
				cancel()
				goto cancelled
			}
		case <-time.After(2 * time.Second):
			t.Fatal("retry did not enter wait")
		}
	}
cancelled:
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || node.calls != 1 {
			t.Fatalf("err=%v calls=%d", err, node.calls)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not interrupt retry wait")
	}
}

func TestNodeRetryBackoffAndRetryAfter(t *testing.T) {
	p := NodeExecutionPolicy{RetryDelayMS: 10000, Backoff: "exponential"}
	if delay, ok := retryDelay(p, 4, errors.New("x")); !ok || delay != 30*time.Second {
		t.Fatalf("backoff not capped: %v %v", delay, ok)
	}
	if delay, ok := retryDelay(p, 1, &service.RateLimitError{RetryAfter: time.Minute}); !ok || delay != time.Minute {
		t.Fatalf("Retry-After not honored: %v %v", delay, ok)
	}
	if _, ok := retryDelay(p, 1, &service.RateLimitError{RetryAfter: time.Hour}); ok {
		t.Fatal("excessive Retry-After retried too early")
	}
}

func TestNodePolicyPreservesSingleAttemptDefaultAndBranchStop(t *testing.T) {
	node := &retryTestNode{run: func(context.Context, int) (NodeResult, error) { return nil, &service.UpstreamError{StatusCode: 503} }}
	st := &nodeState{node: service.WorkflowNode{ID: "n"}, noder: node}
	e := NewEngineWithDependencies(Dependencies{})
	if _, _, err := e.executeNode(executiontest.Context(t), st, &Registry{}, nil, func(map[string]any, error) {}); err == nil || node.calls != 1 {
		t.Fatalf("default unexpectedly retried: calls=%d error=%v", node.calls, err)
	}
	node.calls = 0
	node.run = func(context.Context, int) (NodeResult, error) { return nil, ErrStopBranch }
	st.node.Data = map[string]any{"execution": map[string]any{"max_attempts": 5, "retry_delay_ms": 0, "on_error": "error_output"}}
	if _, stopped, err := e.executeNode(executiontest.Context(t), st, &Registry{}, nil, func(map[string]any, error) {}); err != nil || !stopped || node.calls != 1 {
		t.Fatalf("branch stop was retried/handled: stopped=%v calls=%d error=%v", stopped, node.calls, err)
	}
}
