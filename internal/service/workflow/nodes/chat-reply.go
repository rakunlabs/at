package nodes

import (
	"context"
	"fmt"

	"github.com/rytsh/mugo/templatex"

	"github.com/rakunlabs/at/internal/render"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// chatReplyNode sends a message to a chat session. This allows workflows
// (e.g. cron-triggered) to push results into an ongoing conversation.
//
// Config (node.Data):
//
//	"session_id": string — the target chat session ID (supports Go template)
//	"role":       string — message role, defaults to "assistant"
//
// Input ports:
//
//	"message" — the text content to send (string)
//	"data"    — upstream data available as template context for session_id
//
// Output ports (selection-based):
//
//	"success" — activated on successful message creation
//	"error"   — activated on failure
//	"always"  — always activated
type chatReplyNode struct {
	sessionIDTmpl string
	role          string
}

func init() {
	workflow.RegisterNodeType("chat_reply", newChatReplyNode)
}

func newChatReplyNode(node service.WorkflowNode) (workflow.Noder, error) {
	sessionID, _ := node.Data["session_id"].(string)
	role, _ := node.Data["role"].(string)
	if role == "" {
		role = "assistant"
	}

	return &chatReplyNode{
		sessionIDTmpl: sessionID,
		role:          role,
	}, nil
}

func (n *chatReplyNode) Type() string { return "chat_reply" }

func (n *chatReplyNode) Validate(_ context.Context, reg *workflow.Registry) error {
	if n.sessionIDTmpl == "" {
		return fmt.Errorf("chat_reply: 'session_id' is required")
	}
	if reg.ChatMessageCreator == nil {
		return fmt.Errorf("chat_reply: chat session store not configured")
	}
	return nil
}

func (n *chatReplyNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	// Build template context from inputs.
	tmplCtx := buildTemplateContext(inputs)

	// Resolve session ID (may be a Go template).
	sessionID, err := renderChatReplyTemplate("session_id", n.sessionIDTmpl, tmplCtx, varFuncMap(reg))
	if err != nil {
		return workflow.NewSelectionResult(
			map[string]any{"error": err.Error(), "status": "failed"},
			[]string{"always", "error"},
		), nil
	}
	if sessionID == "" {
		return workflow.NewSelectionResult(
			map[string]any{"error": "session_id resolved to empty string", "status": "failed"},
			[]string{"always", "error"},
		), nil
	}

	// Verify the session exists.
	if reg.ChatSessionLookup != nil {
		session, err := reg.ChatSessionLookup(ctx, sessionID)
		if err != nil {
			return workflow.NewSelectionResult(
				map[string]any{"error": fmt.Sprintf("lookup session %q: %v", sessionID, err), "status": "failed"},
				[]string{"always", "error"},
			), nil
		}
		if session == nil {
			return workflow.NewSelectionResult(
				map[string]any{"error": fmt.Sprintf("session %q not found", sessionID), "status": "failed"},
				[]string{"always", "error"},
			), nil
		}
	}

	// Extract the message content.
	message := extractMessageContent(inputs)
	if message == "" {
		return workflow.NewSelectionResult(
			map[string]any{"error": "no message content provided", "status": "failed"},
			[]string{"always", "error"},
		), nil
	}

	// Create the message in the chat session.
	if err := reg.ChatMessageCreator(ctx, sessionID, n.role, message); err != nil {
		return workflow.NewSelectionResult(
			map[string]any{"error": fmt.Sprintf("create message: %v", err), "status": "failed"},
			[]string{"always", "error"},
		), nil
	}

	return workflow.NewSelectionResult(
		map[string]any{
			"status":     "sent",
			"session_id": sessionID,
			"role":       n.role,
		},
		[]string{"always", "success"},
	), nil
}

// extractMessageContent pulls the message text from inputs.
// It checks for "message" key first, then "text", then "data",
// then "response" to be flexible with upstream node outputs.
func extractMessageContent(inputs map[string]any) string {
	// Direct "message" input port.
	if msg, ok := inputs["message"].(string); ok && msg != "" {
		return msg
	}

	// "text" from a template node.
	if text, ok := inputs["text"].(string); ok && text != "" {
		return text
	}

	// "response" from an llm_call node.
	if resp, ok := inputs["response"].(string); ok && resp != "" {
		return resp
	}

	// "data" might be a string directly.
	if data, ok := inputs["data"].(string); ok && data != "" {
		return data
	}

	// "data" might be a map with "content" or "text".
	if data, ok := inputs["data"].(map[string]any); ok {
		if content, ok := data["content"].(string); ok && content != "" {
			return content
		}
		if text, ok := data["text"].(string); ok && text != "" {
			return text
		}
		if response, ok := data["response"].(string); ok && response != "" {
			return response
		}
	}

	return ""
}

// renderChatReplyTemplate renders a Go text/template string.
func renderChatReplyTemplate(name, tmplText string, ctx map[string]any, funcs map[string]any) (string, error) {
	if tmplText == "" {
		return "", nil
	}
	result, err := render.ExecuteWithData(tmplText, ctx, templatex.WithExecFuncMap(funcs))
	if err != nil {
		return "", fmt.Errorf("chat_reply: template %q: %w", name, err)
	}
	return string(result), nil
}
