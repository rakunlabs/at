import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

export interface AgentHeartbeat {
  agent_id: string;
  status: string;
  last_heartbeat_at: string;
  metadata: Record<string, any>;
  updated_at: string;
}

export async function recordHeartbeat(agentId: string, metadata?: Record<string, any>): Promise<void> {
  await api.post(`/agents/${agentId}/heartbeat`, { metadata });
}

export async function getHeartbeat(agentId: string): Promise<AgentHeartbeat | null> {
  const res = await api.get<AgentHeartbeat>(`/agents/${agentId}/heartbeat-status`);
  return res.data;
}

export async function listHeartbeats(): Promise<AgentHeartbeat[]> {
  const res = await api.get<AgentHeartbeat[]>('/heartbeats');
  return res.data;
}
