import type { ChatAttachment } from '../api/chat-sessions';

export interface SessionToolEvent {
  type: 'call' | 'result' | 'wait';
  name: string;
  id: string;
  progress?: string;
  result?: string;
}

export interface SessionConfirmation {
  toolName: string;
  toolId: string;
  arguments: string;
}

export interface SessionTurnState {
  phase: 'idle' | 'running' | 'awaiting_confirmation' | 'finishing' | 'completed' | 'failed' | 'stopped';
  content: string;
  tools: SessionToolEvent[];
  confirmation: SessionConfirmation | null;
  confirming: boolean;
  error: string;
}

export function emptySessionTurn(): SessionTurnState {
  return { phase: 'idle', content: '', tools: [], confirmation: null, confirming: false, error: '' };
}

export function sessionTurnBusy(state: SessionTurnState): boolean {
  return state.phase === 'running' || state.phase === 'awaiting_confirmation' || state.phase === 'finishing';
}

interface SessionEvent {
  type: string;
  content?: string;
  tool_name?: string;
  tool_id?: string;
  result?: string;
  arguments?: string;
}

/** Owns one stream and all its callbacks. Reset/Stop invalidate callbacks
 * before aborting, including asynchronous transcript/confirmation responses. */
export function createSessionTurnController(options: {
  send(sessionId: string, content: string, event: (event: SessionEvent) => void, error: (error: string) => void, done: () => Promise<void>, attachments: ChatAttachment[]): AbortController;
  confirm(sessionId: string, toolId: string, approved: boolean): Promise<void>;
  changed(state: SessionTurnState): void;
  activity(): void;
  complete(sessionId: string, lastResponse: string, current: () => boolean): Promise<boolean>;
}) {
  let state = emptySessionTurn();
  let revision = 0;
  let connection: AbortController | null = null;
  let sessionId = '';
  let disposed = false;
  const update = (patch: Partial<SessionTurnState>) => { state = { ...state, ...patch }; options.changed(state); };
  const invalidate = () => {
    revision++;
    const previous = connection;
    connection = null;
    previous?.abort();
  };
  const fail = (error: string) => {
    connection = null;
    update({ phase: 'failed', error, confirmation: null, confirming: false });
  };
  const finishing = () => state.phase === 'finishing';
  return {
    start(id: string, content: string, attachments: ChatAttachment[] = []): boolean {
      if (disposed || sessionTurnBusy(state)) return false;
      invalidate();
      sessionId = id;
      const token = revision;
      const current = () => !disposed && revision === token;
      let lastResponse = '';
      update({ ...emptySessionTurn(), phase: 'running' });
      const event = (event: SessionEvent) => {
        if (!current() || !sessionTurnBusy(state) || state.phase === 'finishing') return;
        const name = event.tool_name || '', toolId = event.tool_id || '';
        switch (event.type) {
          case 'content':
            lastResponse = event.content || '';
            update({ tools: state.tools.filter(e => e.type !== 'wait'), content: event.content ? state.content + (state.content ? '\n\n' : '') + event.content : state.content });
            break;
          case 'tool_call':
            update({ tools: [...state.tools, { type: 'call', name, id: toolId }] });
            break;
          case 'tool_progress':
            update({ tools: state.tools.some(e => e.type === 'call' && e.id === toolId)
              ? state.tools.map(e => e.type === 'call' && e.id === toolId ? { ...e, progress: `${name}: ${event.content}` } : e)
              : [...state.tools.filter(e => e.type !== 'wait'), { type: 'wait', name, id: 'wait', progress: event.content }] });
            break;
          case 'tool_result':
            update({ tools: [...state.tools.filter(e => e.id !== toolId), { type: 'result', name, id: toolId, result: event.result }],
              ...(state.confirmation?.toolId === toolId ? { phase: 'running', confirmation: null, confirming: false } : {}) });
            break;
          case 'tool_confirm':
            update({ phase: 'awaiting_confirmation', confirmation: { toolName: name, toolId, arguments: event.arguments || '{}' } });
            break;
          default: return;
        }
        options.activity();
      };
      const done = async () => {
        if (!current() || !sessionTurnBusy(state) || state.phase === 'finishing') return;
        update({ phase: 'finishing', confirmation: null, confirming: false });
        try {
          const adopted = await options.complete(id, lastResponse, current);
          if (!current() || !finishing()) return;
          connection = null;
          update({ phase: 'completed', ...(adopted ? { content: '', tools: [] } : {}) });
          options.activity();
        } catch (error) { if (current()) fail(error instanceof Error ? error.message : 'Failed to load saved response'); }
      };
      try {
        const started = options.send(id, content, event, error => { if (current() && sessionTurnBusy(state)) fail(error); }, done, attachments);
        if (current() && sessionTurnBusy(state)) connection = started;
        else started.abort();
      } catch (error) { if (current()) fail(error instanceof Error ? error.message : 'Failed to start turn'); }
      return true;
    },
    async confirm(approved: boolean): Promise<void> {
      if (!state.confirmation || state.confirming || disposed) return;
      const confirmation = state.confirmation, token = revision, id = sessionId;
      update({ confirming: true });
      try {
        await options.confirm(id, confirmation.toolId, approved);
        if (revision === token && state.confirmation?.toolId === confirmation.toolId) update({ phase: 'running', confirmation: null, confirming: false });
      } finally {
        if (revision === token) update({ confirming: false });
      }
    },
    stop() {
      if (disposed || !sessionTurnBusy(state)) return;
      invalidate();
      update({ phase: 'stopped', confirmation: null, confirming: false, error: 'Generation stopped. Any received response is kept below.' });
    },
    reset() { if (!disposed) { invalidate(); sessionId = ''; update(emptySessionTurn()); } },
    destroy() { disposed = true; invalidate(); },
  };
}
