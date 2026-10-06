/**
 * Messages written while a turn is running. They are delivered at the next
 * step boundary of that turn (after tool results, before the next model call)
 * or, when the model has finished, continue the same turn. Stop leaves them
 * queued so nothing the user wrote is silently discarded.
 */
export interface QueuedChatMessage<A> {
  id: string;
  text: string;
  attachments: A[];
  queuedAt: string;
}

let counter = 0;

export function queuedMessage<A>(text: string, attachments: A[], now = new Date()): QueuedChatMessage<A> {
  counter++;
  return { id: `q-${now.getTime().toString(36)}-${counter}`, text, attachments, queuedAt: now.toISOString() };
}

/** Remove one entry; returns the remaining queue and the removed entry. */
export function takeQueued<A>(queue: QueuedChatMessage<A>[], id: string): [QueuedChatMessage<A>[], QueuedChatMessage<A> | null] {
  const index = queue.findIndex(q => q.id === id);
  if (index < 0) return [queue, null];
  return [[...queue.slice(0, index), ...queue.slice(index + 1)], queue[index]];
}

/** Attachments first, then the text — the shape the composer has always sent. */
export function userMessageContent<A, P>(text: string, attachments: A[], toPart: (a: A) => P): string | Array<P | { type: 'text'; text: string }> {
  if (attachments.length === 0) return text;
  const parts: Array<P | { type: 'text'; text: string }> = attachments.map(toPart);
  if (text) parts.push({ type: 'text', text });
  return parts;
}

/** Short single-line preview for the queue list. */
export function queuedPreview<A extends { name: string }>(item: QueuedChatMessage<A>, max = 160): string {
  const text = item.text.replace(/\s+/g, ' ').trim();
  const files = item.attachments.length ? `${item.attachments.length} file${item.attachments.length === 1 ? '' : 's'}` : '';
  const body = text.length > max ? `${text.slice(0, max - 1)}…` : text;
  return [body, files ? `[${files}]` : ''].filter(Boolean).join(' ');
}

export const INTERRUPTED_TOOL_RESULT = 'Interrupted by the user before this tool finished. It may or may not have taken effect; check before repeating it.';

interface TranscriptMessage {
  role: string;
  content?: unknown;
  tool_calls?: Array<{ id: string }>;
  tool_call_id?: string;
}

/**
 * Tool calls of the latest assistant step that have no result. An interrupted
 * turn leaves these behind, and every provider rejects a history whose tool
 * calls are not answered, so they are closed before the next model call.
 */
export function unansweredToolCalls(messages: TranscriptMessage[]): string[] {
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    if (m.role === 'assistant' && m.tool_calls?.length) {
      const answered = new Set(messages.slice(i + 1).filter(x => x.role === 'tool').map(x => x.tool_call_id));
      return m.tool_calls.map(c => c.id).filter(id => !answered.has(id));
    }
    if (m.role === 'user') return [];
  }
  return [];
}

/** An assistant entry with no text, parts or tool calls: an interrupted placeholder. */
export function isEmptyAssistant(m: TranscriptMessage | undefined): boolean {
  if (!m || m.role !== 'assistant' || m.tool_calls?.length) return false;
  if (typeof m.content === 'string') return !m.content;
  return Array.isArray(m.content) ? m.content.length === 0 : !m.content;
}
