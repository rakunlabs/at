import { resumableStream } from './resumable-stream';

// Client side of durable agent runs (Developer Spaces sessions, Chats
// background runs): one event stream that is started by a POST, reattached by
// GET with a byte offset, and ends in exactly one terminal event.

export class RunEventError extends Error {
  /** The run was stopped on purpose; completed steps are saved. */
  readonly cancelled: boolean;
  constructor(message: string, cancelled = false) {
    super(message);
    this.cancelled = cancelled;
  }
}

/**
 * Read `data:` frames until the terminal `done` event. An `error` event rejects
 * with its message; a stream that ends without a terminal event rejects too,
 * because a dropped connection must not look like a finished run.
 */
export async function consumeRunEvents<E extends { type: string }>(
  response: Response,
  onEvent: (event: E) => void,
  endedEarly = 'The run stream ended before it finished. Reload to see what was saved.',
): Promise<void> {
  const reader = response.body?.getReader();
  if (!reader) throw new RunEventError('No response body');
  const decoder = new TextDecoder();
  let buffer = '';
  let data: string[] = [];
  const dispatch = () => {
    if (!data.length) return false;
    const event = JSON.parse(data.join('\n'));
    data = [];
    if (event.type === 'error') throw new RunEventError(event.error || 'The run could not complete', event.cancelled === true);
    onEvent(event as E);
    return event.type === 'done';
  };
  try {
    while (true) {
      const { done, value } = await reader.read();
      buffer += done ? decoder.decode() : decoder.decode(value, { stream: true });
      let end: number;
      while ((end = buffer.indexOf('\n')) !== -1) {
        const line = buffer.slice(0, end).replace(/\r$/, '');
        buffer = buffer.slice(end + 1);
        if (line === '') { if (dispatch()) return; continue; }
        if (line.startsWith('data:')) data.push(line.slice(5).replace(/^ /, ''));
      }
      if (done) {
        if (buffer.startsWith('data:')) data.push(buffer.slice(5).replace(/^ /, ''));
        if (dispatch()) return;
        throw new RunEventError(endedEarly);
      }
    }
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}

export interface RunStreamOptions {
  fetcher: typeof fetch;
  /** POST target that starts the run; unused when resuming. */
  start: string;
  /** Replay base: `${replay}/${streamId}?offset=` reattaches. */
  replay: string;
  body?: unknown;
  streamId?: string;
  /** Reattach by GET only; never starts work. */
  resume?: boolean;
  signal?: AbortSignal;
  endedEarly?: string;
  /** Message for a refused start or reattach; defaults to the server's message. */
  failed?: (status: number, message: string) => string;
}

/**
 * Start or reattach to a run and deliver its events. Aborting only detaches —
 * the run keeps going; stopping it is the surface's own cancel call.
 */
export async function followRunStream<E extends { type: string }>(options: RunStreamOptions, onEvent: (event: E) => void): Promise<void> {
  const init: RequestInit = options.resume
    ? { signal: options.signal }
    : { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(options.body ?? {}), signal: options.signal };
  const response = await resumableStream(options.fetcher, options.resume ? '' : options.start, init, options.replay, {
    id: options.streamId, resume: options.resume, cancelOnAbort: false,
  });
  if (!response.ok) {
    // Only the server's own JSON message is shown; a proxy's HTML error page
    // never becomes the reader's error text.
    let message = '';
    try { const data = await response.json(); message = data.message || data.error?.message || (typeof data.error === 'string' ? data.error : ''); } catch { /* not JSON */ }
    throw new RunEventError(options.failed ? options.failed(response.status, message) : message || `Request failed (${response.status})`);
  }
  await consumeRunEvents(response, onEvent, options.endedEarly);
}
