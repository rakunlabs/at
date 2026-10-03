import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

export type TracePrivacyAction = 'skip' | 'redact';

export interface TracePrivacyRule {
  id: string;
  workspace_id: string;
  scope: 'installation' | 'workspace';
  description: string;
  user_id: string;
  token_id: string;
  provider: string;
  model: string;
  source: string;
  action: TracePrivacyAction;
  enabled: boolean;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface TracePrivacySettings {
  allow_user_opt_out: boolean;
  updated_at?: string;
}

export interface TracePrivacyApplyResult {
  dry_run: boolean;
  action: TracePrivacyAction;
  traces: number;
  observations: number;
}

export interface TracePrivacyOptOut {
  allowed: boolean;
  opted_out: boolean;
}

export async function listTracePrivacyRules(): Promise<TracePrivacyRule[]> {
  return (await api.get<{ items: TracePrivacyRule[] }>('/trace-privacy/rules')).data.items || [];
}
export async function createTracePrivacyRule(rule: Partial<TracePrivacyRule>): Promise<TracePrivacyRule> {
  return (await api.post('/trace-privacy/rules', rule)).data;
}
export async function updateTracePrivacyRule(id: string, rule: Partial<TracePrivacyRule>): Promise<TracePrivacyRule> {
  return (await api.put(`/trace-privacy/rules/${encodeURIComponent(id)}`, rule)).data;
}
export async function deleteTracePrivacyRule(id: string): Promise<void> {
  await api.delete(`/trace-privacy/rules/${encodeURIComponent(id)}`);
}
export async function applyTracePrivacyRule(id: string, dryRun: boolean): Promise<TracePrivacyApplyResult> {
  return (await api.post(`/trace-privacy/rules/${encodeURIComponent(id)}/apply`, {}, { params: dryRun ? { dry_run: 'true' } : undefined, timeout: 120000 })).data;
}
export async function getTracePrivacySettings(): Promise<TracePrivacySettings> {
  return (await api.get('/trace-privacy/settings')).data;
}
export async function saveTracePrivacySettings(settings: TracePrivacySettings): Promise<TracePrivacySettings> {
  return (await api.put('/trace-privacy/settings', { allow_user_opt_out: settings.allow_user_opt_out })).data;
}
export async function getTraceOptOut(): Promise<TracePrivacyOptOut> {
  return (await api.get('/trace-privacy/opt-out')).data;
}
export async function setTraceOptOut(opted_out: boolean): Promise<TracePrivacyOptOut> {
  return (await api.put('/trace-privacy/opt-out', { opted_out })).data;
}
