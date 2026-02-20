import axios from 'axios';

const api = axios.create({
  baseURL: '/api/v1',
});

// ─── Info API ───

export interface InfoProvider {
  key: string;
  type: string;
  default_model: string;
  models: string[];
}

export interface InfoResponse {
  providers: InfoProvider[];
  store_type: string;
}

export async function getInfo(): Promise<InfoResponse> {
  const res = await api.get<InfoResponse>('/info');
  return res.data;
}

// ─── Models API ───

export interface ModelData {
  id: string;
  object: string;
  owned_by: string;
}

interface ModelsResponse {
  object: string;
  data: ModelData[];
}

export async function listModels(authToken?: string): Promise<ModelData[]> {
  const headers: Record<string, string> = {};
  if (authToken) {
    headers['Authorization'] = `Bearer ${authToken}`;
  }
  const res = await api.get<ModelsResponse>('/models', { headers });
  return res.data.data;
}
