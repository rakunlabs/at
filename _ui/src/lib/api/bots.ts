import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export interface BotCustomCommand {
  command: string;          // without leading slash
  description?: string;     // shown in /help
  organization_id?: string; // route via org intake
  agent_id?: string;        // route to specific agent
  brief?: string;           // task description template; "{args}" gets replaced with user args
  title_prefix?: string;    // optional prefix for the resulting task title
  max_iterations?: number;  // optional per-task override
}

export interface BotConfig {
  id: string;
  platform: string;
  name: string;
  token: string;
  default_agent_id: string;
  channel_agents: Record<string, string>;
  allowed_agent_ids?: string[];
  custom_commands?: BotCustomCommand[];
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

export async function startBot(id: string): Promise<any> {
  const res = await api.post(`/bots/${id}/start`);
  return res.data;
}

export async function stopBot(id: string): Promise<any> {
  const res = await api.post(`/bots/${id}/stop`);
  return res.data;
}

export interface BotStatus {
  running: boolean;
  platform?: string;
  started_at?: string;
}

export async function getBotStatus(id: string): Promise<BotStatus> {
  const res = await api.get<BotStatus>(`/bots/${id}/status`);
  return res.data;
}
