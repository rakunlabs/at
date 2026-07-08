import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({ baseURL: 'api/v1' });

// LLMCall mirrors service.LLMCall — one recorded upstream provider call
// with full request/response bodies (Langfuse-style tracing).
export interface LLMCall {
  id: string;
  trace_id: string;
  session_id: string;
  source: string;
  endpoint: string;
  token_id: string;
  agent_id: string;
  task_id: string;
  run_id: string;
  organization_id: string;
  provider: string;
  model: string;
  requested_model: string;
  request_body: string;
  response_body: string;
  request_bytes: number;
  response_bytes: number;
  request_truncated: boolean;
  response_truncated: boolean;
  request_ref: string;
  response_ref: string;
  streamed: boolean;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  reasoning_tokens: number;
  cost_cents: number;
  latency_ms: number;
  time_to_first_token_ms: number;
  status: string;
  error_code: string;
  error_message: string;
  finish_reason: string;
  user_field: string;
  created_at: string;
}

export async function listLLMCalls(params?: ListParams): Promise<ListResult<LLMCall>> {
  const res = await api.get<ListResult<LLMCall>>('/llm-calls', { params });
  return res.data;
}

export async function getLLMCall(id: string): Promise<LLMCall> {
  const res = await api.get<LLMCall>(`/llm-calls/${id}`);
  return res.data;
}
