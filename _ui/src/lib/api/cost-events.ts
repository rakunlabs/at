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

// CostByTaskResult is the rolled-up cost across a root task and every
// transitive sub-task. Used by the TaskDetail "View cost" button so a
// pipeline's full spend can be seen at a glance.
export interface CostByTaskResult {
  task_id: string;
  task_count: number;
  task_ids: string[];
  cost_cents: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  event_count: number;
}

export async function getCostByTask(taskId: string): Promise<CostByTaskResult> {
  const res = await api.get<CostByTaskResult>(`/tasks/${taskId}/cost`);
  return res.data;
}
