import axios from 'axios';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

export interface Trigger {
  id: string;
  workflow_id: string;
  target_type: string;  // "workflow"
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
  /** Dedicated webhook servers this webhook is published on. Omit on update to keep them. */
  webhook_routes?: WebhookRoute[];
  /** Unreachable on the main /webhooks route (requires at least one server). */
  hide_from_main?: boolean;
  /** HMAC verification. Omit on update to keep it; secret '***' keeps the stored secret. */
  signature?: WebhookSignature | null;
}

export interface WebhookRoute {
  server_id: string;
  /** Empty: reachable by alias or ID. Otherwise replaces both on that server. */
  path: string;
}

export type WebhookSignatureScheme = '' | 'github' | 'stripe' | 'hmac_sha256';

export interface WebhookSignature {
  scheme: WebhookSignatureScheme;
  secret?: string;
  header?: string;
  prefix?: string;
  encoding?: 'hex' | 'base64';
}

export interface WebhookDelivery {
  id: string;
  trigger_id: string;
  server_id: string;
  method: string;
  path: string;
  status: number;
  run_id: string;
  error: string;
  client_ip: string;
  body_bytes: number;
  duration_ms: number;
  created_at: string;
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

export async function listWebhookDeliveries(id: string, limit = 50): Promise<WebhookDelivery[]> {
  const res = await api.get<{ deliveries: WebhookDelivery[] }>(`/triggers/${encodeURIComponent(id)}/deliveries`, { params: { limit } });
  return res.data.deliveries ?? [];
}
