import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

export interface GuardrailPattern {
  name: string;
  pattern: string;
}

export interface GuardrailConfig {
  input: { max_chars?: number; deny?: GuardrailPattern[] };
  injection: {
    enabled: boolean;
    action?: 'block' | 'warn';
    tool_results?: 'off' | 'warn' | 'withhold' | 'block';
    sensitivity?: 'normal' | 'strict';
    classifier?: string;
    fail_closed?: boolean;
  };
  masking: { entities?: string[]; custom?: GuardrailPattern[]; restore?: boolean };
  output: { deny?: GuardrailPattern[]; action?: 'block' | 'redact' };
}

export interface GuardrailPolicy {
  id: string;
  workspace_id: string;
  scope: 'installation' | 'workspace';
  name: string;
  description: string;
  enabled: boolean;
  mode: 'enforce' | 'monitor';
  user_id: string;
  token_id: string;
  provider: string;
  model: string;
  source: string;
  config: GuardrailConfig;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface GuardrailList {
  items: GuardrailPolicy[];
  entities: string[];
}

export interface GuardrailTestResult {
  blocked: boolean;
  blocked_by?: string;
  message?: string;
  masked: string;
  output?: string;
  findings: Record<string, any>[];
  masked_count: Record<string, number>;
}

export async function listGuardrails(): Promise<GuardrailList> {
  const data = (await api.get<GuardrailList>('/guardrails')).data;
  return { items: data.items || [], entities: data.entities || [] };
}
export async function createGuardrail(policy: Partial<GuardrailPolicy>): Promise<GuardrailPolicy> {
  return (await api.post('/guardrails', policy)).data;
}
export async function updateGuardrail(id: string, policy: Partial<GuardrailPolicy>): Promise<GuardrailPolicy> {
  return (await api.put(`/guardrails/${encodeURIComponent(id)}`, policy)).data;
}
export async function deleteGuardrail(id: string): Promise<void> {
  await api.delete(`/guardrails/${encodeURIComponent(id)}`);
}
export async function testGuardrail(policy: Partial<GuardrailPolicy>, sample: { input: string; tool_result?: string; output?: string }): Promise<GuardrailTestResult> {
  return (await api.post('/guardrails/test', { policy, ...sample })).data;
}
