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

export interface Connection {
  id: string;
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
  metadata?: Record<string, unknown>;
}

export interface UpdateConnectionInput extends CreateConnectionInput {}

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
