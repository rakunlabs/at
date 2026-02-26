import axios from 'axios';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

export interface ActiveRun {
  id: string;
  workflow_id: string;
  source: string;
  started_at: string;
  duration: string;
}

interface ActiveRunsResponse {
  runs: ActiveRun[];
}

interface CancelRunResponse {
  message: string;
  run_id: string;
}

// ─── API Functions ───

export async function listActiveRuns(): Promise<ActiveRun[]> {
  const res = await api.get<ActiveRunsResponse>('/runs');
  return res.data.runs;
}

export async function cancelRun(runId: string): Promise<CancelRunResponse> {
  const res = await api.post<CancelRunResponse>(`/runs/${runId}/cancel`);
  return res.data;
}
