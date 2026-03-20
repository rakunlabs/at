import axios from 'axios';
import type { ListResult, ListParams } from './types';

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
  node_number?: number;
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


interface WorkflowVersionsResponse {
  versions: WorkflowVersion[];
}

// ─── Node Type Metadata ───

export type PortType = 'text' | 'data' | 'messages' | 'image' | 'audio' | 'video' | 'embedding' | 'boolean' | 'config';

export interface PortMeta {
  name: string;
  type: PortType;
  required?: boolean;
  accept?: PortType[];
  label?: string;
  position?: 'left' | 'right' | 'top' | 'bottom';
}

export interface FieldMeta {
  name: string;
  type: string;       // "string", "number", "boolean", "array", "object"
  required?: boolean;
  default?: any;
  description?: string;
  enum?: string[];
}

export interface NodeTypeMeta {
  type: string;
  label: string;
  category: string;
  description: string;
  inputs: PortMeta[];
  outputs: PortMeta[];
  fields?: FieldMeta[];
  color?: string;
}

interface NodeTypesResponse {
  node_types: NodeTypeMeta[];
}

// ─── API Functions ───

export async function listWorkflows(params?: ListParams): Promise<ListResult<Workflow>> {
  const res = await api.get<ListResult<Workflow>>('/workflows', { params });
  return res.data;
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

export async function runWorkflow(id: string, inputs: Record<string, any>, sync = false, version?: number, entryNodeIds?: string[]): Promise<Record<string, any>> {
  const params = new URLSearchParams();
  if (sync) params.set('sync', 'true');
  if (version !== undefined) params.set('version', String(version));
  const qs = params.toString();
  const url = `/workflows/run/${id}${qs ? '?' + qs : ''}`;
  const body: Record<string, any> = { inputs };
  if (entryNodeIds && entryNodeIds.length > 0) {
    body.entry_node_ids = entryNodeIds;
  }
  const res = await api.post<Record<string, any>>(url, body);
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

// ─── Node Type Metadata API ───

/** Fetch the complete catalog of registered node types with port schemas. */
export async function getNodeTypes(): Promise<NodeTypeMeta[]> {
  const res = await api.get<NodeTypesResponse>('/workflow-node-types');
  return res.data.node_types;
}

// ─── Streaming Run API ───

export interface WorkflowStreamEvent {
  event_type: string;
  node_id?: string;
  node_type?: string;
  data?: Record<string, any>;
  duration_ms?: number;
  error?: string;
  run_id?: string;
  workflow_id?: string;
  outputs?: Record<string, any>;
  status?: string;
}

/**
 * Run a workflow with SSE streaming of per-node events.
 * Returns an AbortController to cancel the run.
 *
 * @param id - Workflow ID
 * @param inputs - Workflow inputs
 * @param onEvent - Called for each SSE event
 * @param onDone - Called when the stream ends
 * @param version - Optional version number
 * @param entryNodeIds - Optional entry node IDs
 */
export function runWorkflowStream(
  id: string,
  inputs: Record<string, any>,
  onEvent: (event: WorkflowStreamEvent) => void,
  onDone?: () => void,
  version?: number,
  entryNodeIds?: string[],
): AbortController {
  const controller = new AbortController();

  const params = new URLSearchParams();
  if (version !== undefined) params.set('version', String(version));
  const qs = params.toString();
  const url = `api/v1/workflows/run-stream/${id}${qs ? '?' + qs : ''}`;

  const body: Record<string, any> = { inputs };
  if (entryNodeIds && entryNodeIds.length > 0) {
    body.entry_node_ids = entryNodeIds;
  }

  fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal: controller.signal,
  })
    .then(async (response) => {
      if (!response.ok) {
        const text = await response.text();
        onEvent({ event_type: 'error', error: text || `HTTP ${response.status}` });
        onDone?.();
        return;
      }

      const reader = response.body?.getReader();
      if (!reader) {
        onEvent({ event_type: 'error', error: 'No response body' });
        onDone?.();
        return;
      }

      const decoder = new TextDecoder();
      let buffer = '';

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });

        // Parse SSE events from buffer
        const lines = buffer.split('\n');
        buffer = lines.pop() || ''; // Keep incomplete line in buffer

        for (const line of lines) {
          const trimmed = line.trim();
          if (!trimmed) continue;
          if (trimmed.startsWith('data: ')) {
            try {
              const data = JSON.parse(trimmed.slice(6)) as WorkflowStreamEvent;
              onEvent(data);
            } catch {
              // Skip malformed JSON
            }
          }
        }
      }

      onDone?.();
    })
    .catch((err) => {
      if (err.name !== 'AbortError') {
        onEvent({ event_type: 'error', error: err.message || 'Stream failed' });
      }
      onDone?.();
    });

  return controller;
}
