import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({
  baseURL: 'api/v1',
});

export interface RateLimitConfig {
  // Token-bucket rate (0 = unlimited).
  requests_per_minute?: number;
  // Weighted input-tokens-per-minute bucket (0 = unlimited).
  input_tokens_per_minute?: number;
  // Concurrency cap for in-flight requests (0 = unlimited).
  max_concurrent?: number;
  // How long Acquire blocks before failing fast (0 = default 60s).
  wait_timeout_ms?: number;
  // Cap on upstream Retry-After honoured by the agent retry loop.
  // 0 = default 60s, -1 = no cap, >0 = ms.
  retry_after_cap_ms?: number;
}

export interface LLMConfig {
  type: string;
  shared_with_all_workspaces?: boolean;
  // Availability, managed only through setProviderDisabled: a disabled provider
  // keeps its credentials but is hidden from model lists and refuses requests.
  // Deliberately not sent by the editor, so a config save cannot resume it.
  disabled?: boolean;
  api_key?: string;
  // Google service-account key file, verbatim, for the vertex / vertex-gemini
  // types. Encrypted at rest and redacted to "***" on read, so a provider that
  // has one reads back the sentinel rather than the key. Empty means the
  // provider falls back to the server's Application Default Credentials.
  credentials_json?: string;
  base_url?: string;
  model: string;
  models?: string[];
  embedding_models?: string[];
  extra_headers?: Record<string, string>;
  auth_type?: string;
  // OAuth refresh token managed by provider auth flows; redacted by the server.
  refresh_token?: string;
  // Absolute access-token expiry (RFC3339). Managed by the OAuth flow; lets
  // the gateway refresh proactively across restarts.
  token_expires_at?: string;
  proxy?: string;
  insecure_skip_verify?: boolean;
  rate_limit?: RateLimitConfig;
}

export interface ProviderRecord {
  workspace_id?: string;
  owner_user_id?: string;
  id: string;
  key: string;
  display_key?: string;
  reference?: string;
  scope?: ProviderScope;
  config: LLMConfig;
  created_at: string;
  updated_at: string;
}

export type ProviderScope = 'personal' | 'workspace' | 'global';

export async function listProviders(params?: ListParams): Promise<ListResult<ProviderRecord>> {
  const res = await api.get<ListResult<ProviderRecord>>('/providers', { params });
  return res.data;
}

export async function getProvider(key: string): Promise<ProviderRecord> {
  const res = await api.get<ProviderRecord>(`/providers/${key}`);
  return res.data;
}

export async function createProvider(key: string, config: LLMConfig): Promise<ProviderRecord> {
  const res = await api.post<ProviderRecord>('/providers', { key, config });
  return res.data;
}

// clearCredentialsJSON removes a stored Google service-account key and returns
// the provider to Application Default Credentials. It is a separate flag
// because an omitted credentials_json preserves the stored one — the editor
// only ever holds the redaction sentinel, so "empty means delete" would wipe
// the key on any unrelated edit.
export async function updateProvider(
  key: string,
  config: LLMConfig,
  clearCredentialsJSON = false,
): Promise<ProviderRecord> {
  const body: Record<string, any> = { config };
  if (clearCredentialsJSON) body.clear_credentials_json = true;
  const res = await api.put<ProviderRecord>(`/providers/${key}`, body);
  return res.data;
}

export async function deleteProvider(key: string): Promise<void> {
  await api.delete(`/providers/${key}`);
}

export async function setProviderDisabled(key: string, disabled: boolean): Promise<{ disabled: boolean }> {
  const res = await api.put<{ disabled: boolean }>(`/providers/${key}/disable`, { disabled });
  return res.data;
}

export async function listPersonalProviders(params?: ListParams): Promise<ListResult<ProviderRecord>> {
  const res = await api.get<ListResult<ProviderRecord>>('/personal-providers', { params });
  return res.data;
}

export async function createPersonalProvider(key: string, config: LLMConfig, scope: ProviderScope = 'personal'): Promise<ProviderRecord> {
  const res = await api.post<ProviderRecord>('/personal-providers', { key, config, scope });
  return res.data;
}

export async function updatePersonalProvider(id: string, key: string, config: LLMConfig, clearCredentialsJSON = false): Promise<ProviderRecord> {
  const body: Record<string, any> = { key, config };
  if (clearCredentialsJSON) body.clear_credentials_json = true;
  const res = await api.put<ProviderRecord>(`/personal-providers/${encodeURIComponent(id)}`, body);
  return res.data;
}

export async function setPersonalProviderScope(id: string, scope: ProviderScope): Promise<ProviderRecord> {
  const res = await api.put<ProviderRecord>(`/personal-providers/${encodeURIComponent(id)}/scope`, { scope });
  return res.data;
}

export async function setPersonalProviderDisabled(id: string, disabled: boolean): Promise<{ disabled: boolean }> {
  const res = await api.put<{ disabled: boolean }>(`/personal-providers/${encodeURIComponent(id)}/disable`, { disabled });
  return res.data;
}

export async function deletePersonalProvider(id: string): Promise<void> {
  await api.delete(`/personal-providers/${encodeURIComponent(id)}`);
}

interface DiscoverModelsResponse {
  models: string[];
}

export async function discoverModels(config: Partial<LLMConfig>, key?: string): Promise<string[]> {
  const body: Record<string, any> = { config };
  if (key) body.key = key;
  const res = await api.post<DiscoverModelsResponse>('/providers/discover-models', body);
  return res.data.models;
}

export async function discoverEmbeddingModels(config: Partial<LLMConfig>, key?: string): Promise<string[]> {
  const body: Record<string, any> = { config };
  if (key) body.key = key;
  const res = await api.post<DiscoverModelsResponse>('/providers/discover-embedding-models', body);
  return res.data.models;
}

export async function discoverPersonalModels(config: Partial<LLMConfig>, providerId?: string): Promise<string[]> {
  const body: Record<string, any> = { config };
  if (providerId) body.provider_id = providerId;
  const res = await api.post<DiscoverModelsResponse>('/personal-providers/discover-models', body);
  return res.data.models;
}

export async function discoverPersonalEmbeddingModels(config: Partial<LLMConfig>, providerId?: string): Promise<string[]> {
  const body: Record<string, any> = { config };
  if (providerId) body.provider_id = providerId;
  const res = await api.post<DiscoverModelsResponse>('/personal-providers/discover-embedding-models', body);
  return res.data.models;
}

// ─── Device Auth (subscription-backed provider device flows) ───

export interface DeviceAuthResponse {
  user_code: string;
  verification_uri: string;
  expires_in: number;
  interval: number;
}

export interface DeviceAuthStatusResponse {
  status: 'pending' | 'authorized' | 'expired' | 'error' | 'none';
  error?: string;
}

export async function startDeviceAuth(key: string, personal = false): Promise<DeviceAuthResponse> {
  const res = await api.post<DeviceAuthResponse>(personal ? '/personal-providers/device-auth' : '/providers/device-auth', { key });
  return res.data;
}

export async function getDeviceAuthStatus(key: string, personal = false): Promise<DeviceAuthStatusResponse> {
  const res = await api.get<DeviceAuthStatusResponse>(personal ? '/personal-providers/device-auth-status' : '/providers/device-auth-status', {
    params: { key },
  });
  return res.data;
}

// ─── Claude Auth (Anthropic OAuth Authorization Code + PKCE) ───

export interface ClaudeAuthStartResponse {
  auth_url: string;
  expires_in: number;
}

export interface ClaudeAuthCallbackResponse {
  status: 'authorized';
}

export async function startClaudeAuth(key: string, personal = false): Promise<ClaudeAuthStartResponse> {
  const res = await api.post<ClaudeAuthStartResponse>(personal ? '/personal-providers/claude-auth' : '/providers/claude-auth', { key });
  return res.data;
}

export async function submitClaudeAuthCode(key: string, code: string, personal = false): Promise<ClaudeAuthCallbackResponse> {
  const res = await api.post<ClaudeAuthCallbackResponse>(personal ? '/personal-providers/claude-auth/callback' : '/providers/claude-auth/callback', { key, code });
  return res.data;
}

// ─── Claude Auth Token Paste ───

export async function submitClaudeAuthToken(
  key: string,
  access_token: string,
  refresh_token: string,
  personal = false,
): Promise<ClaudeAuthCallbackResponse> {
  const res = await api.post<ClaudeAuthCallbackResponse>(personal ? '/personal-providers/claude-auth/token' : '/providers/claude-auth/token', {
    key,
    access_token,
    refresh_token,
  });
  return res.data;
}

// ─── Claude Auth Sync from CLI ───

export interface ClaudeAuthSyncResponse {
  status: 'authorized';
  source: string;
  expires_at?: string; // RFC3339 token expiry time (if known from CLI credentials)
}

export async function syncClaudeAuthFromCLI(key: string, personal = false): Promise<ClaudeAuthSyncResponse> {
  const res = await api.post<ClaudeAuthSyncResponse>(personal ? '/personal-providers/claude-auth/sync' : '/providers/claude-auth/sync', { key });
  return res.data;
}
