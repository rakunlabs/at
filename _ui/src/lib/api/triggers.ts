import axios from 'axios';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

export interface Trigger {
  id: string;
  workflow_id: string;
  type: 'http' | 'cron';
  config: Record<string, any>;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

interface TriggersResponse {
  triggers: Trigger[];
}

// ─── API Functions ───

export async function listTriggers(workflowId: string): Promise<Trigger[]> {
  const res = await api.get<TriggersResponse>(`/workflows/${workflowId}/triggers`);
  return res.data.triggers;
}

export async function getTrigger(id: string): Promise<Trigger> {
  const res = await api.get<Trigger>(`/triggers/${id}`);
  return res.data;
}

export async function createTrigger(workflowId: string, trigger: Partial<Trigger>): Promise<Trigger> {
  const res = await api.post<Trigger>(`/workflows/${workflowId}/triggers`, trigger);
  return res.data;
}

export async function updateTrigger(id: string, trigger: Partial<Trigger>): Promise<Trigger> {
  const res = await api.put<Trigger>(`/triggers/${id}`, trigger);
  return res.data;
}

export async function deleteTrigger(id: string): Promise<void> {
  await api.delete(`/triggers/${id}`);
}
