import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

export interface WakeupRequest {
  id: string;
  agent_id: string;
  organization_id?: string;
  status: string;
  idempotency_key: string;
  context: Record<string, any>;
  coalesced_count: number;
  run_id: string;
  created_at: string;
  updated_at: string;
}

export async function createWakeupRequest(agentId: string, data: Partial<WakeupRequest>): Promise<WakeupRequest> {
  const res = await api.post<WakeupRequest>(`/agents/${agentId}/wakeup`, data);
  return res.data;
}

export async function listPendingWakeupRequests(agentId: string): Promise<WakeupRequest[]> {
  const res = await api.get<WakeupRequest[]>(`/agents/${agentId}/wakeup-requests`);
  return res.data;
}

export async function promoteDeferredWakeup(agentId: string): Promise<void> {
  await api.post(`/agents/${agentId}/wakeup-requests/promote`);
}

export async function getWakeupRequest(id: string): Promise<WakeupRequest> {
  const res = await api.get<WakeupRequest>(`/wakeup-requests/${id}`);
  return res.data;
}

export async function markWakeupDispatched(id: string): Promise<void> {
  await api.post(`/wakeup-requests/${id}/dispatch`);
}
