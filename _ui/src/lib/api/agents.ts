import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export interface AgentConfig {
  description: string;
  provider: string;
  model: string;
  system_prompt: string;
  skills: string[];
  mcp_sets: string[];
  mcp_urls: string[];
  builtin_tools: string[];
  max_iterations: number;
  tool_timeout: number;
  confirmation_required_tools?: string[];
  heartbeat_schedule?: string;
}

export interface Agent {
  id: string;
  name: string;
  config: AgentConfig;
  created_at: string;
  updated_at: string;
  created_by: string;
  updated_by: string;
}

export async function listAgents(params?: ListParams): Promise<ListResult<Agent>> {
  const res = await api.get<ListResult<Agent>>('/agents', { params });
  return res.data;
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
