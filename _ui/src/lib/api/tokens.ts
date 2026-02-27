import axios from 'axios';

const api = axios.create({
  baseURL: 'api/v1',
});

export interface APIToken {
  id: string;
  name: string;
  token_prefix: string;
  allowed_providers: string[] | null;
  allowed_models: string[] | null;
  allowed_webhooks: string[] | null;
  expires_at: string | null;
  created_at: string;
  last_used_at: string | null;
  created_by: string;
}

export interface CreateTokenRequest {
  name: string;
  allowed_providers?: string[];
  allowed_models?: string[];
  allowed_webhooks?: string[];
  expires_at?: string; // RFC3339 timestamp, empty/omitted = no expiry
}

export interface UpdateTokenRequest {
  name: string;
  allowed_providers?: string[];
  allowed_models?: string[];
  allowed_webhooks?: string[];
  expires_at?: string; // RFC3339 timestamp, empty/omitted = no expiry
}

export interface CreateTokenResponse {
  token: string; // full token — shown only once
  info: APIToken;
}

interface TokensResponse {
  tokens: APIToken[];
}

export async function listTokens(): Promise<APIToken[]> {
  const res = await api.get<TokensResponse>('/api-tokens');
  return res.data.tokens;
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
