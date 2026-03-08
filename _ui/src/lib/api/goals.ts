import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export interface Goal {
  id: string;
  organization_id: string;
  parent_goal_id: string;
  name: string;
  description: string;
  status: string;
  priority: number;
  created_at: string;
  updated_at: string;
  created_by: string;
  updated_by: string;
}

export async function listGoals(params?: ListParams): Promise<ListResult<Goal>> {
  const res = await api.get<ListResult<Goal>>('/goals', { params });
  return res.data;
}

export async function getGoal(id: string): Promise<Goal> {
  const res = await api.get<Goal>(`/goals/${id}`);
  return res.data;
}

export async function createGoal(data: Partial<Goal>): Promise<Goal> {
  const res = await api.post<Goal>('/goals', data);
  return res.data;
}

export async function updateGoal(id: string, data: Partial<Goal>): Promise<Goal> {
  const res = await api.put<Goal>(`/goals/${id}`, data);
  return res.data;
}

export async function deleteGoal(id: string): Promise<void> {
  await api.delete(`/goals/${id}`);
}

export async function listGoalChildren(id: string): Promise<Goal[]> {
  const res = await api.get<Goal[]>(`/goals/${id}/children`);
  return res.data;
}

export async function getGoalAncestry(id: string): Promise<Goal[]> {
  const res = await api.get<Goal[]>(`/goals/${id}/ancestry`);
  return res.data;
}
