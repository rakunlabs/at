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
  created_at: string;
  updated_at: string;
}

interface WorkflowsResponse {
  workflows: Workflow[];
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

export async function runWorkflow(id: string, inputs: Record<string, any>): Promise<Record<string, any>> {
  const res = await api.post<Record<string, any>>(`/workflows/run/${id}`, { inputs });
  return res.data;
}
