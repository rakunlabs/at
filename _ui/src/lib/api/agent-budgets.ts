import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export interface AgentBudget {
  id: string;
  agent_id: string;
  monthly_limit: number;
  current_spend: number;
  period_start: string;
  period_end: string;
  created_at: string;
  updated_at: string;
}

export interface AgentUsageRecord {
  id: string;
  agent_id: string;
  task_id: string;
  workflow_run_id: string;
  session_id: string;
  model: string;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  estimated_cost: number;
  created_at: string;
}

export interface ModelPricing {
  id: string;
  provider_key: string;
  model: string;
  prompt_price_per_1m: number;
  completion_price_per_1m: number;
  created_at: string;
  updated_at: string;
}

export interface AgentSpend {
  total_spend: number;
}

export async function getAgentBudget(agentId: string): Promise<AgentBudget | null> {
  const res = await api.get<AgentBudget>(`/agents/${agentId}/budget`);
  return res.data;
}

export async function setAgentBudget(agentId: string, data: Partial<AgentBudget>): Promise<void> {
  await api.put(`/agents/${agentId}/budget`, data);
}

export async function getAgentUsage(agentId: string, params?: ListParams): Promise<ListResult<AgentUsageRecord>> {
  const res = await api.get<ListResult<AgentUsageRecord>>(`/agents/${agentId}/usage`, { params });
  return res.data;
}

export async function getAgentSpend(agentId: string): Promise<AgentSpend> {
  const res = await api.get<AgentSpend>(`/agents/${agentId}/spend`);
  return res.data;
}

export async function listModelPricing(): Promise<ModelPricing[]> {
  const res = await api.get<ModelPricing[]>('/model-pricing');
  return res.data;
}

export async function setModelPricing(data: Partial<ModelPricing>): Promise<void> {
  await api.post('/model-pricing', data);
}
