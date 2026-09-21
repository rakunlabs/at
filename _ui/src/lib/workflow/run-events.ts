import type { NodeStatus } from 'kaykay';

export interface NodeAttempt {
  attempt: number;
  status: 'running' | 'failed' | 'completed';
  error?: string;
  duration_ms?: number;
}

export interface NodeRunState {
  attempt?: number;
  max_attempts?: number;
  retry_delay_ms?: number;
  error_policy?: string;
  attempt_history?: NodeAttempt[];
  pin_signature?: string;
  result_kind?: string;
  selection?: string[];
  pinned?: boolean;
  skipped?: boolean;
  status: NodeStatus;
  execution_id?: string;
  invocations?: number;
  inputs?: Record<string, any>;
  resolved_inputs?: Record<string, any>;
  inputs_omitted?: boolean;
  resolved_inputs_omitted?: boolean;
  data_omitted?: boolean;
  data?: Record<string, any>;
  error?: string;
  duration_ms?: number;
}

export interface WorkflowStreamEvent {
  attempt?: number;
  max_attempts?: number;
  retry_delay_ms?: number;
  error_policy?: string;
  pin_signature?: string;
  result_kind?: string;
  selection?: string[];
  pinned?: boolean;
  test_mode?: boolean;
  event_type: string;
  node_id?: string;
  node_type?: string;
  execution_id?: string;
  inputs?: Record<string, any>;
  resolved_inputs?: Record<string, any>;
  inputs_omitted?: boolean;
  resolved_inputs_omitted?: boolean;
  data_omitted?: boolean;
  data?: Record<string, any>;
  duration_ms?: number;
  error?: string;
  run_id?: string;
  workflow_id?: string;
  outputs?: Record<string, any>;
  status?: string;
}

export interface WorkflowRunState {
  nodeRunStates: Record<string, NodeRunState>;
  status: 'idle' | 'running' | 'completed' | 'error';
  error: string;
  outputs: Record<string, any> | null;
}

export function applyWorkflowEvent(state: WorkflowRunState, event: WorkflowStreamEvent) {
  if (event.event_type === 'run_started') state.status = 'running';
  if (event.event_type === 'done') {
    if (state.status !== 'error') state.status = 'completed';
    state.outputs = event.outputs ?? event.data ?? null;
  }
  if (event.event_type === 'error' && !event.node_id) {
    state.status = 'error';
    state.error = event.error || 'Unknown error';
  }
  if (!event.node_id) return;
  const previous = state.nodeRunStates[event.node_id];
  if (event.event_type === 'started') {
    state.nodeRunStates[event.node_id] = {
      status: 'running', execution_id: event.execution_id,
      max_attempts: event.max_attempts, attempt_history: [],
      invocations: (previous?.invocations ?? 0) + 1,
      pinned: event.pinned,
      inputs: event.inputs ?? (event.execution_id && !event.inputs_omitted && !event.pinned ? {} : undefined), inputs_omitted: event.inputs_omitted,
      resolved_inputs: event.resolved_inputs, resolved_inputs_omitted: event.resolved_inputs_omitted,
    };
    return;
  }
  // Fan-out invocations complete out of order. Never show one invocation's
  // output beside a different invocation's input. Keep the latest-started one.
  if (event.execution_id && previous?.execution_id && event.execution_id !== previous.execution_id) return;
  if (event.event_type === 'attempt_started' || event.event_type === 'attempt_failed' || event.event_type === 'retrying') {
    let history = previous?.attempt_history ?? [];
    if (event.event_type !== 'retrying' && event.attempt) {
      history = [...history.filter(item => item.attempt !== event.attempt), {
        attempt: event.attempt,
        status: event.event_type === 'attempt_started' ? 'running' : 'failed',
        error: event.error, duration_ms: event.duration_ms,
      } as NodeAttempt].slice(-5);
    }
    state.nodeRunStates[event.node_id] = {
      ...previous, status: 'running', execution_id: event.execution_id ?? previous?.execution_id,
      attempt: event.attempt, max_attempts: event.max_attempts,
      retry_delay_ms: event.event_type === 'retrying' ? event.retry_delay_ms : undefined,
      attempt_history: history,
    };
    return;
  }
  if (event.event_type === 'completed' || event.event_type === 'error' || event.event_type === 'skipped' || event.event_type === 'error_handled') {
    state.nodeRunStates[event.node_id] = {
      ...previous,
      attempt: event.attempt, max_attempts: event.max_attempts ?? previous?.max_attempts,
      retry_delay_ms: undefined, error_policy: event.error_policy,
      attempt_history: previous?.attempt_history?.map(item => item.attempt === event.attempt && event.event_type === 'completed' ? { ...item, status: 'completed' } : item),
      pin_signature: event.pin_signature, result_kind: event.result_kind,
      selection: event.selection, pinned: event.pinned,
      skipped: event.event_type === 'skipped',
      execution_id: event.execution_id ?? previous?.execution_id,
      status: event.event_type === 'completed' ? 'completed' : event.event_type === 'skipped' ? 'idle' : 'error',
      data: event.data ?? (event.pin_signature && !event.data_omitted ? {} : undefined), data_omitted: event.data_omitted,
      error: event.error, duration_ms: event.duration_ms,
    };
  }
}
