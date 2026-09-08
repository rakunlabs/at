import { authFetch } from './transport';

export interface PersonalModel { provider_key: string; model: string }
export interface ConversationConfig extends PersonalModel { title: string; system_prompt: string }
export interface Conversation extends ConversationConfig {
  id: string; owner_user_id: string; created_at: string; updated_at: string;
}
export interface PersonalMessage extends PersonalModel {
  id: string; conversation_id: string; request_id: string; sequence: number;
  role: 'user' | 'assistant'; content: string;
  status: 'pending' | 'streaming' | 'completed' | 'failed' | 'cancelled';
  finish_reason: string; error: string; created_at: string;
  usage: { prompt_tokens: number; completion_tokens: number; cache_read_tokens: number; cache_write_tokens: number; reasoning_tokens: number; total_tokens: number };
}
export interface PersonalPage<T> { items: T[]; next_before: string }
export interface PersonalSend { content: string; request_id: string }
export interface PendingTurn extends PersonalSend { assistant_id?: string }
export interface AcceptedTurn { user: PersonalMessage; assistant: PersonalMessage; replay: boolean }
export interface PersonalDelta { assistant_message_id: string; offset: number; content: string }

export const utf8Length = (text: string) => new TextEncoder().encode(text).length;
export const isActive = (message: PersonalMessage) => message.role === 'assistant' && (message.status === 'pending' || message.status === 'streaming');
export const conversationRoute = (id: string) => `/chat/${encodeURIComponent(id)}`;
export const mergeMessages = (current: PersonalMessage[], incoming: PersonalMessage[]) =>
  [...new Map([...current, ...incoming].map(message => [message.id, message])).values()].sort((a, b) => a.sequence - b.sequence);
export function appendPersonalDelta(message: PersonalMessage, delta: PersonalDelta): PersonalMessage | null {
  if (message.id !== delta.assistant_message_id || utf8Length(message.content) !== delta.offset) return null;
  return { ...message, content: message.content + delta.content };
}

export class PersonalChatError extends Error {
  status: number;
  constructor(status: number, message: string) { super(message); this.status = status; }
}
const root = 'api/v1/conversations';
const path = (id: string) => `${root}/${encodeURIComponent(id)}`;
const pageQuery = (before = '') => `?${new URLSearchParams({ limit: '50', ...(before ? { before } : {}) })}`;
async function checked(response: Response) {
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new PersonalChatError(response.status, body.message || `Request failed (${response.status})`);
  }
  return response;
}
async function json<T>(url: string, signal: AbortSignal, method = 'GET', body?: unknown): Promise<T> {
  const response = await checked(await authFetch(url, {
    method, signal, credentials: 'same-origin', cache: 'no-store',
    ...(body === undefined ? {} : { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }),
  }));
  return response.status === 204 ? undefined as T : response.json();
}
export const listPersonalModels = (signal: AbortSignal) => json<PersonalModel[]>(`${root}/models`, signal);
export const listConversations = (signal: AbortSignal, before = '') => json<PersonalPage<Conversation>>(root + pageQuery(before), signal);
export const getConversation = (id: string, signal: AbortSignal) => json<Conversation>(path(id), signal);
export const createConversation = (config: ConversationConfig, signal: AbortSignal) => json<Conversation>(root, signal, 'POST', { title: config.title, provider_key: config.provider_key, model: config.model, system_prompt: config.system_prompt });
export const updateConversation = (id: string, config: Partial<ConversationConfig>, signal: AbortSignal) => {
  const { title, provider_key, model, system_prompt } = config;
  return json<Conversation>(path(id), signal, 'PATCH', { title, provider_key, model, system_prompt });
};
export const deleteConversation = (id: string, signal: AbortSignal) => json<void>(path(id), signal, 'DELETE');
export const listPersonalMessages = (id: string, signal: AbortSignal, before = '') => json<PersonalPage<PersonalMessage>>(`${path(id)}/messages${pageQuery(before)}`, signal);
export const getPersonalMessage = (id: string, messageID: string, signal: AbortSignal) => json<PersonalMessage>(`${path(id)}/messages/${encodeURIComponent(messageID)}`, signal);
export const cancelPersonalMessage = (id: string, assistantID: string, signal: AbortSignal) => json<PersonalMessage>(`${path(id)}/messages/${encodeURIComponent(assistantID)}/cancel`, signal, 'POST');

// Frames and UTF-8 code points may both be split across arbitrary network chunks.
export async function readPersonalEvents(body: ReadableStream<Uint8Array>, onEvent: (event: string, data: any) => void | Promise<void>): Promise<boolean> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  let terminal = false;
  try {
    while (true) {
      const { done, value } = await reader.read();
      buffer += done ? decoder.decode() : decoder.decode(value, { stream: true });
      let boundary: RegExpExecArray | null;
      while ((boundary = /\r?\n\r?\n/.exec(buffer))) {
        const frame = buffer.slice(0, boundary.index);
        buffer = buffer.slice(boundary.index + boundary[0].length);
        let event = '';
        const data: string[] = [];
        for (const line of frame.split(/\r?\n/)) {
          if (line.startsWith('event:')) event = line.slice(6).trim();
          if (line.startsWith('data:')) data.push(line.slice(5).replace(/^ /, ''));
        }
        if (!['accepted', 'delta', 'heartbeat', 'snapshot', 'done', 'error'].includes(event)) continue;
        await onEvent(event, JSON.parse(data.join('\n')));
        if (event === 'done' || event === 'error') terminal = true;
      }
      if (done) return terminal;
    }
  } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
}
export async function sendPersonalMessage(id: string, request: PersonalSend, signal: AbortSignal, onEvent: (event: string, data: any) => void | Promise<void>) {
  const response = await checked(await authFetch(`${path(id)}/messages`, {
    method: 'POST', credentials: 'same-origin', cache: 'no-store', signal,
    headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
    body: JSON.stringify({ content: request.content, request_id: request.request_id }),
  }));
  if (!response.body || !response.headers.get('content-type')?.includes('text/event-stream')) throw new Error('Expected a conversation stream');
  return readPersonalEvents(response.body, onEvent);
}
