import axios from 'axios';
import { consumeRunEvents, followRunStream, RunEventError } from '../helper/run-events';
import type { PlaygroundMessage } from './playground';

// Server-side Chats runs keep answering a conversation after the page closes.
// The page starts one, follows its events, and reattaches with the replay
// stream when it comes back. Unknown replay never restarts work.

const api = axios.create({ baseURL: 'api/v1' });

export interface ChatRunReceipt {
  id: string;
  workspace_id: string;
  heartbeat_at: string;
  cancel_requested: boolean;
  /** The owner stopped renewing it; a new run may take the conversation over. */
  stale: boolean;
}

export interface ChatRunStatus {
  run: ChatRunReceipt | null;
  /** Live replay on the replica that answered; '' when it runs elsewhere or has ended. */
  stream_id: string;
}

export type ChatRunEvent =
  | { type: 'status'; run_id: string; trace_id: string }
  | { type: 'delta'; content?: string; reasoning?: string }
  | { type: 'message'; message: PlaygroundMessage }
  | { type: 'tool_start'; tool_call_id: string; name: string }
  | { type: 'tool_progress'; tool_call_id?: string; content: string }
  /** The run stopped at a `question` call; the answer starts the next run. */
  | { type: 'question'; tool_call_id: string }
  /** The live replay is full; the run continues and saves its results. */
  | { type: 'truncated' }
  | { type: 'done'; waiting?: boolean };

/** Rejection of a run stream; `cancelled` marks a deliberate Stop. */
export { RunEventError as ChatRunError };

export async function getChatRun(conversationId: string): Promise<ChatRunStatus> {
  const res = await api.get<ChatRunStatus>(`/chats/conversations/${encodeURIComponent(conversationId)}/run`);
  return res.data;
}

/** Stop the conversation's run; with `runId`, only that run (a late Stop must not hit its successor). */
export async function stopChatRun(conversationId: string, runId = ''): Promise<void> {
  await api.delete(`/chats/conversations/${encodeURIComponent(conversationId)}/run`, { params: runId ? { run_id: runId } : undefined });
}

/**
 * Start a run (no `resume`) or reattach to one by stream ID (`resume`). Resolves
 * on `done`; rejects with ChatRunError when the run failed or was stopped.
 * Aborting `signal` only detaches this page — the run keeps going.
 */
export async function followChatRun(
  conversationId: string,
  options: { streamId: string; resume?: boolean; traceId?: string; signal: AbortSignal; fetcher?: typeof fetch },
  onEvent: (event: ChatRunEvent) => void,
): Promise<void> {
  const base = `api/v1/chats/conversations/${encodeURIComponent(conversationId)}`;
  await followRunStream({
    fetcher: options.fetcher ?? fetch,
    start: `${base}/run`,
    replay: `${base}/streams`,
    body: { trace_id: options.traceId || undefined },
    streamId: options.streamId,
    resume: options.resume,
    signal: options.signal,
    endedEarly: CHAT_RUN_ENDED_EARLY,
  }, onEvent);
}

const CHAT_RUN_ENDED_EARLY = 'The run stream ended early. Its saved messages are shown; reload to see later ones.';

/** Parse a run's events. The run must end in `done` or an error event; anything else is a lost stream. */
export function consumeChatRunEvents(response: Response, onEvent: (event: ChatRunEvent) => void): Promise<void> {
  return consumeRunEvents(response, onEvent, CHAT_RUN_ENDED_EARLY);
}
