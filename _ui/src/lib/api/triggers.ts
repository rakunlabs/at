import axios from 'axios';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

export interface Trigger {
  id: string;
  workflow_id: string;
  target_type: string;  // "workflow" | "rag_sync"
  target_id: string;
  entry_node_id?: string;
  type: 'http' | 'cron';
  config: Record<string, any>;
  alias?: string;
  public: boolean;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  created_by?: string;
  updated_by?: string;
}

interface TriggersResponse {
  triggers: Trigger[];
}

export interface ListTriggersParams {
  type?: 'http' | 'cron';
  target_type?: string;
  target_id?: string;
}

// ─── API Functions ───

export async function listAllTriggers(params?: ListTriggersParams): Promise<Trigger[]> {
  const res = await api.get<TriggersResponse>('/triggers', { params });
  return res.data.triggers ?? [];
}

export async function listTriggers(workflowId: string): Promise<Trigger[]> {
  const res = await api.get<TriggersResponse>(`/workflows/${workflowId}/triggers`);
  return res.data.triggers ?? [];
}

export async function getTrigger(id: string): Promise<Trigger> {
  const res = await api.get<Trigger>(`/triggers/${id}`);
  return res.data;
}

export async function createTrigger(trigger: Partial<Trigger>): Promise<Trigger> {
  const res = await api.post<Trigger>('/triggers', trigger);
  return res.data;
}

/** @deprecated Use createTrigger() with target_type/target_id instead */
export async function createWorkflowTrigger(workflowId: string, trigger: Partial<Trigger>): Promise<Trigger> {
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
