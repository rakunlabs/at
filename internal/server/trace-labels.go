package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/rakunlabs/at/internal/gateway/wire"
	"github.com/rakunlabs/at/internal/service"
)

// traceLabels are optional, client-asserted trace attributes read from
// request headers. They label a trace for filtering; none of them is an
// identity, so x-at-user is stored as the display-only end user and never
// reaches budgets or authorization.
type traceLabels struct {
	name        string
	user        string
	tags        []string
	environment string
	release     string
}

type traceLabelsKey struct{}

// withTraceLabels reads x-at-trace-name, x-at-user, x-at-tags (comma
// separated), x-at-environment and x-at-release. Values pass the same
// correlation-ID checks as trace and session IDs (bounded, no control
// characters); the store additionally bounds tag count and length.
func withTraceLabels(r *http.Request) *http.Request {
	l := traceLabels{
		name:        auditCorrelationID(r.Header.Get("x-at-trace-name")),
		user:        auditCorrelationID(r.Header.Get("x-at-user")),
		environment: auditCorrelationID(r.Header.Get("x-at-environment")),
		release:     auditCorrelationID(r.Header.Get("x-at-release")),
	}
	for _, tag := range strings.Split(auditCorrelationID(r.Header.Get("x-at-tags")), ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			l.tags = append(l.tags, tag)
		}
	}
	if l.name == "" && l.user == "" && l.environment == "" && l.release == "" && len(l.tags) == 0 {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), traceLabelsKey{}, l))
}

func traceLabelsFromContext(ctx context.Context) traceLabels {
	l, _ := ctx.Value(traceLabelsKey{}).(traceLabels)
	return l
}

// traceIOFromBodies extracts the last user message from a request body and
// the assistant text from a response body, for a trace's input/output
// preview. It understands the OpenAI chat, Anthropic Messages and Responses
// shapes plus AT's canonical agent-loop shape; anything else yields "".
func traceIOFromBodies(requestBody, responseBody []byte) (string, string) {
	return lastUserText(requestBody), assistantText(responseBody)
}

func lastUserText(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(body, &req) != nil {
		return ""
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			if text := contentText(req.Messages[i].Content); text != "" {
				return text
			}
		}
	}
	if len(req.Input) > 0 {
		var s string
		if json.Unmarshal(req.Input, &s) == nil {
			return s
		}
		var items []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(req.Input, &items) == nil {
			for i := len(items) - 1; i >= 0; i-- {
				if items[i].Role == "user" {
					if text := contentText(items[i].Content); text != "" {
						return text
					}
				}
			}
		}
		var inputs []string
		if json.Unmarshal(req.Input, &inputs) == nil {
			return strings.Join(inputs, "\n")
		}
	}
	return ""
}

// conversationTurnTraceID derives a stable trace ID for one conversational
// turn of a gateway client that sends a session ID but no trace ID (OpenCode,
// Claude Code, most coding CLIs). An agentic client calls the model once per
// tool step, re-sending the whole history each time; without this every step
// became its own trace. The turn is identified by the number of real user
// messages and the text of the last one: tool results never count as user
// messages, so every step of one turn maps to the same trace, and the next
// prompt starts a new one. The scope (workspace, token) and session are part
// of the hash, so two clients reusing a session ID never share a trace.
// Returns "" when the body carries no user message.
func conversationTurnTraceID(scope, sessionID string, body []byte) string {
	if sessionID == "" {
		return ""
	}
	count, last := userTurns(body)
	if count == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte(scope + "\x00" + sessionID + "\x00" + strconv.Itoa(count) + "\x00" + last))
	return "turn-" + hex.EncodeToString(sum[:16])
}

// userTurns counts the user-authored messages in an OpenAI chat, Anthropic
// Messages or Responses request and returns the text of the last one.
// Messages carrying tool results (Anthropic tool_result blocks) are not
// user turns, even when they also contain text.
func userTurns(body []byte) (int, string) {
	if len(body) == 0 {
		return 0, ""
	}
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(body, &req) != nil {
		return 0, ""
	}
	count, last := 0, ""
	add := func(role string, content json.RawMessage) {
		if role != "user" || hasToolResult(content) {
			return
		}
		if text := contentText(content); text != "" {
			count++
			last = text
		}
	}
	for _, m := range req.Messages {
		add(m.Role, m.Content)
	}
	if count == 0 && len(req.Input) > 0 {
		var s string
		if json.Unmarshal(req.Input, &s) == nil {
			if s != "" {
				return 1, s
			}
			return 0, ""
		}
		var items []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(req.Input, &items) == nil {
			for _, item := range items {
				add(item.Role, item.Content)
			}
		}
	}
	return count, last
}

func hasToolResult(raw json.RawMessage) bool {
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "tool_result" || b.Type == "function_call_output" {
			return true
		}
	}
	return false
}

// gatewayTraceScope namespaces derived trace IDs by workspace and API token.
func gatewayTraceScope(auth *authResult) string {
	if auth == nil || auth.token == nil {
		return ""
	}
	return auth.token.WorkspaceID + "\x00" + auth.token.ID
}

// contentText flattens string or block-array content to its text parts.
// Tool results are not user text and are skipped.
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []map[string]any
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		switch b["type"] {
		case "text", "input_text", "output_text":
			if text, _ := b["text"].(string); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func assistantText(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var openai wire.ChatCompletionResponse
	if json.Unmarshal(body, &openai) == nil && len(openai.Choices) > 0 {
		if c := openai.Choices[0].Message.Content; c != nil {
			return *c
		}
		return ""
	}
	var other struct {
		Content    json.RawMessage `json:"content"`
		Output     json.RawMessage `json:"output"`
		OutputText string          `json:"output_text"`
		// service.LLMResponse marshals with Go field names.
		LLMContent string `json:"Content"`
	}
	if json.Unmarshal(body, &other) != nil {
		return ""
	}
	if other.OutputText != "" {
		return other.OutputText
	}
	if text := contentText(other.Content); text != "" {
		return text
	}
	if len(other.Output) > 0 {
		var items []struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(other.Output, &items) == nil {
			var parts []string
			for _, item := range items {
				if item.Type == "message" {
					if text := contentText(item.Content); text != "" {
						parts = append(parts, text)
					}
				}
			}
			return strings.Join(parts, "\n")
		}
	}
	return other.LLMContent
}

// rootTraceUpdate derives trace-level attributes for an observation that has
// no parent: header labels, a default name, and the input/output preview of a
// root generation. Agent runs supply their own update and are left alone.
func rootTraceUpdate(ctx context.Context, p llmAuditParams, obsType, parentID string) *service.TraceUpdate {
	if p.trace != nil || parentID != "" {
		return p.trace
	}
	labels := traceLabelsFromContext(ctx)
	u := &service.TraceUpdate{Name: labels.name, EndUser: labels.user, Tags: labels.tags}
	if u.EndUser == "" {
		u.EndUser = p.userField
	}
	switch obsType {
	case service.ObservationGeneration, service.ObservationEmbedding:
		if u.Name == "" {
			u.Name = p.name
		}
		if u.Name == "" {
			u.Name = firstNonEmpty(p.requestedModel, p.fullModel)
		}
		u.Input, u.Output = traceIOFromBodies(p.requestBody, p.responseBody)
	case service.ObservationTool, service.ObservationEvent, service.ObservationSpan:
		if u.Name == "" {
			u.Name = p.name
		}
	}
	if u.Empty() {
		return nil
	}
	return u
}
