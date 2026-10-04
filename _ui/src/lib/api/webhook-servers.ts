import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

export interface WebhookServerStatus {
  state: 'running' | 'stopped' | 'error' | 'disabled';
  error?: string;
  address?: string;
  since?: string;
}

export interface WebhookServer {
  id: string;
  name: string;
  description: string;
  bind_host: string;
  port: number;
  base_path: string;
  enabled: boolean;
  all_workspaces: boolean;
  workspace_ids: string[];
  /** PEM certificate. For workspace members only '***' (TLS on) or ''. */
  tls_cert: string;
  /** Write-only: reads return '***'; sending '***' keeps the stored key. */
  tls_key: string;
  allowed_cidrs: string[];
  max_body_bytes: number;
  rate_limit_per_minute: number;
  /** How callers reach this listener; used for copyable URLs. */
  public_url: string;
  created_at?: string;
  updated_at?: string;
  status?: WebhookServerStatus;
}

export async function listWebhookServers(): Promise<WebhookServer[]> {
  const res = await api.get<{ servers: WebhookServer[] }>('webhook-servers');
  return res.data.servers ?? [];
}

export async function createWebhookServer(server: Partial<WebhookServer>): Promise<WebhookServer> {
  return (await api.post<WebhookServer>('webhook-servers', server)).data;
}

export async function updateWebhookServer(id: string, server: Partial<WebhookServer>): Promise<WebhookServer> {
  return (await api.put<WebhookServer>(`webhook-servers/${encodeURIComponent(id)}`, server)).data;
}

export async function deleteWebhookServer(id: string): Promise<void> {
  await api.delete(`webhook-servers/${encodeURIComponent(id)}`);
}

export async function reloadWebhookServers(): Promise<WebhookServer[]> {
  const res = await api.post<{ servers: WebhookServer[] }>('webhook-servers/reload');
  return res.data.servers ?? [];
}

/**
 * Base URL of a dedicated listener. An explicit public URL wins; otherwise the
 * listener is assumed reachable on the current host at its own port.
 */
export function webhookServerBaseUrl(s: Pick<WebhookServer, 'public_url' | 'port' | 'base_path' | 'tls_cert' | 'bind_host'>, location: { protocol: string; hostname: string } = window.location): string {
  if (s.public_url) return s.public_url.replace(/\/+$/, '');
  const scheme = s.tls_cert ? 'https' : 'http';
  const bound = s.bind_host && !['0.0.0.0', '::', ''].includes(s.bind_host) ? s.bind_host : location.hostname;
  const host = bound.includes(':') && !bound.startsWith('[') ? `[${bound}]` : bound;
  return `${scheme}://${host}:${s.port}${s.base_path || ''}`;
}

export function webhookServerUrl(s: Parameters<typeof webhookServerBaseUrl>[0], path: string, location?: { protocol: string; hostname: string }): string {
  return `${webhookServerBaseUrl(s, location)}/${path.replace(/^\/+/, '')}`;
}
