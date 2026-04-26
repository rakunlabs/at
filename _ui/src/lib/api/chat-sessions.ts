import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export interface ChatSessionConfig {
  platform?: string;
  platform_user_id?: string;
  platform_channel_id?: string;
  bot_config_id?: string;
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

export async function listChatMessages(sessionId: string): Promise<ChatMessage[]> {
  const res = await api.get<ChatMessage[]>(`/chat/sessions/${sessionId}/messages`);
  return res.data;
}

/** Send a message to a chat session and receive SSE events. Returns an AbortController. */
export function sendMessage(
  sessionId: string,
  content: string,
  onEvent: (event: any) => void,
  onError: (error: string) => void,
  onDone: () => void,
): AbortController {
  const controller = new AbortController();

  const basePath = document.querySelector('base')?.getAttribute('href') || '';

  fetch(`${basePath}api/v1/chat/sessions/${sessionId}/messages`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content }),
    signal: controller.signal,
  })
    .then(async (response) => {
      if (!response.ok) {
        const text = await response.text();
        onError(text || `HTTP ${response.status}`);
        return;
      }

      const reader = response.body?.getReader();
      if (!reader) {
        onError('No response body');
        return;
      }

      const decoder = new TextDecoder();
      let buffer = '';

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split('\n');
        buffer = lines.pop() || '';

        for (const line of lines) {
          if (line.startsWith('data: ')) {
            try {
              const data = JSON.parse(line.slice(6));
              if (data.type === 'done') {
                onDone();
                return;
              }
              if (data.error) {
                onError(data.error);
                return;
              }
              onEvent(data);
            } catch {
              // Skip malformed JSON
            }
          } else if (line.startsWith('event: error')) {
            // Next data line will have the error
          }
        }
      }

      onDone();
    })
    .catch((err) => {
      if (err.name !== 'AbortError') {
        onError(err.message || 'Network error');
      }
    });

  return controller;
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
