import axios from 'axios';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

/** Minimal reference to an agent that uses a connection. */
export interface ConnectionAgentRef {
  id: string;
  name: string;
  level: 'agent' | 'skill';
}

/**
 * Credential fields returned by the API. Secrets are redacted by default:
 * `*_set` flags indicate whether a value is stored. Pass `?reveal=true` on
 * GET to receive the actual values (in `client_secret`, `refresh_token`, etc.).
 */
export interface ConnectionCredentials {
  client_id?: string;
  client_secret_set?: boolean;
  client_secret?: string;
  refresh_token_set?: boolean;
  refresh_token?: string;
  api_key_set?: boolean;
  api_key?: string;
  extra_keys_set?: string[];
  extra?: Record<string, string>;
}

export type ConnectionScope = 'personal' | 'workspace';

/** State of an MCP OAuth authorization. Never carries tokens. */
export interface ConnectionMCPOAuth {
  mcp_url: string;
  issuer: string;
  scopes?: string[];
  expires_at?: string;
  needs_reauth?: boolean;
}

export interface Connection {
  id: string;
  scope: ConnectionScope;
  owner_user_id?: string;
  mcp_oauth?: ConnectionMCPOAuth;
  provider: string;
  name: string;
  account_label?: string;
  description?: string;
  credentials: ConnectionCredentials;
  metadata?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
  created_by?: string;
  updated_by?: string;
  used_by_agents?: ConnectionAgentRef[];
}

export interface CreateConnectionInput {
  provider: string;
  name: string;
  account_label?: string;
  description?: string;
  credentials?: {
    client_id?: string;
    client_secret?: string;
    refresh_token?: string;
    api_key?: string;
    extra?: Record<string, string>;
  };
  /** Connector-driven dynamic credential map keyed by full variable name
   *  (e.g. {"spotify_client_id": "..."}). Merged into credentials server-side. */
  fields?: Record<string, string>;
  metadata?: Record<string, unknown>;
  /** Set on create only; personal connections belong to the signed-in account. */
  scope?: ConnectionScope;
}

export interface UpdateConnectionInput extends Omit<CreateConnectionInput, 'scope'> {}

// ─── CRUD ───

export async function listConnections(provider?: string): Promise<Connection[]> {
  const res = await api.get<Connection[]>('/connections', {
    params: provider ? { provider } : undefined,
  });
  return res.data ?? [];
}

export async function getConnection(id: string, reveal = false): Promise<Connection> {
  const res = await api.get<Connection>(`/connections/${encodeURIComponent(id)}`, {
    params: reveal ? { reveal: 'true' } : undefined,
  });
  return res.data;
}

export async function createConnection(input: CreateConnectionInput): Promise<Connection> {
  const res = await api.post<Connection>('/connections', input);
  return res.data;
}

export async function updateConnection(id: string, input: UpdateConnectionInput): Promise<Connection> {
  const res = await api.put<Connection>(`/connections/${encodeURIComponent(id)}`, input);
  return res.data;
}

export interface DeleteConnectionResult {
  status?: string;
  detached_from_agents?: number;
  error?: string;
  used_by_agents?: ConnectionAgentRef[];
  hint?: string;
}

export async function deleteConnection(id: string, force = false): Promise<DeleteConnectionResult> {
  const res = await api.delete<DeleteConnectionResult>(`/connections/${encodeURIComponent(id)}`, {
    params: force ? { force: 'true' } : undefined,
    validateStatus: () => true,
  });
  if (res.status >= 200 && res.status < 300) {
    return res.data;
  }
  // 409: return conflict info so the UI can offer force-delete.
  if (res.status === 409) {
    return res.data;
  }
  throw new Error((res.data as { error?: string })?.error ?? `delete failed: ${res.status}`);
}

export interface ImportConnectionsResult {
  created: Connection[];
  skipped: { provider: string; reason: string }[];
}

export async function importConnectionsFromVariables(): Promise<ImportConnectionsResult> {
  const res = await api.post<ImportConnectionsResult>('/connections/import-from-variables');
  return res.data;
}

// ─── MCP OAuth ───

export interface MCPOAuthStartInput {
  set_id?: string;
  server_id?: string;
  upstream_index?: number;
  /** Renew an existing MCP account instead of naming an upstream. */
  connection_id?: string;
  target?: 'personal' | 'shared';
  connection_name?: string;
  client_secret?: string;
}

export async function startMCPOAuth(input: MCPOAuthStartInput): Promise<{ authorize_url: string; provider: string }> {
  const res = await api.post<{ authorize_url: string; provider: string }>('/mcp/oauth/start', input);
  return res.data;
}

export interface MCPOAuthResult { type: 'at-mcp-oauth-result'; ok: boolean; message: string; connection_id?: string; state?: string }

/** An OAuth upstream of an MCP set the caller may use, with their own account for it. */
export interface MCPOAuthAccountTarget {
  set_id: string;
  set_name: string;
  set_scope: 'personal' | 'workspace';
  upstream_index: number;
  provider: string;
  /** scheme://host only. */
  server: string;
  accounts: ('user' | 'agent' | 'shared')[];
  account?: { connection_id: string; name: string; label?: string; needs_reauth?: boolean };
  shared?: { connection_id: string };
}

export async function listMCPOAuthAccounts(): Promise<MCPOAuthAccountTarget[]> {
  const res = await api.get<MCPOAuthAccountTarget[]>('/mcp/oauth/accounts');
  return res.data ?? [];
}

export function validMCPOAuthMessage(event: Pick<MessageEvent, 'origin' | 'source' | 'data'>, popup: Window | null, origin: string): event is MessageEvent<MCPOAuthResult> {
  return event.origin === origin && !!popup && event.source === popup && event.data?.type === 'at-mcp-oauth-result' && typeof event.data.ok === 'boolean';
}

/**
 * Opens the authorization popup and resolves with the callback's result.
 * Call it directly from a click handler: opening after an await loses the
 * user activation browsers require for popups.
 */
export function connectMCPAccount(input: MCPOAuthStartInput): Promise<MCPOAuthResult> {
  const popup = window.open('about:blank', '_blank', 'popup,width=560,height=760');
  if (!popup) return Promise.reject(new Error('Allow popups for this site, then try again.'));
  popup.document.title = 'Connecting your MCP account…';
  return new Promise((resolve, reject) => {
    let settled = false;
    let state = '';
    let channel: BroadcastChannel | undefined;
    const finish = (error?: Error, result?: MCPOAuthResult) => {
      if (settled) return;
      settled = true;
      clearInterval(timer); clearTimeout(timeout); window.removeEventListener('message', receive);
      channel?.close();
      if (!popup.closed) popup.close();
      if (error) reject(error); else resolve(result!);
    };
    const receive = (event: MessageEvent) => {
      if (!validMCPOAuthMessage(event, popup, location.origin)) return;
      if (event.data.state && event.data.state !== state) return;
      if (event.data.ok) finish(undefined, event.data);
      else finish(new Error(event.data.message || 'Authorization failed.'));
    };
    // COOP can make the original WindowProxy report closed while the popup
    // is still authorizing. When the fallback is available, await its result
    // (or the ceremony deadline), not that unreliable closed flag.
    const timer = window.setInterval(() => { if (popup.closed && !channel) finish(new Error('The authorization window closed before completion.')); }, 500);
    const timeout = window.setTimeout(() => finish(new Error('Authorization expired. Start again.')), 600_000);
    window.addEventListener('message', receive);
    startMCPOAuth(input).then(({ authorize_url }) => {
      if (settled) return;
      const url = new URL(authorize_url);
      if (url.protocol !== 'https:' && url.protocol !== 'http:') throw new Error('Invalid authorization URL');
      state = url.searchParams.get('state') || '';
      if (state && typeof BroadcastChannel !== 'undefined') {
        try {
          channel = new BroadcastChannel(`at-mcp-oauth:${state}`);
          channel.onmessage = (event: MessageEvent<MCPOAuthResult>) => {
            const result = event.data;
            // BroadcastChannel is same-origin, but concurrent ceremonies
            // must never settle each other's promise.
            if (result?.type !== 'at-mcp-oauth-result' || result.state !== state || typeof result.ok !== 'boolean') return;
            if (result.ok) finish(undefined, result);
            else finish(new Error(result.message || 'Authorization failed.'));
          };
        } catch { /* Keep the opener path when this browser denies channels. */ }
      }
      popup.location.replace(url.href);
    }).catch((e: any) => finish(new Error(e?.response?.data?.message || e?.message || 'Cannot start the authorization.')));
  });
}

// ─── OAuth helpers ───

/** Returns the URL used to start an OAuth flow for a named connection. */
export function getOAuthStartURLForConnection(connectionID: string, provider: string): string {
  const params = new URLSearchParams({
    provider,
    connection_id: connectionID,
    redirect: 'true',
  });
  return `api/v1/oauth/start?${params.toString()}`;
}

export interface ManualAuthURL {
  url: string;
  redirect_uri: string;
  provider: string;
  connection_id?: string;
}

export async function getManualAuthURL(provider: string, connectionID?: string): Promise<ManualAuthURL> {
  const res = await api.get<ManualAuthURL>('/oauth/manual-url', {
    params: connectionID ? { provider, connection_id: connectionID } : { provider },
  });
  return res.data;
}

export async function exchangeCode(
  provider: string,
  code: string,
  redirectUri: string,
  connectionID?: string,
): Promise<{ status: string; connection_id?: string; message: string }> {
  const res = await api.post<{ status: string; connection_id?: string; message: string }>(
    '/oauth/exchange',
    {
      provider,
      code,
      redirect_uri: redirectUri,
      connection_id: connectionID,
    },
  );
  return res.data;
}

// ─── Legacy (flat per-provider view) ───
// Kept for pages that still render the old flat list (e.g. the settings
// import flow). New code should use listConnections() instead.

export interface LegacyConnectionVar {
  key: string;
  description: string;
  secret: boolean;
  set: boolean;
}

export interface LegacyConnection {
  provider: string;
  name: string;
  description: string;
  connected: boolean;
  type: 'oauth' | 'token';
  setup_complete: boolean;
  required_variables?: LegacyConnectionVar[];
  oauth_provider?: string;
}

export async function listLegacyConnections(): Promise<LegacyConnection[]> {
  const res = await api.get<LegacyConnection[]>('/oauth/connections');
  return res.data;
}

export async function disconnectLegacyProvider(provider: string): Promise<void> {
  await api.delete(`/oauth/connections/${encodeURIComponent(provider)}`);
}

export async function saveVariable(data: {
  key: string;
  value: string;
  description?: string;
  secret?: boolean;
}): Promise<void> {
  await api.post('/variables', data);
}
