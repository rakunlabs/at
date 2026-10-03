import axios from 'axios';
import type { ListResult } from './types';
import type { LLMCall, LLMCallTrace } from './llm-calls';

const api = axios.create({ baseURL: 'api/v1' });

export interface TraceScoreSummary {
  name: string;
  data_type: ScoreDataType;
  average?: number;
  string_value?: string;
  count: number;
}

// TraceSummary mirrors service.TraceSummary — one row of the trace list.
export interface TraceSummary extends LLMCallTrace {
  token_id?: string;
  user_id?: string;
  end_user?: string;
  environment?: string;
  release?: string;
  tags: string[];
  models: string[];
  input?: string;
  output?: string;
  duration_ms: number;
  total_tokens: number;
  scores: TraceScoreSummary[];
  bookmarked: boolean;
}

export type ScoreDataType = 'numeric' | 'boolean' | 'categorical';

export interface TraceScore {
  id: string;
  trace_id: string;
  observation_id?: string;
  name: string;
  data_type: ScoreDataType;
  value?: number;
  string_value?: string;
  source: 'annotation' | 'api';
  comment?: string;
  author_user_id?: string;
  token_id?: string;
  created_at: string;
}

export interface TraceDetail {
  trace: TraceSummary;
  observations: LLMCall[];
  truncated: boolean;
  scores: TraceScore[];
}

export interface TraceSession {
  session_id: string;
  token_id?: string;
  user_id?: string;
  end_user?: string;
  sources: string[];
  name?: string;
  trace_count: number;
  observation_count: number;
  generation_count: number;
  input_tokens: number;
  output_tokens: number;
  cost_cents: number;
  error_count: number;
  started_at: string;
  ended_at: string;
}

export interface TraceSessionDetail {
  session: TraceSession;
  traces: TraceSummary[];
}

export interface TraceFacets {
  names: string[];
  models: string[];
  sources: string[];
  environments: string[];
  releases: string[];
  tags: string[];
  user_ids: string[];
  end_users: string[];
  score_names: string[];
}

/** Query parameters accepted by GET /traces (all optional). */
export type TraceListParams = Record<string, string | string[] | number | undefined>;

function serialize(params: TraceListParams): URLSearchParams {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === '') continue;
    if (Array.isArray(value)) value.filter(Boolean).forEach((v) => search.append(key, v));
    else search.set(key, String(value));
  }
  return search;
}

export async function listTraces(params: TraceListParams): Promise<ListResult<TraceSummary>> {
  return (await api.get<ListResult<TraceSummary>>(`/traces?${serialize(params)}`)).data;
}

export async function getTrace(id: string): Promise<TraceDetail> {
  return (await api.get<TraceDetail>(`/traces/${encodeURIComponent(id)}`)).data;
}

export async function getTraceFacets(from?: string): Promise<TraceFacets> {
  return (await api.get<TraceFacets>(`/traces/facets?${serialize({ from })}`)).data;
}

export async function listTraceSessions(params: TraceListParams): Promise<ListResult<TraceSession>> {
  return (await api.get<ListResult<TraceSession>>(`/traces/sessions?${serialize(params)}`)).data;
}

export async function getTraceSession(id: string, tokenID = ''): Promise<TraceSessionDetail> {
  return (await api.get<TraceSessionDetail>(`/traces/sessions/${encodeURIComponent(id)}?${serialize({ token_id: tokenID })}`)).data;
}

export interface NewTraceScore {
  name: string;
  data_type: ScoreDataType;
  value?: number;
  string_value?: string;
  observation_id?: string;
  comment?: string;
}

export async function createTraceScore(traceID: string, score: NewTraceScore): Promise<TraceScore> {
  return (await api.post<TraceScore>(`/traces/${encodeURIComponent(traceID)}/scores`, score)).data;
}

export async function deleteTraceScore(id: string): Promise<void> {
  await api.delete(`/traces/scores/${encodeURIComponent(id)}`);
}

export async function setTraceBookmark(traceID: string, bookmarked: boolean): Promise<void> {
  await api.put(`/traces/${encodeURIComponent(traceID)}/bookmark`, { bookmarked });
}
