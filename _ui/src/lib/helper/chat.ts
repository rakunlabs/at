// ─── Chat Types ───

export interface ContentPart {
  type: 'text' | 'image_url';
  text?: string;
  image_url?: { url: string };
}

export interface ChatMessage {
  role: 'user' | 'assistant' | 'system' | 'tool';
  content: string | ContentPart[];
  tool_calls?: ToolCall[];
  tool_call_id?: string;
}

export interface ToolCall {
  id: string;
  type: 'function';
  function: {
    name: string;
    arguments: string;
  };
}

export interface ToolDefinition {
  type: 'function';
  function: {
    name: string;
    description: string;
    parameters: Record<string, any>;
  };
}

// ─── Content Helpers ───

/** Extract display text from a ChatMessage's content. */
export function getTextContent(content: string | ContentPart[]): string {
  if (typeof content === 'string') return content;
  return content
    .filter((p) => p.type === 'text')
    .map((p) => p.text || '')
    .join('');
}

/** Merge an SSE delta.content into the current assistant message content.
 *  delta.content may be a plain string (text-only) or an array of
 *  content parts (multimodal, e.g. text + image_url from Gemini). */
export function mergeDeltaContent(
  prev: string | ContentPart[],
  deltaContent: string | ContentPart[],
): string | ContentPart[] {
  if (typeof deltaContent === 'string') {
    if (typeof prev === 'string') return prev + deltaContent;
    const parts = [...prev];
    const lastText = parts.findLast((p) => p.type === 'text');
    if (lastText) {
      lastText.text = (lastText.text || '') + deltaContent;
    } else {
      parts.push({ type: 'text', text: deltaContent });
    }
    return parts;
  }

  let parts: ContentPart[] =
    typeof prev === 'string'
      ? prev ? [{ type: 'text', text: prev }] : []
      : [...prev];

  for (const part of deltaContent) {
    if (part.type === 'text' && part.text) {
      const lastText = parts.findLast((p) => p.type === 'text');
      if (lastText) {
        lastText.text = (lastText.text || '') + part.text;
      } else {
        parts.push({ type: 'text', text: part.text });
      }
    } else if (part.type === 'image_url') {
      parts.push(part);
    }
  }
  return parts;
}

// ─── SSE Streaming ───

export interface StreamCallbacks {
  onDelta: (deltaContent: string | ContentPart[]) => void;
  onToolCalls: (toolCalls: ToolCall[]) => void;
  onError: (error: string) => void;
}

/**
 * Stream a chat completion request via SSE.
 * Uses the admin API endpoint (no gateway auth needed).
 *
 * Tool calls are accumulated across multiple SSE chunks (OpenAI streaming
 * format uses `index` to identify which tool call is being continued, and
 * arguments may arrive as fragments across multiple deltas). The fully
 * assembled tool calls are delivered via `onToolCalls` once after the
 * stream completes.
 */
export async function streamChatCompletion(
  url: string,
  body: {
    model: string;
    messages: Array<{ role: string; content: any; tool_calls?: any[]; tool_call_id?: string }>;
    tools?: ToolDefinition[];
    stream: boolean;
  },
  callbacks: StreamCallbacks,
  signal: AbortSignal,
): Promise<void> {
  const response = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  });

  if (!response.ok) {
    const errBody = await response.text();
    let errMsg = `HTTP ${response.status}`;
    try {
      const errJson = JSON.parse(errBody);
      errMsg = errJson?.error?.message || errMsg;
    } catch {
      errMsg = errBody || errMsg;
    }
    throw new Error(errMsg);
  }

  const reader = response.body?.getReader();
  if (!reader) throw new Error('No response body');

  const decoder = new TextDecoder();
  let buffer = '';

  // Accumulate tool calls by index across all SSE chunks.
  // OpenAI streaming format: first delta for a tool call carries id +
  // function.name, subsequent deltas for the same index append to
  // function.arguments.
  const accumulatedToolCalls: ToolCall[] = [];

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;

    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split('\n');
    buffer = lines.pop() || '';

    for (const line of lines) {
      const trimmed = line.trim();
      if (!trimmed || !trimmed.startsWith('data: ')) continue;

      const data = trimmed.slice(6);
      if (data === '[DONE]') continue;

      try {
        const chunk = JSON.parse(data);
        const delta = chunk.choices?.[0]?.delta;
        if (!delta) continue;

        if (delta.content) {
          callbacks.onDelta(delta.content);
        }

        if (delta.tool_calls && delta.tool_calls.length > 0) {
          for (const tc of delta.tool_calls) {
            // Use the index field if present (OpenAI format), otherwise
            // fall back to positional index within the accumulated array.
            const idx: number = tc.index ?? accumulatedToolCalls.length;

            if (idx < accumulatedToolCalls.length) {
              // Continuation of an existing tool call — append arguments
              const existing = accumulatedToolCalls[idx];
              if (tc.function?.arguments) {
                existing.function.arguments += tc.function.arguments;
              }
              // id and name can also arrive in later chunks for some providers
              if (tc.id && !existing.id) existing.id = tc.id;
              if (tc.function?.name && !existing.function.name) {
                existing.function.name = tc.function.name;
              }
            } else {
              // New tool call at this index
              accumulatedToolCalls.push({
                id: tc.id || '',
                type: 'function',
                function: {
                  name: tc.function?.name || '',
                  arguments: tc.function?.arguments || '',
                },
              });
            }
          }
        }
      } catch {
        // Skip unparseable chunks
      }
    }
  }

  // Deliver fully assembled tool calls once after stream completes
  if (accumulatedToolCalls.length > 0) {
    callbacks.onToolCalls(accumulatedToolCalls);
  }
}
