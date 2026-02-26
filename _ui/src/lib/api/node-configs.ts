import axios from 'axios';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

export interface NodeConfig {
  id: string;
  name: string;
  type: string; // e.g. "email", "slack", "sms"
  data: string; // JSON blob with type-specific configuration
  created_at: string;
  updated_at: string;
}

interface NodeConfigsResponse {
  node_configs: NodeConfig[];
}

// ─── API Functions ───

export async function listNodeConfigs(type?: string): Promise<NodeConfig[]> {
  const params = type ? { type } : {};
  const res = await api.get<NodeConfigsResponse>('/node-configs', { params });
  return res.data.node_configs;
}

export async function getNodeConfig(id: string): Promise<NodeConfig> {
  const res = await api.get<NodeConfig>(`/node-configs/${id}`);
  return res.data;
}

export async function createNodeConfig(data: { name: string; type: string; data: string }): Promise<NodeConfig> {
  const res = await api.post<NodeConfig>('/node-configs', data);
  return res.data;
}

export async function updateNodeConfig(id: string, data: { name: string; type: string; data: string }): Promise<NodeConfig> {
  const res = await api.put<NodeConfig>(`/node-configs/${id}`, data);
  return res.data;
}

export async function deleteNodeConfig(id: string): Promise<void> {
  await api.delete(`/node-configs/${id}`);
}
