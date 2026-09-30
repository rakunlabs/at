package service

import (
	"context"
	"fmt"
	"sort"
)

// ─── System 1 decisions ───
//
// A decision model answers typed questions about a state in one forward pass
// instead of generating text: TypeSafe Jev and the open-weight Laya family
// (`laya-serve`) share the `/v1/systemone` wire protocol. The request and
// answer payloads stay loosely typed on purpose — the question vocabulary
// (`choice`, `score`, `noul`) and the per-answer fields belong to the upstream
// protocol, and AT forwards them rather than re-modelling them, so a field an
// upstream adds later reaches the caller without a code change here.

// DecisionProvider is implemented by providers that answer typed decision
// questions (provider type "systemone").
type DecisionProvider interface {
	Decide(ctx context.Context, req DecisionRequest) (*DecisionResponse, error)
}

// DecisionRequest is one System 1 call.
type DecisionRequest struct {
	// Model selects the upstream checkpoint. Empty or "auto" lets the upstream
	// router choose.
	Model string `json:"model,omitempty"`
	// State is the text or JSON document the questions are about.
	State any `json:"state"`
	// Questions maps a caller-chosen ID to a question object
	// ({type, instructions, criteria}).
	Questions map[string]any `json:"questions"`
	// Options carries additional upstream controls (max_len, head_max_len,
	// lang, lang_guess, task, min_confidence, …). They are merged into the
	// request body without interpretation.
	Options map[string]any `json:"-"`
}

// DecisionResponse is the upstream answer. Raw is the complete body so fields
// AT does not model (routing, per-answer distributions) are preserved.
type DecisionResponse struct {
	Model   string         `json:"model,omitempty"`
	Answers map[string]any `json:"answers"`
	Usage   Usage          `json:"-"`
	Raw     map[string]any `json:"-"`
}

// DecisionQuestionTypes are the question types every System 1 upstream
// implements today.
var DecisionQuestionTypes = []string{"choice", "score", "noul"}

// MaxDecisionQuestions bounds one call; laya-serve refuses more than 64.
const MaxDecisionQuestions = 64

// ValidateDecisionQuestions checks the part of a question set every consumer
// relies on: a non-empty map of objects with a known type. Criteria and
// instructions are left to the upstream, which reports its own 422.
func ValidateDecisionQuestions(questions map[string]any) error {
	if len(questions) == 0 {
		return fmt.Errorf("questions must contain at least one question")
	}
	if len(questions) > MaxDecisionQuestions {
		return fmt.Errorf("too many questions (%d > %d)", len(questions), MaxDecisionQuestions)
	}
	for _, id := range sortedKeys(questions) {
		if id == "" {
			return fmt.Errorf("question IDs must be non-empty")
		}
		q, ok := questions[id].(map[string]any)
		if !ok {
			return fmt.Errorf("question %q must be an object", id)
		}
		typ, _ := q["type"].(string)
		known := false
		for _, t := range DecisionQuestionTypes {
			if t == typ {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("question %q: type must be one of choice, score, noul", id)
		}
		if typ == "choice" && q["criteria"] == nil {
			return fmt.Errorf("question %q: choice questions need criteria", id)
		}
	}
	return nil
}

// ValidateDecisionRequest checks a request before it reaches a provider.
func ValidateDecisionRequest(req DecisionRequest) error {
	if req.State == nil {
		return fmt.Errorf("state is required")
	}
	if s, ok := req.State.(string); ok && s == "" {
		return fmt.Errorf("state is required")
	}
	return ValidateDecisionQuestions(req.Questions)
}

// DecisionAnswerConfidence returns the calibrated probability of the reported
// answer. `answer_confidence` is preferred because it is defined for every
// question type; `confidence` (a distribution-concentration measure on
// choice/score) is the fallback for upstreams that do not report it.
func DecisionAnswerConfidence(answer any) (float64, bool) {
	m, ok := answer.(map[string]any)
	if !ok {
		return 0, false
	}
	for _, key := range []string{"answer_confidence", "confidence"} {
		if v, ok := m[key].(float64); ok {
			return v, true
		}
	}
	return 0, false
}

// DecisionLowConfidence lists, in stable order, the answers an upstream
// flagged `low_confidence` or whose confidence is below minConfidence
// (ignored when <= 0). An answer that reports no confidence at all is never
// flagged by the threshold — absent evidence is not low confidence.
func DecisionLowConfidence(answers map[string]any, minConfidence float64) []string {
	var out []string
	for _, id := range sortedKeys(answers) {
		answer := answers[id]
		if m, ok := answer.(map[string]any); ok {
			if flagged, _ := m["low_confidence"].(bool); flagged {
				out = append(out, id)
				continue
			}
		}
		if minConfidence <= 0 {
			continue
		}
		if c, ok := DecisionAnswerConfidence(answer); ok && c < minConfidence {
			out = append(out, id)
		}
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
