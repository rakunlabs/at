import axios from 'axios';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

export interface WorkflowPos {
  x: number;
  y: number;
}

export interface WorkflowNode {
  id: string;
  type: string;
  position: WorkflowPos;
  data: Record<string, any>;
  width?: number;
  height?: number;
  parent_id?: string;
  z_index?: number;
}

export interface WorkflowEdge {
  id: string;
  source: string;
  target: string;
  source_handle: string;
  target_handle: string;
}

export interface WorkflowGraph {
  nodes: WorkflowNode[];
  edges: WorkflowEdge[];
}

export interface Workflow {
  id: string;
  name: string;
  description: string;
  graph: WorkflowGraph;
  active_version?: number;
  created_at: string;
  updated_at: string;
  created_by?: string;
  updated_by?: string;
}

export interface WorkflowVersion {
  id: string;
  workflow_id: string;
  version: number;
  name: string;
  description: string;
  graph: WorkflowGraph;
  created_at: string;
  created_by?: string;
}

interface WorkflowsResponse {
  workflows: Workflow[];
}

interface WorkflowVersionsResponse {
  versions: WorkflowVersion[];
}

// ─── API Functions ───

export async function listWorkflows(): Promise<Workflow[]> {
  const res = await api.get<WorkflowsResponse>('/workflows');
  return res.data.workflows;
}

export async function getWorkflow(id: string): Promise<Workflow> {
  const res = await api.get<Workflow>(`/workflows/${id}`);
  return res.data;
}

export async function createWorkflow(workflow: Partial<Workflow>): Promise<Workflow> {
  const res = await api.post<Workflow>('/workflows', workflow);
  return res.data;
}

export async function updateWorkflow(id: string, workflow: Partial<Workflow>): Promise<Workflow> {
  const res = await api.put<Workflow>(`/workflows/${id}`, workflow);
  return res.data;
}

export async function deleteWorkflow(id: string): Promise<void> {
  await api.delete(`/workflows/${id}`);
}

export async function runWorkflow(id: string, inputs: Record<string, any>, sync = false, version?: number): Promise<Record<string, any>> {
  const params = new URLSearchParams();
  if (sync) params.set('sync', 'true');
  if (version !== undefined) params.set('version', String(version));
  const qs = params.toString();
  const url = `/workflows/run/${id}${qs ? '?' + qs : ''}`;
  const res = await api.post<Record<string, any>>(url, { inputs });
  return res.data;
}

// ─── Version API Functions ───

export async function listWorkflowVersions(workflowId: string): Promise<WorkflowVersion[]> {
  const res = await api.get<WorkflowVersionsResponse>(`/workflows/${workflowId}/versions`);
  return res.data.versions;
}

export async function getWorkflowVersion(workflowId: string, version: number): Promise<WorkflowVersion> {
  const res = await api.get<WorkflowVersion>(`/workflows/${workflowId}/versions/${version}`);
  return res.data;
}

export async function setActiveVersion(workflowId: string, version: number): Promise<void> {
  await api.put(`/workflows/${workflowId}/active-version`, { version });
}
