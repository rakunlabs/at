import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({
  baseURL: 'api/v1',
});

export interface LLMConfig {
  type: string;
  api_key?: string;
  base_url?: string;
  model: string;
  models?: string[];
  extra_headers?: Record<string, string>;
  auth_type?: string;
  proxy?: string;
  insecure_skip_verify?: boolean;
}

export interface ProviderRecord {
  id: string;
  key: string;
  config: LLMConfig;
  created_at: string;
  updated_at: string;
}

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

export async function updateProvider(key: string, config: LLMConfig): Promise<ProviderRecord> {
  const res = await api.put<ProviderRecord>(`/providers/${key}`, { config });
  return res.data;
}

export async function deleteProvider(key: string): Promise<void> {
  await api.delete(`/providers/${key}`);
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

// ─── Device Auth (GitHub OAuth Device Flow) ───

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

export async function startDeviceAuth(key: string): Promise<DeviceAuthResponse> {
  const res = await api.post<DeviceAuthResponse>('/providers/device-auth', { key });
  return res.data;
}

export async function getDeviceAuthStatus(key: string): Promise<DeviceAuthStatusResponse> {
  const res = await api.get<DeviceAuthStatusResponse>('/providers/device-auth-status', {
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

export async function startClaudeAuth(key: string): Promise<ClaudeAuthStartResponse> {
  const res = await api.post<ClaudeAuthStartResponse>('/providers/claude-auth', { key });
  return res.data;
}

export async function submitClaudeAuthCode(key: string, code: string): Promise<ClaudeAuthCallbackResponse> {
  const res = await api.post<ClaudeAuthCallbackResponse>('/providers/claude-auth/callback', { key, code });
  return res.data;
}
