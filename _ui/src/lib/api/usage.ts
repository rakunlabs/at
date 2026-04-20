import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

export interface UsageFilter {
  from?: string; // RFC3339
  to?: string;   // RFC3339
  status?: string;
  provider?: string[];
  model?: string[];
  agent_id?: string[];
  org_id?: string[];
  project_id?: string[];
  goal_id?: string[];
  billing_code?: string[];
}

export interface UsageSummary {
  key?: string;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  request_count: number;
  error_count: number;
  cost_cents: number;
  avg_latency_ms: number;
  max_latency_ms: number;
  total_latency_ms: number;
  first_event_at?: string;
  last_event_at?: string;
}

export interface UsageTimeSeriesPoint {
  bucket: string;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  request_count: number;
  error_count: number;
  cost_cents: number;
  avg_latency_ms: number;
}

export interface BudgetUtilization {
  agent_id: string;
  agent_name?: string;
  monthly_limit: number;
  current_spend: number;
  period_start?: string;
  period_end?: string;
  usage_percent: number;
}

export type GroupBy = 'provider' | 'model' | 'agent' | 'org' | 'project' | 'goal' | 'billing_code' | 'status';
export type Bucket = 'hour' | 'day';

// Convert a UsageFilter into the flattened query-string object axios expects.
// axios serializes arrays as `provider=a&provider=b` when configured with
// paramsSerializer; we do it ourselves for clarity + predictability.
function toParams(filter: UsageFilter): URLSearchParams {
  const p = new URLSearchParams();
  if (filter.from) p.append('from', filter.from);
  if (filter.to) p.append('to', filter.to);
  if (filter.status) p.append('status', filter.status);
  for (const v of filter.provider || []) p.append('provider', v);
  for (const v of filter.model || []) p.append('model', v);
  for (const v of filter.agent_id || []) p.append('agent_id', v);
  for (const v of filter.org_id || []) p.append('org_id', v);
  for (const v of filter.project_id || []) p.append('project_id', v);
  for (const v of filter.goal_id || []) p.append('goal_id', v);
  for (const v of filter.billing_code || []) p.append('billing_code', v);
  return p;
}

export async function getUsageSummary(filter: UsageFilter = {}): Promise<UsageSummary> {
  const res = await api.get<UsageSummary>(`/usage/summary?${toParams(filter).toString()}`);
  return res.data;
}

export async function getUsageTimeSeries(filter: UsageFilter, bucket: Bucket = 'day'): Promise<UsageTimeSeriesPoint[]> {
  const params = toParams(filter);
  params.append('bucket', bucket);
  const res = await api.get<{ bucket: Bucket; data: UsageTimeSeriesPoint[] }>(`/usage/timeseries?${params.toString()}`);
  return res.data.data || [];
}

export async function getUsageGrouped(filter: UsageFilter, groupBy: GroupBy, limit = 0): Promise<UsageSummary[]> {
  const params = toParams(filter);
  params.append('group_by', groupBy);
  if (limit > 0) params.append('limit', String(limit));
  const res = await api.get<{ group_by: GroupBy; data: UsageSummary[] }>(`/usage/grouped?${params.toString()}`);
  return res.data.data || [];
}

export async function getBudgetUtilization(): Promise<BudgetUtilization[]> {
  const res = await api.get<{ data: BudgetUtilization[] }>(`/usage/budgets`);
  return res.data.data || [];
}

// ─── Date Range Helpers ───

export function isoDaysAgo(days: number): string {
  const d = new Date();
  d.setDate(d.getDate() - days);
  d.setHours(0, 0, 0, 0);
  return d.toISOString();
}

export function isoNow(): string {
  return new Date().toISOString();
}

export function presetRange(preset: '24h' | '7d' | '30d' | 'mtd'): { from: string; to: string } {
  const now = new Date();
  const to = now.toISOString();
  if (preset === '24h') {
    const from = new Date(now.getTime() - 24 * 60 * 60 * 1000).toISOString();
    return { from, to };
  }
  if (preset === '7d') {
    return { from: isoDaysAgo(7), to };
  }
  if (preset === '30d') {
    return { from: isoDaysAgo(30), to };
  }
  // mtd — start of month
  const start = new Date(now.getFullYear(), now.getMonth(), 1).toISOString();
  return { from: start, to };
}
