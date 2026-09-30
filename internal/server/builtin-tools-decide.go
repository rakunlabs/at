package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// execDecide asks a System 1 decision provider typed questions. It is the
// cheap alternative to spending a whole LLM turn on a classification: one
// forward pass, calibrated probabilities, nothing to parse.
func (s *Server) execDecide(ctx context.Context, args map[string]any) (string, error) {
	ref, _ := args["provider"].(string)
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("provider is required (a provider of type systemone, optionally as provider/model)")
	}
	providerKey, model := ref, ""
	if i := strings.Index(ref, "/"); i > 0 {
		providerKey, model = ref[:i], ref[i+1:]
	}
	if m, _ := args["model"].(string); m != "" {
		model = m
	}

	state := args["state"]
	if state == nil {
		return "", fmt.Errorf("state is required")
	}
	questions, err := decideQuestionsArg(args["questions"])
	if err != nil {
		return "", err
	}

	info, err := s.getExecutionProviderInfo(ctx, providerKey)
	if err != nil {
		return "", fmt.Errorf("provider %q: %w", providerKey, err)
	}
	decider, ok := info.provider.(service.DecisionProvider)
	if !ok {
		return "", fmt.Errorf("provider %q does not support decisions; use a provider of type systemone", providerKey)
	}
	if model == "" {
		model = info.defaultModel
	}
	req := service.DecisionRequest{Model: model, State: state, Questions: questions}
	if err := service.ValidateDecisionRequest(req); err != nil {
		return "", err
	}
	resp, err := decider.Decide(ctx, req)
	if err != nil {
		return "", fmt.Errorf("decide: %w", err)
	}

	minConfidence, _ := args["min_confidence"].(float64)
	out := map[string]any{
		"answers":        resp.Answers,
		"model":          resp.Model,
		"low_confidence": service.DecisionLowConfidence(resp.Answers, minConfidence),
	}
	if routing, ok := resp.Raw["routing"]; ok {
		out["routing"] = routing
	}
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode result: %w", err)
	}
	return string(encoded), nil
}

// decideQuestionsArg accepts the question set as an object or as its JSON
// text; models produce both.
func decideQuestionsArg(v any) (map[string]any, error) {
	switch q := v.(type) {
	case map[string]any:
		return q, nil
	case string:
		var out map[string]any
		if err := json.Unmarshal([]byte(q), &out); err != nil {
			return nil, fmt.Errorf("questions must be an object: %w", err)
		}
		return out, nil
	case nil:
		return nil, fmt.Errorf("questions is required")
	default:
		return nil, fmt.Errorf("questions must be an object")
	}
}
