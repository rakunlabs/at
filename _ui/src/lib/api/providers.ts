import axios from 'axios';

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

interface ProvidersResponse {
  providers: ProviderRecord[];
}

export async function listProviders(): Promise<ProviderRecord[]> {
  const res = await api.get<ProvidersResponse>('/providers');
  return res.data.providers;
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
