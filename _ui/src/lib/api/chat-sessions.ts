import axios from 'axios';
import { authFetch as fetch } from './transport';
import type { ListResult, ListParams } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export interface ChatSessionConfig {
  platform?: string;
  platform_user_id?: string;
  platform_channel_id?: string;
  bot_config_id?: string;
  active_task_id?: string;
  history_limit?: number;
  task_discussion_mode?: boolean;
  disable_task_result_sync?: boolean;
}

export interface ListChatSessionsParams extends ListParams {
  /** Filter sessions to only those tied to the given BotConfig. */
  bot_config_id?: string;
  /** Filter sessions to only those for a given platform (telegram, discord, …). */
  platform?: string;
}

export interface ChatSession {
  id: string;
  agent_id: string;
  task_id?: string;
  organization_id?: string;
  name: string;
  config: ChatSessionConfig;
  created_at: string;
  updated_at: string;
  created_by: string;
  updated_by: string;
}

export interface ChatMessageData {
  content: any;
  tool_calls?: any;
  tool_call_id?: string;
  attachments?: ChatAttachment[];
}

export interface ChatAttachment {
  name: string;
  media_type: string;
  data: string;
}

export interface ChatMessage {
  id: string;
  session_id: string;
  role: string;
  data: ChatMessageData;
  created_at: string;
}

export async function listChatSessions(params?: ListChatSessionsParams): Promise<ListResult<ChatSession>> {
  const res = await api.get<ListResult<ChatSession>>('/chat/sessions', { params });
  return res.data;
}

export async function getChatSession(id: string): Promise<ChatSession> {
  const res = await api.get<ChatSession>(`/chat/sessions/${id}`);
  return res.data;
}

export async function createChatSession(data: Partial<ChatSession>): Promise<ChatSession> {
  const res = await api.post<ChatSession>('/chat/sessions', data);
  return res.data;
}

export async function updateChatSession(id: string, data: Partial<ChatSession>): Promise<ChatSession> {
  const res = await api.put<ChatSession>(`/chat/sessions/${id}`, data);
  return res.data;
}

export async function deleteChatSession(id: string): Promise<void> {
  await api.delete(`/chat/sessions/${id}`);
}

export async function clearChatMessages(sessionId: string): Promise<void> {
  await api.delete(`/chat/sessions/${sessionId}/messages`);
}

export interface ListChatMessagesOptions {
  /** Return only the most recent N messages (chronological order). */
  limit?: number;
  /** Page messages strictly older than this message ID (scroll-up lazy load). */
  beforeId?: string;
}

export async function listChatMessages(
  sessionId: string,
  opts?: ListChatMessagesOptions
): Promise<ChatMessage[]> {
  const params: Record<string, string | number> = {};
  if (opts?.limit) params.limit = opts.limit;
  if (opts?.beforeId) params.before_id = opts.beforeId;
  const res = await api.get<ChatMessage[]>(`/chat/sessions/${sessionId}/messages`, { params });
  return res.data;
}

/** Send a message to a chat session and receive SSE events. Returns an AbortController. */
export function sendMessage(
  sessionId: string,
  content: string,
  onEvent: (event: any) => void,
  onError: (error: string) => void,
  onDone: () => void | Promise<void>,
  attachments: ChatAttachment[] = [],
): AbortController {
  const controller = new AbortController();

  const basePath = document.querySelector('base')?.getAttribute('href') || '';

  fetch(`${basePath}api/v1/chat/sessions/${sessionId}/messages`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content, attachments }),
    signal: controller.signal,
  })
    .then(async (response) => {
      if (!response.ok) {
        const text = await response.text();
        let message = text;
        try { message = JSON.parse(text)?.message || text; } catch { /* Non-JSON proxy response. */ }
        onError(message || `HTTP ${response.status}`);
        return;
      }

      await consumeChatEvents(response, onEvent, onDone);
    })
    .catch((err) => {
      if (err.name !== 'AbortError') {
        onError(err.message || 'Network error');
      }
    });

  return controller;
}

/** Decode complete SSE frames, including split UTF-8 and a final unterminated frame.
 * A disconnected stream must never look like a completed (persisted) answer. */
export async function consumeChatEvents(
  response: Response,
  onEvent: (event: any) => void,
  onDone: () => void | Promise<void>,
): Promise<void> {
  const reader = response.body?.getReader();
  if (!reader) throw new Error('No response body');
  const decoder = new TextDecoder();
  let buffer = '';
  let data: string[] = [];
  let eventName = '';
  const dispatch = async () => {
    if (!data.length) return false;
    const payload = data.join('\n');
    data = [];
    const event = JSON.parse(payload);
    if (event.error || event.type === 'error' || eventName === 'error') {
      throw new Error(typeof event.error === 'string' ? event.error : event.error?.message || event.message || 'The agent could not complete this response');
    }
    eventName = '';
    if (event.type === 'done') { await onDone(); return true; }
    onEvent(event);
    return false;
  };
  const line = async (value: string) => {
    if (!value) {
      const complete = await dispatch();
      eventName = '';
      return complete;
    }
    if (value.startsWith('data:')) data.push(value.slice(5).replace(/^ /, ''));
    if (value.startsWith('event:')) eventName = value.slice(6).trim();
    return false;
  };
  try {
    while (true) {
      const { done, value } = await reader.read();
      buffer += done ? decoder.decode() : decoder.decode(value, { stream: true });
      let end: number;
      while ((end = buffer.indexOf('\n')) !== -1) {
        const current = buffer.slice(0, end).replace(/\r$/, '');
        buffer = buffer.slice(end + 1);
        if (await line(current)) return;
      }
      if (done) {
        if (buffer && await line(buffer.replace(/\r$/, ''))) return;
        if (await dispatch()) return;
        throw new Error('Connection ended before the response completed. Received text is preserved; retry when ready.');
      }
    }
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}

/** Send a tool confirmation (approve or reject) for a pending tool call. */
export async function confirmToolCall(
  sessionId: string,
  toolId: string,
  approved: boolean,
): Promise<void> {
  await api.post(`/chat/sessions/${sessionId}/confirm`, {
    tool_id: toolId,
    approved,
  });
}
