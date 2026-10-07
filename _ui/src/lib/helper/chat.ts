import { authFetch as fetch } from '../api/transport';
import { resumableStream } from './resumable-stream';

// ─── Chat Types ───

export interface ContentPart {
  /** `image` only ever appears on transcripts restored from playground history.
   *  With a `media_id` the bytes live in media storage and are re-inlined as a
   *  data URI before the request leaves the browser; without one the original
   *  data-URI was never persisted and the part is rewritten to text. */
  type: 'text' | 'image_url' | 'image' | 'file' | 'input_audio' | 'video_url';
  text?: string;
  image_url?: { url: string };
  /** Outgoing PDF/document part: `{filename, file_data: data URL}`. */
  file?: { filename?: string; file_data?: string };
  input_audio?: { data: string; format: string };
  video_url?: { url: string };
  /** Media-storage object id. Present on a stored image or file. */
  media_id?: string;
  /** Descriptor fields for a stored or omitted image, or a stored file. */
  name?: string;
  bytes?: number;
  omitted?: boolean;
  /** Content type of a stored `file` part (images use `image`). */
  mime_type?: string;
  /** A `file` part the user attached (re-sent to the model), not a delivered artifact. */
  attachment?: boolean;
}

/**
 * Assistant content as it goes upstream. Providers accept only text (and
 * refusals) in the assistant role, so every media part — a generated image,
 * a delivered file, inline audio or video — becomes a text reference. A
 * `media:<id>` reference lets the model cite the object or pass it back to a
 * tool such as generate_image's `reference_images`.
 */
export function assistantOutgoingContent(content: ContentPart[]): ContentPart[] {
  return content.map((part) => {
    if (part.type === 'text') return part;
    const kind = part.type === 'image' || part.type === 'image_url' ? 'image'
      : part.type === 'input_audio' ? 'audio'
      : part.type === 'video_url' ? 'video'
      : 'file';
    const name = part.name || part.file?.filename || kind;
    const detail = [part.mime_type, part.media_id ? `media:${part.media_id}` : ''].filter(Boolean).join(', ');
    return { type: 'text', text: `[${kind} "${name}"${detail ? ` (${detail})` : ''} delivered to the user]` };
  });
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

export interface ChatUsage {
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
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
  /** Form-editing callers must not execute calls from failed/truncated streams. */
  requireComplete?: boolean;
  /**
   * Mint IDs for tool calls the server sent without one. Several
   * OpenAI-compatible servers omit them; the ID is only used to pair the call
   * with its result in the next request, so a client-minted one is sufficient.
   */
  mintMissingToolCallIds?: boolean;
  onDelta: (deltaContent: string | ContentPart[]) => void;
  onToolCalls: (toolCalls: ToolCall[]) => void;
  onError: (error: string) => void;
  onUsage?: (usage: ChatUsage) => void;
  /** The stream's final finish_reason, reported before completeness checks. */
  onFinish?: (finishReason: string) => void;
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
    at_conversation_id?: string;
    at_history_before?: string;
    messages: Array<{ role: string; content: any; tool_calls?: any[]; tool_call_id?: string } | { at_message_id: string }>;
    tools?: ToolDefinition[];
    tool_choice?: 'auto' | 'none' | 'required';
    reasoning_effort?: string;
    stream: boolean;
    stream_options?: { include_usage: boolean };
    metadata?: Record<string, string>;
  },
  callbacks: StreamCallbacks,
  signal: AbortSignal,
  // Correlation headers (x-at-trace-id). The server reads them through
  // auditTraceInfo, so a browser-driven turn can group its own observations —
  // including tools it ran itself — onto one trace.
  headers?: Record<string, string>,
): Promise<void> {
  const options = {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...(headers ?? {}) },
    body: JSON.stringify(body),
    signal,
  };
  const response = url.replace(/\?.*$/, '').endsWith('/chats/completions') || url.replace(/\?.*$/, '') === 'api/v1/chats/completions'
    ? await resumableStream(fetch, url, options, url.replace(/completions(?:\?.*)?$/, 'streams'))
    : await fetch(url, options);

  if (!response.ok) {
    const errBody = await response.text();
    let errMsg = `HTTP ${response.status}`;
    try {
      const errJson = JSON.parse(errBody);
      errMsg = errJson?.error?.message || errJson?.message || errMsg;
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
  const accumulatedToolCalls = new Map<number, ToolCall>();
  let finishReason = '';
  let streamError = '';

  function consumeLine(line: string) {
    signal.throwIfAborted();
    const trimmed = line.trim();
    if (!trimmed.startsWith('data:')) return;
    const data = trimmed.slice(5).trimStart();
    if (data === '[DONE]') return;
    let chunk: any;
    try { chunk = JSON.parse(data); } catch {
      if (callbacks.requireComplete) throw new Error('The model returned malformed stream data. No tools were executed.');
      return;
    }
    if (!chunk || typeof chunk !== 'object') throw new Error('Invalid model stream event');
    if (chunk.error) streamError = chunk.error.message || 'The model stream failed.';
    if (chunk.choices?.[0]?.finish_reason) finishReason = chunk.choices[0].finish_reason;
    if (chunk.usage) callbacks.onUsage?.(chunk.usage);
    const delta = chunk.choices?.[0]?.delta;
    if (!delta) return;
    if (delta.content) callbacks.onDelta(delta.content);
    for (const tc of delta.tool_calls ?? []) {
      const idx: number = tc.index ?? accumulatedToolCalls.size;
      if (!Number.isInteger(idx) || idx < 0) throw new Error('Invalid tool call index');
      const existing = accumulatedToolCalls.get(idx);
      if (existing) {
        if (tc.function?.arguments) existing.function.arguments += tc.function.arguments;
        if (tc.id && !existing.id) existing.id = tc.id;
        if (tc.function?.name && !existing.function.name) existing.function.name = tc.function.name;
      } else {
        accumulatedToolCalls.set(idx, {
          id: tc.id || '', type: 'function',
          function: { name: tc.function?.name || '', arguments: tc.function?.arguments || '' },
        });
      }
    }
  }

  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split('\n');
      buffer = lines.pop() || '';
      for (const line of lines) consumeLine(line);
    }
    buffer += decoder.decode();
    if (buffer.trim()) consumeLine(buffer);
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }

  if (signal.aborted) throw new DOMException('Request aborted', 'AbortError');
  // Provider and transport failures are never a successful chat response. This
  // applies to ordinary Chats too, not only strict form-builder calls; otherwise
  // the diagnostic can be persisted as if the model authored it.
  if (streamError) throw new Error(streamError);
  const toolCalls = [...accumulatedToolCalls.entries()].sort(([a], [b]) => a - b).map(([, call]) => call);
  if (callbacks.mintMissingToolCallIds) {
    for (const call of toolCalls) {
      if (!call.id) call.id = `call_${crypto.randomUUID().replace(/-/g, '').slice(0, 24)}`;
    }
  }
  callbacks.onFinish?.(finishReason);

  if (callbacks.requireComplete) {
    if (!['stop', 'tool_calls', 'function_call'].includes(finishReason)) {
      throw new Error(finishReason === 'length' ? 'The model response was truncated. Ask for a smaller change.' : 'The model did not complete its response. Try again.');
    }
    const ids = toolCalls.map(call => call.id);
    if (ids.some(id => !id) || new Set(ids).size !== ids.length) throw new Error('The model returned invalid tool call IDs. Try again.');
  }

  // Deliver fully assembled tool calls once after stream completes
  if (toolCalls.length > 0) {
    callbacks.onToolCalls(toolCalls);
  }
}
