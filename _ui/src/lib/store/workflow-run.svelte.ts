import type { NodeStatus } from 'kaykay';
import { applyWorkflowEvent, type WorkflowRunState, type WorkflowStreamEvent } from '@/lib/workflow/run-events';
export type { NodeRunState, WorkflowStreamEvent } from '@/lib/workflow/run-events';

/** Reactive store for workflow run state. */
export const workflowRun = $state<WorkflowRunState>({
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
  applyWorkflowEvent(workflowRun, event);
}

/** Derive a node_statuses map suitable for kaykay Canvas node_statuses prop. */
export function getNodeStatuses(): Record<string, NodeStatus> {
  const statuses: Record<string, NodeStatus> = {};
  for (const [id, state] of Object.entries(workflowRun.nodeRunStates)) {
    statuses[id] = state.status;
  }
  return statuses;
}
