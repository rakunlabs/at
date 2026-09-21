/**
 * A minimal Streamable HTTP MCP client that runs in the page.
 *
 * It speaks the same four messages the Go client does — initialize,
 * notifications/initialized, tools/list, tools/call — because that is the
 * whole surface AT uses. The difference is who dials: this one runs on the
 * user's machine, which is what makes a loopback MCP server reachable at all.
 */

export interface LocalMCPTool {
  name: string;
  description?: string;
  inputSchema?: Record<string, unknown>;
}

export type LocalMCPFailure = 'unreachable' | 'blocked' | 'protocol' | 'tool';

/**
 * A browser reports a refused connection and a refused cross-origin request
 * identically, as `TypeError: Failed to fetch`. `hint` therefore names the
 * probable causes rather than pretending to have determined one — "failed to
 * fetch" on its own is an error the user cannot act on.
 */
export class LocalMCPError extends Error {
  kind: LocalMCPFailure;
  hint: string;

  constructor(kind: LocalMCPFailure, message: string, hint = '') {
    super(message);
    this.name = 'LocalMCPError';
    this.kind = kind;
    this.hint = hint;
  }
}

export const CORS_HINT =
  'The MCP server must allow this page: Access-Control-Allow-Origin for this origin, ' +
  'Access-Control-Allow-Headers including content-type, mcp-session-id and mcp-protocol-version, ' +
  'Access-Control-Expose-Headers: Mcp-Session-Id, and — because this page is not on a local ' +
  'address — Access-Control-Allow-Private-Network: true on the preflight.';

interface RPCRequest {
  jsonrpc: '2.0';
  id?: number;
  method: string;
  params?: unknown;
}

const PROTOCOL_VERSION = '2025-03-26';

export interface LocalMCPClientOptions {
  headers?: Record<string, string>;
  signal?: AbortSignal;
  /** Per-call deadline. A local server that hangs must not hang the turn. */
  timeoutMs?: number;
  fetchImpl?: typeof fetch;
}

export class LocalMCPClient {
  readonly url: string;
  private headers: Record<string, string>;
  private fetchImpl: typeof fetch;
  private timeoutMs: number;
  private signal?: AbortSignal;
  private sessionId = '';
  private protocolVersion = '';
  private nextId = 1;
  private initialized = false;

  constructor(url: string, opts: LocalMCPClientOptions = {}) {
    this.url = url.trim();
    this.headers = opts.headers ?? {};
    this.fetchImpl = opts.fetchImpl ?? ((...args) => fetch(...args));
    this.timeoutMs = opts.timeoutMs ?? 30_000;
    this.signal = opts.signal;
  }

  async initialize(): Promise<void> {
    if (this.initialized) return;
    const result = await this.request({
      jsonrpc: '2.0',
      id: this.nextId++,
      method: 'initialize',
      params: {
        protocolVersion: PROTOCOL_VERSION,
        capabilities: {},
        clientInfo: { name: 'at-chats', version: '1' },
      },
    });
    const version = (result as { protocolVersion?: string } | null)?.protocolVersion;
    // The negotiated version is echoed on later requests; a server that
    // reports none keeps the one we asked for.
    this.protocolVersion = version || PROTOCOL_VERSION;
    this.initialized = true;
    // A notification: no id, no response expected. A server that rejects it
    // is not a reason to abandon a working session.
    try {
      await this.request({ jsonrpc: '2.0', method: 'notifications/initialized' });
    } catch {
      /* ignored */
    }
  }

  async listTools(): Promise<LocalMCPTool[]> {
    await this.initialize();
    const result = await this.request({ jsonrpc: '2.0', id: this.nextId++, method: 'tools/list' });
    const tools = (result as { tools?: LocalMCPTool[] } | null)?.tools;

    return Array.isArray(tools) ? tools : [];
  }

  async callTool(name: string, args: Record<string, unknown>): Promise<string> {
    await this.initialize();
    const result = await this.request({
      jsonrpc: '2.0',
      id: this.nextId++,
      method: 'tools/call',
      params: { name, arguments: args ?? {} },
    });
    const content = (result as { content?: Array<{ type?: string; text?: string }> } | null)?.content;
    if (!Array.isArray(content)) return '';

    return content
      .map(part => (typeof part?.text === 'string' ? part.text : ''))
      .filter(Boolean)
      .join('\n');
  }

  private async request(body: RPCRequest): Promise<unknown> {
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      // Streamable HTTP requires clients to accept both shapes.
      Accept: 'application/json, text/event-stream',
      ...this.headers,
    };
    if (this.sessionId) headers['Mcp-Session-Id'] = this.sessionId;
    if (this.protocolVersion) headers['MCP-Protocol-Version'] = this.protocolVersion;

    const controller = new AbortController();
    const abort = () => controller.abort();
    this.signal?.addEventListener('abort', abort, { once: true });
    const timer = setTimeout(abort, this.timeoutMs);

    let res: Response;
    try {
      res = await this.fetchImpl(this.url, {
        method: 'POST',
        headers,
        body: JSON.stringify(body),
        signal: controller.signal,
        // The page must not send AT's cookies to a third program.
        credentials: 'omit',
        mode: 'cors',
      });
    } catch (e: unknown) {
      if (this.signal?.aborted) {
        throw new LocalMCPError('unreachable', 'cancelled');
      }
      const reason = (e as Error)?.name === 'AbortError'
        ? `no response within ${Math.round(this.timeoutMs / 1000)}s`
        : 'could not be reached from this browser';
      throw new LocalMCPError('blocked', `${this.url} ${reason}`, CORS_HINT);
    } finally {
      clearTimeout(timer);
      this.signal?.removeEventListener('abort', abort);
    }

    // Present only when the server exposes it cross-origin. Without it the
    // session cannot be read, so a stateful server is used statelessly rather
    // than failing in a way that looks like a protocol fault.
    const session = res.headers.get('Mcp-Session-Id') || res.headers.get('X-Session-ID');
    if (session) this.sessionId = session;

    if (res.status === 202 || res.status === 204) return null;
    if (!res.ok) {
      const text = await safeText(res);
      throw new LocalMCPError('protocol', `HTTP ${res.status} from ${this.url}${text ? `: ${text}` : ''}`);
    }

    const message = await decodeBody(res);
    if (!message) return null;
    if (message.error) {
      throw new LocalMCPError('tool', message.error.message || 'MCP error', '');
    }

    return message.result ?? null;
  }
}

interface RPCMessage {
  result?: unknown;
  error?: { code?: number; message?: string };
}

async function decodeBody(res: Response): Promise<RPCMessage | null> {
  const type = (res.headers.get('Content-Type') || '').toLowerCase();
  const text = await safeText(res);
  if (!text.trim()) return null;

  if (type.includes('text/event-stream')) {
    return parseSSE(text);
  }
  try {
    return JSON.parse(text) as RPCMessage;
  } catch {
    // Some servers answer SSE without saying so.
    const sse = parseSSE(text);
    if (sse) return sse;
    throw new LocalMCPError('protocol', 'the response was not JSON-RPC');
  }
}

/**
 * Returns the last JSON-RPC message carried by an SSE body. Exported for
 * tests: the framing is the part most likely to differ between servers.
 */
export function parseSSE(body: string): RPCMessage | null {
  let last: RPCMessage | null = null;
  for (const line of body.split(/\r?\n/)) {
    if (!line.startsWith('data:')) continue;
    const payload = line.slice(5).trim();
    if (!payload || payload === '[DONE]') continue;
    try {
      last = JSON.parse(payload) as RPCMessage;
    } catch {
      /* a comment or keep-alive frame */
    }
  }

  return last;
}

async function safeText(res: Response): Promise<string> {
  try {
    return await res.text();
  } catch {
    return '';
  }
}

/**
 * Distinguishes "nothing is listening" from "listening but refusing this
 * page", which a normal fetch failure cannot. An opaque `no-cors` request is
 * a simple request: it reaches the server without a preflight and resolves
 * regardless of the response headers, so only a transport failure rejects it.
 *
 * Used by the connection test, never on the tool-calling path.
 */
export async function probeReachable(url: string, fetchImpl: typeof fetch = fetch): Promise<boolean> {
  try {
    await fetchImpl(url, { method: 'POST', mode: 'no-cors', credentials: 'omit' });

    return true;
  } catch {
    return false;
  }
}
