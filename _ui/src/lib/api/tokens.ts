import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({
  baseURL: 'api/v1',
});

export interface APIToken {
  id: string;
  name: string;
  token_prefix: string;
  allowed_providers_mode: string;
  allowed_providers: string[] | null;
  allowed_models_mode: string;
  allowed_models: string[] | null;
  allowed_webhooks_mode: string;
  allowed_webhooks: string[] | null;
  allowed_rag_mcps_mode: string;
  allowed_rag_mcps: string[] | null;
  expires_at: string | null;
  total_token_limit: number | null;
  limit_reset_interval: string | null;
  last_reset_at: string | null;
  created_at: string;
  last_used_at: string | null;
  created_by: string;
}

export interface TokenUsage {
  token_id: string;
  model: string;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  request_count: number;
  last_request_at: string;
}

export interface CreateTokenRequest {
  name: string;
  allowed_providers_mode?: string;
  allowed_providers?: string[];
  allowed_models_mode?: string;
  allowed_models?: string[];
  allowed_webhooks_mode?: string;
  allowed_webhooks?: string[];
  allowed_rag_mcps_mode?: string;
  allowed_rag_mcps?: string[];
  expires_at?: string; // RFC3339 timestamp, empty/omitted = no expiry
  total_token_limit?: number;
  limit_reset_interval?: string; // "daily", "weekly", "monthly"
}

export interface UpdateTokenRequest {
  name: string;
  allowed_providers_mode?: string;
  allowed_providers?: string[];
  allowed_models_mode?: string;
  allowed_models?: string[];
  allowed_webhooks_mode?: string;
  allowed_webhooks?: string[];
  allowed_rag_mcps_mode?: string;
  allowed_rag_mcps?: string[];
  expires_at?: string; // RFC3339 timestamp, empty/omitted = no expiry
  total_token_limit?: number;
  limit_reset_interval?: string; // "daily", "weekly", "monthly"
}

export interface CreateTokenResponse {
  token: string; // full token — shown only once
  info: APIToken;
}

export async function listTokens(params?: ListParams): Promise<ListResult<APIToken>> {
  const res = await api.get<ListResult<APIToken>>('/api-tokens', { params });
  return res.data;
}

export async function createToken(req: CreateTokenRequest): Promise<CreateTokenResponse> {
  const res = await api.post<CreateTokenResponse>('/api-tokens', req);
  return res.data;
}

export async function deleteToken(id: string): Promise<void> {
  await api.delete(`/api-tokens/${id}`);
}

export async function updateToken(id: string, req: UpdateTokenRequest): Promise<APIToken> {
  const res = await api.put<APIToken>(`/api-tokens/${id}`, req);
  return res.data;
}

export async function getTokenUsage(id: string): Promise<TokenUsage[]> {
  const res = await api.get<TokenUsage[]>(`/api-tokens/${id}/usage`);
  return res.data;
}

export async function resetTokenUsage(id: string): Promise<void> {
  await api.post(`/api-tokens/${id}/usage/reset`);
}
