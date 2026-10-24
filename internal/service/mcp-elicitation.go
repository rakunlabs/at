package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// MCP elicitation (protocol 2025-06-18): during a request, an MCP server may
// ask the client for structured input from the person ("elicitation/create").
// The client shows the message and a form built from requestedSchema, then
// answers accept (with content), decline or cancel.
//
// Clients find the person through the Elicitor on the call's context. Runs
// without a person (workflows, schedules, org delegation, background runs)
// install none and the request is answered "cancel", so a server never waits
// on nobody.

const (
	ElicitAccept  = "accept"
	ElicitDecline = "decline"
	ElicitCancel  = "cancel"

	// ElicitMaxWait bounds how long one question waits for an answer; the
	// tool call's own deadline applies too.
	ElicitMaxWait = 10 * time.Minute
)

// ElicitRequest is one question from an MCP server.
type ElicitRequest struct {
	// Server is the name the server reported (or its host), shown to the
	// person so they know who is asking.
	Server          string         `json:"server"`
	Message         string         `json:"message"`
	RequestedSchema map[string]any `json:"requested_schema"`
}

// ElicitResult is the person's answer.
type ElicitResult struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content,omitempty"`
}

// Elicitor asks the person a question and waits for the answer.
type Elicitor func(ctx context.Context, req ElicitRequest) (ElicitResult, error)

type elicitorKey struct{}

// ContextWithElicitor installs the way MCP servers reach the person.
func ContextWithElicitor(ctx context.Context, elicitor Elicitor) context.Context {
	return context.WithValue(ctx, elicitorKey{}, elicitor)
}

// ElicitorFromContext returns the installed elicitor, if any.
func ElicitorFromContext(ctx context.Context) Elicitor {
	e, _ := ctx.Value(elicitorKey{}).(Elicitor)
	return e
}

// ValidateElicitSchema checks the restricted schema elicitation allows: an
// object whose properties are primitives (string, number, integer, boolean,
// or a string enum).
func ValidateElicitSchema(schema map[string]any) error {
	if schema == nil {
		return fmt.Errorf("requestedSchema is required")
	}
	if t, _ := schema["type"].(string); t != "object" {
		return fmt.Errorf("requestedSchema must be an object schema")
	}
	props, _ := schema["properties"].(map[string]any)
	if len(props) > 50 {
		return fmt.Errorf("requestedSchema has too many properties")
	}
	for name, raw := range props {
		prop, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("property %q is not a schema", name)
		}
		switch t, _ := prop["type"].(string); t {
		case "string", "number", "integer", "boolean":
		default:
			return fmt.Errorf("property %q has unsupported type %q", name, t)
		}
	}
	return nil
}

// NormalizeElicitResult checks an answer against the schema: accept must
// carry an object of the declared properties with matching types and every
// required one present; decline and cancel carry nothing.
func NormalizeElicitResult(schema map[string]any, result ElicitResult) (ElicitResult, error) {
	switch result.Action {
	case ElicitDecline, ElicitCancel:
		return ElicitResult{Action: result.Action}, nil
	case ElicitAccept:
	default:
		return ElicitResult{}, fmt.Errorf("action must be accept, decline or cancel")
	}
	props, _ := schema["properties"].(map[string]any)
	out := map[string]any{}
	for key, value := range result.Content {
		raw, ok := props[key].(map[string]any)
		if !ok {
			return ElicitResult{}, fmt.Errorf("unknown field %q", key)
		}
		if value == nil {
			continue
		}
		typ, _ := raw["type"].(string)
		switch typ {
		case "string":
			s, ok := value.(string)
			if !ok {
				return ElicitResult{}, fmt.Errorf("%s must be text", key)
			}
			if enum, ok := raw["enum"].([]any); ok && len(enum) > 0 {
				found := false
				for _, e := range enum {
					found = found || e == s
				}
				if !found {
					return ElicitResult{}, fmt.Errorf("%s must be one of the listed values", key)
				}
			}
			out[key] = s
		case "number", "integer":
			n, ok := value.(float64)
			if !ok {
				if num, isNum := value.(json.Number); isNum {
					f, err := num.Float64()
					n, ok = f, err == nil
				}
			}
			if !ok || math.IsNaN(n) || math.IsInf(n, 0) || (typ == "integer" && n != math.Trunc(n)) {
				return ElicitResult{}, fmt.Errorf("%s must be a %s", key, typ)
			}
			out[key] = n
		case "boolean":
			b, ok := value.(bool)
			if !ok {
				return ElicitResult{}, fmt.Errorf("%s must be true or false", key)
			}
			out[key] = b
		}
	}
	required, _ := schema["required"].([]any)
	for _, r := range required {
		name, _ := r.(string)
		if _, ok := out[name]; name != "" && !ok {
			return ElicitResult{}, fmt.Errorf("%s is required", name)
		}
	}
	return ElicitResult{Action: ElicitAccept, Content: out}, nil
}

// mcpServerMessage is any JSON-RPC message an MCP server sends; requests from
// the server carry a method and an id (string or number).
type mcpServerMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *MCPError       `json:"error,omitempty"`
}

func (m mcpServerMessage) isRequest() bool {
	return m.Method != "" && len(m.ID) > 0 && string(m.ID) != "null"
}

// answerServerRequest handles a request the server sent while one of ours
// was in flight and returns the JSON-RPC response to send back.
func answerServerRequest(ctx context.Context, server string, msg mcpServerMessage) []byte {
	reply := map[string]any{"jsonrpc": "2.0", "id": msg.ID}
	switch msg.Method {
	case "ping":
		reply["result"] = map[string]any{}
	case "elicitation/create":
		result, err := elicit(ctx, server, msg.Params)
		if err != nil {
			reply["error"] = map[string]any{"code": -32602, "message": err.Error()}
		} else {
			reply["result"] = result
		}
	default:
		reply["error"] = map[string]any{"code": -32601, "message": "method not supported by this client: " + msg.Method}
	}
	data, _ := json.Marshal(reply)
	return data
}

func elicit(ctx context.Context, server string, raw json.RawMessage) (ElicitResult, error) {
	var params struct {
		Message         string         `json:"message"`
		RequestedSchema map[string]any `json:"requestedSchema"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return ElicitResult{}, fmt.Errorf("invalid elicitation params: %w", err)
	}
	if err := ValidateElicitSchema(params.RequestedSchema); err != nil {
		return ElicitResult{}, err
	}
	elicitor := ElicitorFromContext(ctx)
	if elicitor == nil {
		// Nobody can answer this run.
		return ElicitResult{Action: ElicitCancel}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, ElicitMaxWait)
	defer cancel()
	message := strings.TrimSpace(params.Message)
	if len(message) > 4000 {
		message = message[:4000]
	}
	result, err := elicitor(ctx, ElicitRequest{Server: server, Message: message, RequestedSchema: params.RequestedSchema})
	if err != nil {
		// Timed out, the page left or the run stopped: the person did not answer.
		return ElicitResult{Action: ElicitCancel}, nil
	}
	normalized, err := NormalizeElicitResult(params.RequestedSchema, result)
	if err != nil {
		return ElicitResult{Action: ElicitCancel}, nil
	}
	return normalized, nil
}

// mcpClientCapabilities is what AT's MCP clients declare at initialize.
func mcpClientCapabilities() map[string]any {
	return map[string]any{"elicitation": map[string]any{}}
}
