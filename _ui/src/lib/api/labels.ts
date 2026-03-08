import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export interface Label {
  id: string;
  organization_id: string;
  name: string;
  color: string;
  created_at: string;
  updated_at: string;
}

export async function listLabels(params?: ListParams): Promise<ListResult<Label>> {
  const res = await api.get<ListResult<Label>>('/labels', { params });
  return res.data;
}

export async function getLabel(id: string): Promise<Label> {
  const res = await api.get<Label>(`/labels/${id}`);
  return res.data;
}

export async function createLabel(data: Partial<Label>): Promise<Label> {
  const res = await api.post<Label>('/labels', data);
  return res.data;
}

export async function updateLabel(id: string, data: Partial<Label>): Promise<Label> {
  const res = await api.put<Label>(`/labels/${id}`, data);
  return res.data;
}

export async function deleteLabel(id: string): Promise<void> {
  await api.delete(`/labels/${id}`);
}

export async function listTasksForLabel(labelId: string): Promise<any[]> {
  const res = await api.get(`/labels/${labelId}/tasks`);
  return res.data;
}

export async function addLabelToTask(taskId: string, labelId: string): Promise<void> {
  await api.post(`/tasks/${taskId}/labels/${labelId}`);
}

export async function removeLabelFromTask(taskId: string, labelId: string): Promise<void> {
  await api.delete(`/tasks/${taskId}/labels/${labelId}`);
}

export async function listLabelsForTask(taskId: string): Promise<Label[]> {
  const res = await api.get<Label[]>(`/tasks/${taskId}/labels`);
  return res.data;
}
