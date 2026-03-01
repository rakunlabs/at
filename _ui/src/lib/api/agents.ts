import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

export interface Agent {
  id: string;
  name: string;
  description: string;
  provider: string;
  model: string;
  system_prompt: string;
  skills: string[];
  mcp_urls: string[];
  max_iterations: number;
  tool_timeout: number;
  created_at: string;
  updated_at: string;
  created_by: string;
  updated_by: string;
}

export async function listAgents(): Promise<Agent[]> {
  const res = await api.get<{ agents: Agent[] }>('/agents');
  return res.data.agents || [];
}

export async function getAgent(id: string): Promise<Agent> {
  const res = await api.get<Agent>(`/agents/${id}`);
  return res.data;
}

export async function createAgent(data: Partial<Agent>): Promise<Agent> {
  const res = await api.post<Agent>('/agents', data);
  return res.data;
}

export async function updateAgent(id: string, data: Partial<Agent>): Promise<Agent> {
  const res = await api.put<Agent>(`/agents/${id}`, data);
  return res.data;
}

export async function deleteAgent(id: string): Promise<void> {
  await api.delete(`/agents/${id}`);
}
