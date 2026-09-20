import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

export interface TraceExportSettings {
  version: number;
  enabled: boolean;
  target: 'collector' | 'langfuse';
  protocol: 'http' | 'grpc';
  endpoint: string;
  headers: Record<string, string>;
  public_key: string;
  secret_key: string;
  include_content: boolean;
}

export interface TraceExportTestResult {
  ok: boolean;
  message: string;
  trace_id: string;
  duration_ms: number;
}

export async function getTraceExportSettings(): Promise<TraceExportSettings> {
  return (await api.get('/trace-export')).data;
}
export async function saveTraceExportSettings(settings: TraceExportSettings): Promise<TraceExportSettings> {
  return (await api.put('/trace-export', settings)).data;
}
export async function testTraceExport(settings: TraceExportSettings): Promise<TraceExportTestResult> {
  return (await api.post('/trace-export/test', settings, { timeout: 15000 })).data;
}
