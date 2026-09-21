import axios from 'axios';

const api = axios.create({
  baseURL: 'api/v1',
});

/**
 * An MCP endpoint running on the account holder's own machine.
 *
 * The server stores and validates these records. It never opens a connection
 * to one — the browser does, which is what makes a loopback address mean the
 * user's machine rather than the server's.
 */
export interface LocalMCPServer {
  id: string;
  name: string;
  url: string;
  /** Header values read back as '***'; reveal returns the stored ones. */
  headers?: Record<string, string>;
  created_at?: string;
  updated_at?: string;
}

export async function listLocalMCPServers(): Promise<LocalMCPServer[]> {
  const res = await api.get<{ servers: LocalMCPServer[] | null }>('/chats/local-mcp-servers');

  return res.data?.servers ?? [];
}

export async function saveLocalMCPServers(servers: LocalMCPServer[]): Promise<LocalMCPServer[]> {
  const res = await api.put<{ servers: LocalMCPServer[] | null }>('/chats/local-mcp-servers', { servers });

  return res.data?.servers ?? [];
}

/**
 * Returns the stored header values for one record, at the point the browser is
 * about to dial it — so the page does not hold every credential for the whole
 * session.
 */
export async function revealLocalMCPHeaders(id: string): Promise<Record<string, string>> {
  const res = await api.post<{ headers: Record<string, string> | null }>(
    `/chats/local-mcp-servers/${encodeURIComponent(id)}/reveal`,
  );

  return res.data?.headers ?? {};
}

export interface LocalToolObservation {
  trace_id: string;
  session_id?: string;
  name: string;
  server?: string;
  status: 'ok' | 'error';
  latency_ms: number;
  error?: string;
  input?: string;
  output?: string;
}

/**
 * Reports one local tool call to Traces.
 *
 * Without it a conversation's trace would show its generations with the tool
 * steps between them missing — not as an absence, but as an unexplained gap.
 * The server marks the row as client-asserted because it did not do the work.
 *
 * Never throws: a trace is bookkeeping, and a turn must not fail because
 * bookkeeping did.
 */
export async function reportLocalToolObservation(obs: LocalToolObservation): Promise<void> {
  try {
    await api.post('/chats/tool-observations', obs);
  } catch {
    /* ignored by design */
  }
}
