import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export interface BotConfig {
  id: string;
  platform: string;
  name: string;
  token: string;
  default_agent_id: string;
  channel_agents: Record<string, string>;
  access_mode: string;
  pending_approval: boolean;
  allowed_users: string[];
  pending_users: string[];
  enabled: boolean;
  user_containers?: boolean;
  container_image?: string;
  container_cpu?: string;
  container_memory?: string;
  speech_to_text?: string;
  whisper_model?: string;
  created_at: string;
  updated_at: string;
  created_by: string;
  updated_by: string;
}

export async function listBotConfigs(params?: ListParams): Promise<ListResult<BotConfig>> {
  const res = await api.get<ListResult<BotConfig>>('/bots', { params });
  return res.data;
}

export async function getBotConfig(id: string): Promise<BotConfig> {
  const res = await api.get<BotConfig>(`/bots/${id}`);
  return res.data;
}

export async function createBotConfig(data: Partial<BotConfig>): Promise<BotConfig> {
  const res = await api.post<BotConfig>('/bots', data);
  return res.data;
}

export async function updateBotConfig(id: string, data: Partial<BotConfig>): Promise<BotConfig> {
  const res = await api.put<BotConfig>(`/bots/${id}`, data);
  return res.data;
}

export async function deleteBotConfig(id: string): Promise<void> {
  await api.delete(`/bots/${id}`);
}
