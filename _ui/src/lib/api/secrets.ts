import axios from 'axios';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

export interface Secret {
  id: string;
  key: string;
  value: string; // redacted as "***" in list responses
  description: string;
  created_at: string;
  updated_at: string;
}

interface SecretsResponse {
  secrets: Secret[];
}

// ─── API Functions ───

export async function listSecrets(): Promise<Secret[]> {
  const res = await api.get<SecretsResponse>('/secrets');
  return res.data.secrets;
}
