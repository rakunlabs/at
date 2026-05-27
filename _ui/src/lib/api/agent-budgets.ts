import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export interface AgentBudget {
  id: string;
  agent_id: string;
  monthly_limit: number;
  current_spend: number;
  period_start: string;
  period_end: string;
  created_at: string;
  updated_at: string;
}

export interface AgentUsageRecord {
  id: string;
  agent_id: string;
  task_id: string;
  workflow_run_id: string;
  session_id: string;
  model: string;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  estimated_cost: number;
  created_at: string;
}

export interface ModelPricing {
  id: string;
  provider_key: string;
  model: string;
  prompt_price_per_1m: number;
  completion_price_per_1m: number;
  cache_read_price_per_1m: number;
  cache_write_price_per_1m: number;
  source?: string;
  source_provider?: string;
  source_model?: string;
  source_url?: string;
  source_prompt_price_per_1m: number;
  source_completion_price_per_1m: number;
  source_cache_read_price_per_1m: number;
  source_cache_write_price_per_1m: number;
  manual_override: boolean;
  last_synced_at?: string;
  created_at: string;
  updated_at: string;
}

export interface ModelPricingSyncPreviewItem {
  provider_key: string;
  provider_type: string;
  model: string;
  matched: boolean;
  match_type?: string;
  confidence?: number;
  status: 'missing' | 'update' | 'current' | 'override' | 'no_match';
  has_current: boolean;
  manual_override: boolean;
  source?: string;
  source_provider?: string;
  source_model?: string;
  source_url?: string;
  current_prompt_price_per_1m: number;
  current_completion_price_per_1m: number;
  current_cache_read_price_per_1m: number;
  current_cache_write_price_per_1m: number;
  source_prompt_price_per_1m: number;
  source_completion_price_per_1m: number;
  source_cache_read_price_per_1m: number;
  source_cache_write_price_per_1m: number;
}

export interface ModelPricingSyncPreviewResponse {
  source: string;
  items: ModelPricingSyncPreviewItem[];
}

export interface ModelPricingSyncSource {
  source: string;
  label: string;
  url?: string;
  description?: string;
}

export interface ModelPricingSyncApplyResponse {
  applied: number;
  skipped: number;
  errors?: string[];
}

export interface ModelPricingCatalog {
  version: number;
  exported_at: string;
  items: ModelPricing[];
}

export interface ModelPricingAgentPreviewRequest {
  provider_key: string;
  model?: string;
  instruction?: string;
  source_url?: string;
  source_text?: string;
  web_search?: boolean;
}

export interface AgentSpend {
  total_spend: number;
}

export async function getAgentBudget(agentId: string): Promise<AgentBudget | null> {
  const res = await api.get<AgentBudget>(`/agents/${agentId}/budget`);
  return res.data;
}

export async function setAgentBudget(agentId: string, data: Partial<AgentBudget>): Promise<void> {
  await api.put(`/agents/${agentId}/budget`, data);
}

export async function getAgentUsage(agentId: string, params?: ListParams): Promise<ListResult<AgentUsageRecord>> {
  const res = await api.get<ListResult<AgentUsageRecord>>(`/agents/${agentId}/usage`, { params });
  return res.data;
}

export async function getAgentSpend(agentId: string): Promise<AgentSpend> {
  const res = await api.get<AgentSpend>(`/agents/${agentId}/spend`);
  return res.data;
}

export async function listModelPricing(): Promise<ModelPricing[]> {
  const res = await api.get<ModelPricing[]>('/model-pricing');
  return res.data;
}

export async function exportModelPricingCatalog(): Promise<ModelPricingCatalog> {
  const res = await api.get<ModelPricingCatalog>('/model-pricing/catalog');
  return res.data;
}

export async function importModelPricingCatalog(
  catalog: Pick<ModelPricingCatalog, 'items'>,
  overwrite_overrides = false
): Promise<ModelPricingSyncApplyResponse> {
  const res = await api.post<ModelPricingSyncApplyResponse>('/model-pricing/catalog/import', {
    items: catalog.items,
    overwrite_overrides,
  });
  return res.data;
}

export async function setModelPricing(data: Partial<ModelPricing>): Promise<void> {
  await api.post('/model-pricing', data);
}

export async function deleteModelPricing(id: string): Promise<void> {
  await api.delete(`/model-pricing/${id}`);
}

export async function resetModelPricing(id: string): Promise<void> {
  await api.post(`/model-pricing/${id}/reset`);
}

export async function previewModelPricingSync(source = 'pi.dev'): Promise<ModelPricingSyncPreviewResponse> {
  const res = await api.post<ModelPricingSyncPreviewResponse>('/model-pricing/sync/preview', { source });
  return res.data;
}

export async function listModelPricingSyncSources(): Promise<ModelPricingSyncSource[]> {
  const res = await api.get<ModelPricingSyncSource[]>('/model-pricing/sync/sources');
  return res.data;
}

export async function previewModelPricingAgent(data: ModelPricingAgentPreviewRequest): Promise<ModelPricingSyncPreviewResponse> {
  const res = await api.post<ModelPricingSyncPreviewResponse>('/model-pricing/agent/preview', data);
  return res.data;
}

export async function applyModelPricingSync(
  items: { provider_key: string; model: string }[],
  overwrite_overrides = false,
  source = 'pi.dev',
  preview_items?: ModelPricingSyncPreviewItem[]
): Promise<ModelPricingSyncApplyResponse> {
  const res = await api.post<ModelPricingSyncApplyResponse>('/model-pricing/sync/apply', {
    source,
    overwrite_overrides,
    items,
    preview_items,
  });
  return res.data;
}
