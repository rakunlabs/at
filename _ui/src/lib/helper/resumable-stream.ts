/** One POST starts work. Only GET reattaches; byte offsets preserve split UTF-8
 * and partial SSE frames without repeating callbacks or tool execution. */
export async function resumableStream(
  fetcher: typeof fetch,
  url: string,
  init: RequestInit,
  replayBase: string,
  options: { id?: string; resume?: boolean; cancelOnAbort?: boolean } = {},
): Promise<Response> {
  const id = options.id ?? crypto.randomUUID();
  const replay = `${replayBase}/${id}`;
  const headers = new Headers(init.headers);
  headers.set('X-AT-Stream-ID', id);
  const signal = init.signal;
  const cancel = () => {
    // Stop/navigation is explicit cancellation, unlike a network disconnect.
    void fetcher(replay, { method: 'DELETE', keepalive: true }).catch(() => {});
  };
  if (options.cancelOnAbort !== false) signal?.addEventListener('abort', cancel, { once: true });
  let offset = 0;
  let response: Response | undefined;
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
  let stopped = false;
  let failures = 0;
  let tail = '';
  const wait = (ms: number) => new Promise<void>((resolve, reject) => {
    const abort = () => { clearTimeout(timer); reject(signal?.reason ?? new DOMException('Aborted', 'AbortError')); };
    const timer = setTimeout(() => { signal?.removeEventListener('abort', abort); resolve(); }, ms);
    if (signal?.aborted) abort(); else signal?.addEventListener('abort', abort, { once: true });
  });
  const reconnect = async () => {
    while (true) {
      signal?.throwIfAborted();
      if (++failures > 12) throw new Error('Stream reconnection timed out. Check saved messages before starting another turn.');
      await wait(Math.min(10000, 500 * 2 ** Math.min(failures - 1, 5)));
      let next: Response;
      try { next = await request(`${replay}?offset=${offset}`, { signal, headers: init.headers }); }
      catch (e) { if (signal?.aborted) throw e; continue; }
      if (!next.ok) {
        if (next.status >= 500) continue;
        const text = await next.text();
        let message = text;
        try { const data = JSON.parse(text); message = data.message || data.error?.message || text; } catch { /* proxy error */ }
        throw new Error(message || `Stream replay unavailable (${next.status}). Check saved history.`);
      }
      if (next.headers.get('X-AT-Stream-Replay') !== '1') throw new Error('Server does not support safe stream replay. Check saved history.');
      response = next;
      reader = next.body?.getReader();
      if (!reader) throw new Error('No replay body');
      return;
    }
  };
  const cleanup = () => { signal?.removeEventListener('abort', cancel); };
  // Header timeout is cleared as soon as fetch resolves, so it never caps the
  // generation itself. A silent body has its own heartbeat watchdog below.
  const request = async (target: string, options: RequestInit) => {
    const deadline = new AbortController();
    const timer = setTimeout(() => deadline.abort(), 30000);
    const combined = options.signal ? AbortSignal.any([options.signal, deadline.signal]) : deadline.signal;
    try { return await fetcher(target, { ...options, signal: combined }); }
    finally { clearTimeout(timer); }
  };
  try {
    try { response = await request(options.resume ? `${replay}?offset=0` : url, options.resume ? { signal, headers: init.headers } : { ...init, headers }); }
    catch (e) { if (signal?.aborted) throw e; await reconnect(); }
    if (!response) throw new Error('No stream response');
    // Old servers and ordinary rejection responses keep their original behavior.
    if (!response.ok || response.headers.get('X-AT-Stream-Replay') !== '1') { cleanup(); return response; }
    reader ??= response.body?.getReader();
    const body = new ReadableStream<Uint8Array>({
      async pull(controller) {
        try {
          while (!stopped) {
            signal?.throwIfAborted();
            let result: ReadableStreamReadResult<Uint8Array>;
            try {
              if (!reader) throw new Error('No stream body');
              let timer: ReturnType<typeof setTimeout> | undefined;
              try {
                result = await Promise.race([
                  reader.read(),
                  new Promise<never>((_, reject) => { timer = setTimeout(() => reject(new Error('Stream heartbeat lost')), 45000); }),
                ]);
              } finally { clearTimeout(timer); }
            }
            catch (e) {
              if (signal?.aborted) throw e;
              await reader?.cancel().catch(() => {}); reader?.releaseLock(); reader = undefined;
              await reconnect(); continue;
            }
            if (!result.done) {
              offset += result.value.byteLength; failures = 0;
              tail = (tail + new TextDecoder().decode(result.value)).slice(-128);
              controller.enqueue(result.value); return;
            }
            reader?.releaseLock(); reader = undefined;
            const length = response?.headers.get('X-AT-Stream-Length');
            const fullyRead = response?.headers.get('X-AT-Stream-Complete') === 'true' && length !== null && length !== undefined && Number(length) === offset;
            if (tail.endsWith(': at-stream-complete\n\n') || fullyRead) { stopped = true; cleanup(); controller.close(); return; }
            await reconnect();
          }
        } catch (e) { stopped = true; cleanup(); controller.error(e); }
      },
      async cancel() { stopped = true; cleanup(); await reader?.cancel().catch(() => {}); },
    });
    return new Response(body, { status: response.status, headers: response.headers });
  } catch (e) { cleanup(); throw e; }
}
