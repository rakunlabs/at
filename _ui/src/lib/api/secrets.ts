import axios from 'axios';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

export interface Variable {
  id: string;
  key: string;
  value: string; // redacted as "***" in list responses for secret variables
  description: string;
  secret: boolean; // true = encrypted at rest, value redacted in list API
  created_at: string;
  updated_at: string;
}

interface VariablesResponse {
  variables: Variable[];
}

// ─── API Functions ───

export async function listVariables(): Promise<Variable[]> {
  const res = await api.get<VariablesResponse>('/variables');
  return res.data.variables;
}

export async function getVariable(id: string): Promise<Variable> {
  const res = await api.get<Variable>(`/variables/${id}`);
  return res.data;
}

export async function createVariable(data: { key: string; value: string; description?: string; secret?: boolean }): Promise<Variable> {
  const res = await api.post<Variable>('/variables', data);
  return res.data;
}

export async function updateVariable(id: string, data: { key: string; value?: string; description?: string; secret?: boolean }): Promise<Variable> {
  const res = await api.put<Variable>(`/variables/${id}`, data);
  return res.data;
}

export async function deleteVariable(id: string): Promise<void> {
  await api.delete(`/variables/${id}`);
}
