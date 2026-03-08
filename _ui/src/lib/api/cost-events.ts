import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export interface CostEvent {
  id: string;
  organization_id: string;
  agent_id: string;
  task_id: string;
  project_id: string;
  goal_id: string;
  billing_code: string;
  run_id: string;
  provider: string;
  model: string;
  input_tokens: number;
  output_tokens: number;
  cost_cents: number;
  created_at: string;
}

export interface CostSummary {
  key: string;
  total_cost_cents: number;
  total_input_tokens: number;
  total_output_tokens: number;
  event_count: number;
}

export async function listCostEvents(params?: ListParams): Promise<ListResult<CostEvent>> {
  const res = await api.get<ListResult<CostEvent>>('/cost-events', { params });
  return res.data;
}

export async function recordCostEvent(data: Partial<CostEvent>): Promise<CostEvent> {
  const res = await api.post<CostEvent>('/cost-events', data);
  return res.data;
}

export async function getCostByBillingCode(params?: ListParams): Promise<CostSummary[]> {
  const res = await api.get<CostSummary[]>('/cost-events/by-billing-code', { params });
  return res.data;
}

export async function getCostByAgent(agentId: string): Promise<CostSummary> {
  const res = await api.get<CostSummary>(`/agents/${agentId}/cost`);
  return res.data;
}

export async function getCostByProject(projectId: string): Promise<CostSummary> {
  const res = await api.get<CostSummary>(`/projects/${projectId}/cost`);
  return res.data;
}

export async function getCostByGoal(goalId: string): Promise<CostSummary> {
  const res = await api.get<CostSummary>(`/goals/${goalId}/cost`);
  return res.data;
}
