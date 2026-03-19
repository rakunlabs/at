import axios from 'axios';

const api = axios.create({
  baseURL: 'api/v1',
});

// --- Types ---

export interface ConnectionVar {
  key: string;
  description: string;
  secret: boolean;
  set: boolean;
}

export interface Connection {
  provider: string;
  name: string;
  description: string;
  connected: boolean;
  type: 'oauth' | 'token';
  setup_complete: boolean;
  required_variables?: ConnectionVar[];
  oauth_provider?: string;
}

// --- API Functions ---

export async function listConnections(): Promise<Connection[]> {
  const res = await api.get<Connection[]>('/oauth/connections');
  return res.data;
}

export async function disconnectProvider(provider: string): Promise<void> {
  await api.delete(`/oauth/connections/${provider}`);
}

export function getOAuthStartURL(provider: string): string {
  return `api/v1/oauth/start?provider=${encodeURIComponent(provider)}&redirect=true`;
}

export async function saveVariable(data: { key: string; value: string; description?: string; secret?: boolean }): Promise<void> {
  await api.post('/variables', data);
}

export interface ManualAuthURL {
  url: string;
  redirect_uri: string;
  provider: string;
}

export async function getManualAuthURL(provider: string): Promise<ManualAuthURL> {
  const res = await api.get<ManualAuthURL>('/oauth/manual-url', { params: { provider } });
  return res.data;
}

export async function exchangeCode(provider: string, code: string, redirectUri: string): Promise<{ status: string; message: string }> {
  const res = await api.post<{ status: string; message: string }>('/oauth/exchange', {
    provider,
    code,
    redirect_uri: redirectUri,
  });
  return res.data;
}
