import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export interface HeartbeatRun {
  id: string;
  agent_id: string;
  invocation_source: string;
  trigger_detail: string;
  status: string;
  context_snapshot: Record<string, any>;
  usage_json: Record<string, any>;
  result_json: Record<string, any>;
  log_ref: string;
  log_bytes: number;
  log_sha256: string;
  stdout_excerpt: string;
  stderr_excerpt: string;
  session_id_before: string;
  session_id_after: string;
  started_at: string;
  finished_at: string;
  created_at: string;
}

export async function listHeartbeatRuns(agentId: string, params?: ListParams): Promise<ListResult<HeartbeatRun>> {
  const res = await api.get<ListResult<HeartbeatRun>>(`/agents/${agentId}/runs`, { params });
  return res.data;
}

export async function createHeartbeatRun(agentId: string, data: Partial<HeartbeatRun>): Promise<HeartbeatRun> {
  const res = await api.post<HeartbeatRun>(`/agents/${agentId}/runs`, data);
  return res.data;
}

export async function getActiveRun(agentId: string): Promise<HeartbeatRun | null> {
  const res = await api.get<HeartbeatRun>(`/agents/${agentId}/active-run`);
  return res.data;
}

export async function getHeartbeatRun(id: string): Promise<HeartbeatRun> {
  const res = await api.get<HeartbeatRun>(`/heartbeat-runs/${id}`);
  return res.data;
}

export async function updateHeartbeatRun(id: string, data: Partial<HeartbeatRun>): Promise<HeartbeatRun> {
  const res = await api.put<HeartbeatRun>(`/heartbeat-runs/${id}`, data);
  return res.data;
}
