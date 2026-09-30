package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// decisionNode asks a System 1 decision provider (type "systemone": Laya,
// TypeSafe Jev) typed questions about its input and routes on confidence.
//
// Config (node.Data):
//
//	"provider":       string — provider key (required)
//	"model":          string — checkpoint; empty/"auto" lets the upstream route
//	"questions":      object — {id: {type: choice|score|noul, instructions, criteria}}
//	"min_confidence": number — 0..1; answers below it select "escalate" (0 = off)
//	"max_len":        number — optional upstream token budget for long states
//
// Input ports:  "state" — the text or JSON object the questions are about
// Output ports: "decided" / "escalate" — both carry
//
//	{answers, model, low_confidence: [ids], state, usage}
//
// Downstream nodes read answers with JSON Pointers such as
// /answers/department/choice (e.g. a Switch).
type decisionNode struct {
	providerKey   string
	model         string
	questions     map[string]any
	minConfidence float64
	maxLen        int
}

func init() {
	workflow.RegisterNodeType("decision", newDecisionNode)
	// Like llm_call: a provider call through the scoped execution provider,
	// no host primitive.
	service.RegisterExecutionCapability("node", "decision", false)
}

func newDecisionNode(node service.WorkflowNode) (workflow.Noder, error) {
	n := &decisionNode{}
	n.providerKey, _ = node.Data["provider"].(string)
	n.model, _ = node.Data["model"].(string)
	switch q := node.Data["questions"].(type) {
	case map[string]any:
		n.questions = q
	case string:
		// The editor may persist the JSON text of the question set.
		if q != "" {
			if err := json.Unmarshal([]byte(q), &n.questions); err != nil {
				return nil, fmt.Errorf("decision: questions must be a JSON object: %w", err)
			}
		}
	}
	if v, ok := node.Data["min_confidence"].(float64); ok {
		n.minConfidence = v
	}
	if v, ok := node.Data["max_len"].(float64); ok && v > 0 {
		n.maxLen = int(v)
	}
	return n, nil
}

func (n *decisionNode) Type() string { return "decision" }

func (n *decisionNode) Meta() workflow.NodeMeta {
	return workflow.NodeMeta{
		Type:        "decision",
		Label:       "Decision",
		Category:    "flow_control",
		Description: "Answer typed questions (choice/score/yes-no) with a System 1 decision model and route low-confidence results to escalate",
		Inputs: []workflow.PortMeta{
			{Name: "state", Type: workflow.PortTypeData, Required: true, Accept: []workflow.PortType{workflow.PortTypeText}, Label: "State", Position: "left"},
		},
		Outputs: []workflow.PortMeta{
			{Name: "decided", Type: workflow.PortTypeData, Label: "Decided", Position: "right"},
			{Name: "escalate", Type: workflow.PortTypeData, Label: "Escalate", Position: "right"},
		},
		Fields: []workflow.FieldMeta{
			{Name: "label", Type: "string", Required: true, Description: "Display name"},
			{Name: "provider", Type: "string", Required: true, Description: "Provider key of a systemone provider"},
			{Name: "model", Type: "string", Description: "Checkpoint (auto, english, multilingual, typed-decisions); empty lets the service route"},
			{Name: "questions", Type: "object", Required: true, Description: "{id: {type: choice|score|noul, instructions, criteria}}"},
			{Name: "min_confidence", Type: "number", Description: "0–1. Any answer below it routes to escalate. 0 disables the threshold"},
			{Name: "max_len", Type: "number", Description: "Optional upstream token budget for long states"},
		},
		Color: "violet",
	}
}

func (n *decisionNode) Validate(_ context.Context, reg *workflow.Registry) error {
	if n.providerKey == "" {
		return fmt.Errorf("decision: 'provider' is required")
	}
	if err := service.ValidateDecisionQuestions(n.questions); err != nil {
		return fmt.Errorf("decision: %w", err)
	}
	if n.minConfidence < 0 || n.minConfidence > 1 {
		return fmt.Errorf("decision: min_confidence must be between 0 and 1")
	}
	if reg != nil && reg.ProviderLookup == nil {
		return fmt.Errorf("decision: no provider lookup configured")
	}
	return nil
}

func (n *decisionNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	state, ok := inputs["state"]
	if !ok || state == nil {
		// Accept the generic data/text ports so the node drops into existing
		// graphs without an input mapping.
		if v, ok := inputs["data"]; ok {
			state = v
		} else if v, ok := inputs["text"]; ok {
			state = v
		}
	}
	if state == nil {
		return nil, fmt.Errorf("decision: connect the state input or configure its input mapping")
	}
	if b, ok := state.([]byte); ok {
		state = string(b)
	}

	provider, defaultModel, err := reg.ProviderLookup(n.providerKey)
	if err != nil {
		return nil, fmt.Errorf("decision: provider %q: %w", n.providerKey, err)
	}
	decider, ok := provider.(service.DecisionProvider)
	if !ok {
		return nil, fmt.Errorf("decision: provider %q does not support decisions (use a systemone provider)", n.providerKey)
	}
	model := n.model
	if model == "" {
		model = defaultModel
	}

	req := service.DecisionRequest{Model: model, State: state, Questions: n.questions}
	if n.maxLen > 0 {
		req.Options = map[string]any{"max_len": n.maxLen}
	}
	requestedModel := n.providerKey
	if model != "" {
		requestedModel += "/" + model
	}
	requestBody, _ := json.Marshal(map[string]any{"model": model, "state": state, "questions": n.questions, "options": req.Options})

	started := time.Now()
	resp, err := decider.Decide(ctx, req)
	latencyMs := time.Since(started).Milliseconds()
	if err != nil {
		if reg.RecordObservation != nil {
			reg.RecordObservation(ctx, service.LLMCall{
				ObservationType: service.ObservationGeneration,
				Name:            "decisions", Source: "workflow",
				Provider: n.providerKey, Model: model, RequestedModel: requestedModel,
				RequestBody: string(requestBody), LatencyMs: latencyMs,
				Status: "error", ErrorCode: "decision_error", ErrorMessage: err.Error(), Level: service.ObservationLevelError,
				Metadata: map[string]any{"question_count": len(n.questions)},
			})
		}
		return nil, fmt.Errorf("decision: %w", err)
	}

	low := service.DecisionLowConfidence(resp.Answers, n.minConfidence)
	if reg.RecordObservation != nil {
		responseBody, _ := json.Marshal(resp.Raw)
		reg.RecordObservation(ctx, service.LLMCall{
			ObservationType: service.ObservationGeneration,
			Name:            "decisions", Source: "workflow",
			Provider: n.providerKey, Model: resp.Model, RequestedModel: requestedModel,
			RequestBody: string(requestBody), ResponseBody: string(responseBody),
			InputTokens: int64(resp.Usage.PromptTokens), LatencyMs: latencyMs,
			Status: "ok", FinishReason: "stop",
			Metadata: map[string]any{"question_count": len(n.questions), "low_confidence": low},
		})
	}

	lowAny := make([]any, len(low))
	for i, id := range low {
		lowAny[i] = id
	}
	out := map[string]any{
		"answers":        resp.Answers,
		"model":          resp.Model,
		"low_confidence": lowAny,
		"state":          state,
		"usage": map[string]any{
			"input_tokens":  resp.Usage.PromptTokens,
			"output_tokens": resp.Usage.CompletionTokens,
		},
	}
	if routing, ok := resp.Raw["routing"]; ok {
		out["routing"] = routing
	}
	port := "decided"
	if len(low) > 0 {
		port = "escalate"
	}
	return workflow.NewSelectionResult(map[string]any{port: out}, []string{port}), nil
}
