import axios from 'axios';
import type { ListResult } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export type BudgetResourceKind = 'provider' | 'virtual_provider';
export type BudgetOverrideMode = 'custom' | 'unlimited' | 'blocked';

export interface ProviderBudgetPolicy {
  id?: string;
  resource_kind: BudgetResourceKind;
  resource_id: string;
  total_limit_cents: number;
  default_user_limit_cents: number;
  budget_period: 'daily' | 'weekly' | 'monthly';
  budget_reset_day: number;
  budget_reset_time: string;
  budget_timezone: string;
  enforce_unpriced: boolean;
  period_start?: string;
  period_end?: string;
  spent_cents?: number;
  reserved_cents?: number;
  user_spent_cents?: number;
  user_reserved_cents?: number;
  effective_user_limit_cents?: number;
}

export interface ProviderBudgetOverride {
  policy_id: string;
  user_id: string;
  mode: BudgetOverrideMode;
  limit_cents: number;
  updated_at?: string;
}

export interface VirtualProviderModel {
  alias: string;
  provider_ref: string;
  model: string;
  position?: number;
}

export interface VirtualProvider {
  workspace_id?: string;
  id?: string;
  key: string;
  name: string;
  description?: string;
  default_model: string;
  disabled: boolean;
  models: VirtualProviderModel[];
  created_at?: string;
  updated_at?: string;
}

export interface VirtualProviderGrant {
  id?: string;
  virtual_provider_id: string;
  workspace_id: string;
  model_patterns: string[];
  allow_user_overrides: boolean;
  max_user_limit_cents: number;
}

export async function getProviderBudget(kind: BudgetResourceKind, id: string, userId?: string) {
  const res = await api.get<ProviderBudgetPolicy | null>(`/provider-budgets/${kind}/${encodeURIComponent(id)}`, { params: userId ? { user_id: userId } : undefined });
  return res.data;
}

export async function saveProviderBudget(kind: BudgetResourceKind, id: string, policy: ProviderBudgetPolicy) {
  const res = await api.put<ProviderBudgetPolicy>(`/provider-budgets/${kind}/${encodeURIComponent(id)}`, policy);
  return res.data;
}

export async function deleteProviderBudget(kind: BudgetResourceKind, id: string) {
  await api.delete(`/provider-budgets/${kind}/${encodeURIComponent(id)}`);
}

export async function listProviderBudgetOverrides(policyId: string) {
  const res = await api.get<{items: ProviderBudgetOverride[]}>(`/provider-budget-overrides/${encodeURIComponent(policyId)}`);
  return res.data.items || [];
}

export async function saveProviderBudgetOverride(policyId: string, override: Omit<ProviderBudgetOverride, 'policy_id'>) {
  await api.put(`/provider-budget-overrides/${encodeURIComponent(policyId)}`, override);
}

export async function deleteProviderBudgetOverride(policyId: string, userId: string) {
  await api.delete(`/provider-budget-overrides/${encodeURIComponent(policyId)}/${encodeURIComponent(userId)}`);
}

export async function listVirtualProviders() {
  return (await api.get<ListResult<VirtualProvider>>('/virtual-providers')).data;
}

export async function createVirtualProvider(record: VirtualProvider) {
  return (await api.post<VirtualProvider>('/virtual-providers', record)).data;
}

export async function updateVirtualProvider(id: string, record: VirtualProvider) {
  return (await api.put<VirtualProvider>(`/virtual-providers/${encodeURIComponent(id)}`, record)).data;
}

export async function deleteVirtualProvider(id: string) {
  await api.delete(`/virtual-providers/${encodeURIComponent(id)}`);
}

export async function listVirtualProviderGrants(id: string) {
  return (await api.get<{items: VirtualProviderGrant[]}>(`/virtual-providers/${encodeURIComponent(id)}/grants`)).data.items || [];
}

export async function saveVirtualProviderGrant(id: string, grant: VirtualProviderGrant) {
  return (await api.put<VirtualProviderGrant>(`/virtual-providers/${encodeURIComponent(id)}/grants`, grant)).data;
}

export async function deleteVirtualProviderGrant(id: string, workspaceId: string) {
  await api.delete(`/virtual-providers/${encodeURIComponent(id)}/grants/${encodeURIComponent(workspaceId)}`);
}
