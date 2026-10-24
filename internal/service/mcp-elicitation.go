package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
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

// Elicitation modes. Form mode collects data through the client; URL mode
// (protocol 2025-11-25) sends the person to a page of the server's, so the
// data never passes through AT.
const (
	ElicitModeForm = "form"
	ElicitModeURL  = "url"

	// MCPErrURLElicitationRequired is URLElicitationRequiredError: the
	// request can only be retried after the listed URL elicitations.
	MCPErrURLElicitationRequired = -32042
)

// ElicitRequest is one question from an MCP server.
type ElicitRequest struct {
	// Server is the name the server reported (or its host), shown to the
	// person so they know who is asking.
	Server          string         `json:"server"`
	Mode            string         `json:"mode"`
	Message         string         `json:"message"`
	RequestedSchema map[string]any `json:"requested_schema,omitempty"`
	// URL and ElicitationID are set in URL mode.
	URL           string `json:"url,omitempty"`
	ElicitationID string `json:"elicitation_id,omitempty"`
	// Required marks a URL elicitation the server demanded with -32042: the
	// tool call is retried once the person says they finished it.
	Required bool `json:"required,omitempty"`
}

// ElicitResult is the person's answer.
type ElicitResult struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content,omitempty"`
}

// Elicitor asks the person a question and waits for the answer.
type Elicitor func(ctx context.Context, req ElicitRequest) (ElicitResult, error)

type elicitorKey struct{}

// ElicitationCompleteFunc receives notifications/elicitation/complete: the
// server finished the out-of-band interaction with that elicitation ID.
type ElicitationCompleteFunc func(elicitationID string)

type elicitationCompleteKey struct{}

// CopyElicitationContext copies the completion listener and URL passthrough
// flag of from onto ctx.
func CopyElicitationContext(ctx, from context.Context) context.Context {
	if fn, _ := from.Value(elicitationCompleteKey{}).(ElicitationCompleteFunc); fn != nil {
		ctx = ContextWithElicitationComplete(ctx, fn)
	}
	if from.Value(urlElicitationPassthroughKey{}) != nil {
		ctx = ContextWithURLElicitationPassthrough(ctx)
	}
	return ctx
}

// ContextWithElicitationComplete installs the completion listener.
func ContextWithElicitationComplete(ctx context.Context, fn ElicitationCompleteFunc) context.Context {
	return context.WithValue(ctx, elicitationCompleteKey{}, fn)
}

// handleServerNotification acts on notifications a server sends while a
// request is open. Unknown notifications are ignored.
func handleServerNotification(ctx context.Context, msg mcpServerMessage) {
	if msg.Method != "notifications/elicitation/complete" {
		return
	}
	var params struct {
		ElicitationID string `json:"elicitationId"`
	}
	if json.Unmarshal(msg.Params, &params) != nil || params.ElicitationID == "" {
		return
	}
	if fn, _ := ctx.Value(elicitationCompleteKey{}).(ElicitationCompleteFunc); fn != nil {
		fn(params.ElicitationID)
	}
}

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
	if schema == nil && result.Action == ElicitAccept {
		// URL mode: accept carries no content.
		return ElicitResult{Action: ElicitAccept}, nil
	}
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

func (m mcpServerMessage) isNotification() bool {
	return m.Method != "" && (len(m.ID) == 0 || string(m.ID) == "null")
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
		Mode            string         `json:"mode"`
		Message         string         `json:"message"`
		RequestedSchema map[string]any `json:"requestedSchema"`
		URL             string         `json:"url"`
		ElicitationID   string         `json:"elicitationId"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return ElicitResult{}, fmt.Errorf("invalid elicitation params: %w", err)
	}
	req := ElicitRequest{Server: server, Mode: params.Mode, Message: params.Message}
	switch params.Mode {
	case "", ElicitModeForm:
		if err := ValidateElicitSchema(params.RequestedSchema); err != nil {
			return ElicitResult{}, err
		}
		req.Mode, req.RequestedSchema = ElicitModeForm, params.RequestedSchema
	case ElicitModeURL:
		if err := ValidateElicitURL(params.URL); err != nil {
			return ElicitResult{}, err
		}
		if strings.TrimSpace(params.ElicitationID) == "" {
			return ElicitResult{}, fmt.Errorf("elicitationId is required in url mode")
		}
		req.URL, req.ElicitationID = params.URL, params.ElicitationID
	default:
		return ElicitResult{}, fmt.Errorf("unsupported elicitation mode %q", params.Mode)
	}
	return askPerson(ctx, req), nil
}

// askPerson runs the installed elicitor and turns every failure (nobody to
// ask, timeout, stopped run, an answer that does not fit) into cancel.
func askPerson(ctx context.Context, req ElicitRequest) ElicitResult {
	elicitor := ElicitorFromContext(ctx)
	if elicitor == nil {
		return ElicitResult{Action: ElicitCancel}
	}
	ctx, cancel := context.WithTimeout(ctx, ElicitMaxWait)
	defer cancel()
	req.Message = strings.TrimSpace(req.Message)
	if len(req.Message) > 4000 {
		req.Message = strings.ToValidUTF8(req.Message[:4000], "")
	}
	result, err := elicitor(ctx, req)
	if err != nil {
		return ElicitResult{Action: ElicitCancel}
	}
	normalized, err := NormalizeElicitResult(req.RequestedSchema, result)
	if err != nil {
		return ElicitResult{Action: ElicitCancel}
	}
	return normalized
}

// ValidateElicitURL accepts absolute http(s) URLs without credentials. The
// URL is only shown to the person; AT never fetches it.
func ValidateElicitURL(raw string) error {
	if raw == "" || len(raw) > 4096 {
		return fmt.Errorf("url is required and must be at most 4096 characters")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("url must be an absolute http or https URL")
	}
	if u.User != nil {
		return fmt.Errorf("url must not contain credentials")
	}
	return nil
}

// urlElicitationsRequired extracts the elicitations of a -32042 error.
func urlElicitationsRequired(err error) []ElicitRequest {
	var mcpErr *MCPError
	if !errors.As(err, &mcpErr) || mcpErr.Code != MCPErrURLElicitationRequired || len(mcpErr.Data) == 0 {
		return nil
	}
	var data struct {
		Elicitations []struct {
			Mode          string `json:"mode"`
			ElicitationID string `json:"elicitationId"`
			URL           string `json:"url"`
			Message       string `json:"message"`
		} `json:"elicitations"`
	}
	if json.Unmarshal(mcpErr.Data, &data) != nil {
		return nil
	}
	var out []ElicitRequest
	for _, e := range data.Elicitations {
		if e.Mode != ElicitModeURL || e.ElicitationID == "" || ValidateElicitURL(e.URL) != nil {
			return nil // malformed: report the error as it is
		}
		out = append(out, ElicitRequest{Mode: ElicitModeURL, URL: e.URL, ElicitationID: e.ElicitationID, Message: e.Message, Required: true})
		if len(out) == 8 {
			break
		}
	}
	return out
}

// callToolWithURLElicitation runs call; when the server answers -32042 it
// asks the person to complete each URL and, once they confirm all of them,
// retries the call one time. Declining (or nobody to ask) returns the
// original error.
func callToolWithURLElicitation(ctx context.Context, server string, call func() (string, error)) (string, error) {
	result, err := call()
	required := urlElicitationsRequired(err)
	if len(required) == 0 || ElicitorFromContext(ctx) == nil || ctx.Value(urlElicitationPassthroughKey{}) != nil {
		return result, err
	}
	for _, req := range required {
		req.Server = server
		if askPerson(ctx, req).Action != ElicitAccept {
			return result, err
		}
	}
	return call()
}

type urlElicitationPassthroughKey struct{}

// ContextWithURLElicitationPassthrough leaves -32042 errors to the caller
// (AT's gateway forwards them to its own MCP client, which retries itself)
// instead of asking and retrying here.
func ContextWithURLElicitationPassthrough(ctx context.Context) context.Context {
	return context.WithValue(ctx, urlElicitationPassthroughKey{}, true)
}

// URLElicitationRequiredError returns the -32042 error inside err, if any.
func URLElicitationRequiredError(err error) *MCPError {
	var mcpErr *MCPError
	if errors.As(err, &mcpErr) && mcpErr.Code == MCPErrURLElicitationRequired && len(urlElicitationsRequired(mcpErr)) > 0 {
		return mcpErr
	}
	return nil
}

// mcpClientCapabilities is what AT's MCP clients declare at initialize.
func mcpClientCapabilities() map[string]any {
	return map[string]any{"elicitation": map[string]any{"form": map[string]any{}, "url": map[string]any{}}}
}
