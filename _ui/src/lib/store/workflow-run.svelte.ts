import type { NodeStatus } from 'kaykay';

/** Per-node execution state during/after a workflow run. */
export interface NodeRunState {
  status: NodeStatus;
  data?: Record<string, any>;
  error?: string;
  duration_ms?: number;
}

/** SSE event from the backend run-stream endpoint. */
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

/** Reactive store for workflow run state. */
export const workflowRun = $state<{
  nodeRunStates: Record<string, NodeRunState>;
  status: 'idle' | 'running' | 'completed' | 'error';
  error: string;
  outputs: Record<string, any> | null;
}>({
  nodeRunStates: {},
  status: 'idle',
  error: '',
  outputs: null,
});

/** Reset all run state (call before starting a new run). */
export function clearRunState() {
  for (const key of Object.keys(workflowRun.nodeRunStates)) {
    delete workflowRun.nodeRunStates[key];
  }
  workflowRun.status = 'idle';
  workflowRun.error = '';
  workflowRun.outputs = null;
}

/** Process an SSE event and update the store. */
export function handleStreamEvent(event: WorkflowStreamEvent) {
  switch (event.event_type) {
    case 'run_started':
      workflowRun.status = 'running';
      break;

    case 'started':
      if (event.node_id) {
        workflowRun.nodeRunStates[event.node_id] = {
          status: 'running',
        };
      }
      break;

    case 'completed':
      if (event.node_id) {
        workflowRun.nodeRunStates[event.node_id] = {
          status: 'completed',
          data: event.data,
          duration_ms: event.duration_ms,
        };
      }
      break;

    case 'error':
      if (event.node_id) {
        workflowRun.nodeRunStates[event.node_id] = {
          status: 'error',
          error: event.error,
          duration_ms: event.duration_ms,
        };
      }
      // If no node_id, this is a workflow-level error.
      if (!event.node_id) {
        workflowRun.status = 'error';
        workflowRun.error = event.error || 'Unknown error';
      }
      break;

    case 'skipped':
      if (event.node_id) {
        workflowRun.nodeRunStates[event.node_id] = {
          status: 'idle',
          duration_ms: event.duration_ms,
        };
      }
      break;

    case 'done':
      workflowRun.status = 'completed';
      workflowRun.outputs = event.outputs ?? event.data ?? null;
      break;
  }
}

/** Derive a node_statuses map suitable for kaykay Canvas node_statuses prop. */
export function getNodeStatuses(): Record<string, NodeStatus> {
  const statuses: Record<string, NodeStatus> = {};
  for (const [id, state] of Object.entries(workflowRun.nodeRunStates)) {
    statuses[id] = state.status;
  }
  return statuses;
}
