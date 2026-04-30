package openaicompatible

import (
	"encoding/base64"
	"fmt"
)

// Many ChatRequest fields are pointers to distinguish "unset" from a literal
// zero value. Use Go 1.26's value form of the built-in new for that:
//
//	req := &ChatRequest{
//	    Temperature: new(0.2),
//	    MaxTokens:   new(120),
//	    Seed:        new(42),
//	}

// ─── Message constructors ─────────────────────────────────────────────────

// SystemMessage builds a role="system" message with plain-text content.
func SystemMessage(text string) Message {
	return Message{Role: RoleSystem, Content: text}
}

// DeveloperMessage builds a role="developer" message (OpenAI o-series and
// gpt-4.1+ models). Servers that don't recognise the role typically treat
// it as system.
func DeveloperMessage(text string) Message {
	return Message{Role: RoleDeveloper, Content: text}
}

// UserMessage builds a role="user" message with plain-text content.
func UserMessage(text string) Message {
	return Message{Role: RoleUser, Content: text}
}

// UserMessageParts builds a role="user" message with multimodal content.
// Use [TextPart], [ImageURLPart], [ImageDataPart], [InputAudioPart],
// [FilePart] to construct each part.
func UserMessageParts(parts ...ContentPart) Message {
	return Message{Role: RoleUser, Content: parts}
}

// AssistantMessage builds a role="assistant" message with plain-text content.
func AssistantMessage(text string) Message {
	return Message{Role: RoleAssistant, Content: text}
}

// AssistantToolCallMessage builds a role="assistant" message that requests
// one or more tool invocations. Content may be empty.
func AssistantToolCallMessage(content string, toolCalls ...ToolCall) Message {
	m := Message{Role: RoleAssistant, ToolCalls: toolCalls}
	if content != "" {
		m.Content = content
	}
	return m
}

// ToolMessage builds a role="tool" message carrying the result of a
// previous assistant tool_call. toolCallID must match the ID in the
// assistant's ToolCall.
func ToolMessage(toolCallID, result string) Message {
	return Message{
		Role:       RoleTool,
		ToolCallID: toolCallID,
		Content:    result,
	}
}

// ─── ContentPart constructors ─────────────────────────────────────────────

// TextPart builds a {"type":"text","text":...} content block.
func TextPart(text string) ContentPart {
	return ContentPart{Type: "text", Text: text}
}

// ImageURLPart references an image by URL.
//
// detail may be "low", "high", or "auto" (or "" to omit).
func ImageURLPart(url, detail string) ContentPart {
	return ContentPart{
		Type:     "image_url",
		ImageURL: &ImageURL{URL: url, Detail: detail},
	}
}

// ImageDataPart embeds a base64-encoded image as a data URI.
// mediaType is e.g. "image/png", "image/jpeg".
func ImageDataPart(mediaType string, data []byte, detail string) ContentPart {
	uri := fmt.Sprintf("data:%s;base64,%s", mediaType, base64.StdEncoding.EncodeToString(data))
	return ContentPart{
		Type:     "image_url",
		ImageURL: &ImageURL{URL: uri, Detail: detail},
	}
}

// InputAudioPart embeds base64-encoded audio. format is e.g. "wav" or "mp3".
func InputAudioPart(data []byte, format string) ContentPart {
	return ContentPart{
		Type:       "input_audio",
		InputAudio: &InputAudio{Data: base64.StdEncoding.EncodeToString(data), Format: format},
	}
}

// InputAudioPartBase64 embeds already-base64-encoded audio.
func InputAudioPartBase64(b64, format string) ContentPart {
	return ContentPart{
		Type:       "input_audio",
		InputAudio: &InputAudio{Data: b64, Format: format},
	}
}

// FilePartByID references a previously-uploaded file by its file_id.
func FilePartByID(fileID string) ContentPart {
	return ContentPart{
		Type: "file",
		File: &FileContent{FileID: fileID},
	}
}

// FilePartInline embeds file bytes (base64-encoded) into the request.
func FilePartInline(filename string, data []byte) ContentPart {
	return ContentPart{
		Type: "file",
		File: &FileContent{
			Filename: filename,
			FileData: base64.StdEncoding.EncodeToString(data),
		},
	}
}

// ─── Tool constructors ────────────────────────────────────────────────────

// FunctionTool builds a Tool of type "function" with the given schema.
//
// parameters should be a JSON schema document, e.g.:
//
//	parameters := map[string]any{
//	    "type": "object",
//	    "properties": map[string]any{
//	        "city": map[string]any{"type": "string"},
//	    },
//	    "required": []string{"city"},
//	}
func FunctionTool(name, description string, parameters map[string]any) Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        name,
			Description: description,
			Parameters:  parameters,
		},
	}
}

// ToolChoiceFunction returns a value suitable for [ChatRequest.ToolChoice]
// that forces the model to call the named function.
func ToolChoiceFunction(name string) any {
	return map[string]any{
		"type":     "function",
		"function": map[string]any{"name": name},
	}
}
