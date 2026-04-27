import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export const TASK_STATUSES = [
  'backlog', 'todo', 'in_progress', 'in_review', 'blocked', 'done', 'cancelled',
] as const;

export const TASK_STATUS_LABELS: Record<string, string> = {
  backlog: 'Backlog',
  open: 'Open',
  todo: 'To Do',
  in_progress: 'In Progress',
  in_review: 'In Review',
  blocked: 'Blocked',
  review: 'Review',
  completed: 'Completed',
  done: 'Done',
  cancelled: 'Cancelled',
};

export const TASK_PRIORITIES = ['critical', 'high', 'medium', 'low'] as const;

export const TASK_PRIORITY_LABELS: Record<string, string> = {
  critical: 'Critical',
  high: 'High',
  medium: 'Medium',
  low: 'Low',
};

export interface Task {
  id: string;
  organization_id: string;
  project_id: string;
  goal_id: string;
  parent_id: string;
  assigned_agent_id: string;
  identifier: string;
  title: string;
  description: string;
  status: string;
  priority_level: string;
  priority: number;
  result: string;
  billing_code: string;
  request_depth: number;
  // Per-task max iterations override. 0 = use the agent's default.
  // The iteration counter always starts fresh at 0 for each task run.
  max_iterations?: number;
  checked_out_by: string;
  checked_out_at: string;
  started_at: string;
  completed_at: string;
  cancelled_at: string;
  hidden_at: string;
  created_at: string;
  updated_at: string;
  created_by: string;
  updated_by: string;
}

export interface TaskWithSubtasks extends Task {
  sub_tasks?: TaskWithSubtasks[];
}

export async function listTasks(params?: ListParams): Promise<ListResult<Task>> {
  const res = await api.get<ListResult<Task>>('/tasks', { params });
  return res.data;
}

export async function getTask(id: string): Promise<Task> {
  const res = await api.get<Task>(`/tasks/${id}`);
  return res.data;
}

export async function getTaskWithSubtasks(id: string): Promise<TaskWithSubtasks> {
  const res = await api.get<TaskWithSubtasks>(`/tasks/${id}`, {
    params: { include: 'subtasks' },
  });
  return res.data;
}

export async function createTask(data: Partial<Task>): Promise<Task> {
  const res = await api.post<Task>('/tasks', data);
  return res.data;
}

export async function updateTask(id: string, data: Partial<Task>): Promise<Task> {
  const res = await api.put<Task>(`/tasks/${id}`, data);
  return res.data;
}

export async function deleteTask(id: string): Promise<void> {
  await api.delete(`/tasks/${id}`);
}

export async function checkoutTask(id: string, agentId: string): Promise<void> {
  await api.post(`/tasks/${id}/checkout`, { agent_id: agentId });
}

export async function releaseTask(id: string): Promise<void> {
  await api.post(`/tasks/${id}/release`);
}

export async function processTask(id: string): Promise<{ id: string; status: string }> {
  const res = await api.post<{ id: string; status: string }>(`/tasks/${id}/process`);
  return res.data;
}

export async function createTaskChat(id: string): Promise<{ id: string; agent_id: string; task_id: string; organization_id: string; name: string }> {
  const res = await api.post(`/tasks/${id}/chat`);
  return res.data;
}

export async function cancelTaskDelegation(id: string): Promise<{ message: string; task_id: string }> {
  const res = await api.post<{ message: string; task_id: string }>(`/tasks/${id}/cancel`);
  return res.data;
}

export interface ActiveDelegation {
  task_id: string;
  agent_id: string;
  org_id: string;
  started_at: string;
  duration: string;
}

export async function listActiveDelegations(): Promise<{ delegations: ActiveDelegation[] }> {
  const res = await api.get<{ delegations: ActiveDelegation[] }>('/active-delegations');
  return res.data;
}

export async function listTasksByAgent(agentId: string, params?: ListParams): Promise<ListResult<Task>> {
  const res = await api.get<ListResult<Task>>(`/agents/${agentId}/tasks`, { params });
  return res.data;
}
