import type { NodeRunState } from './run-events';

export interface PinnedNode {
  signature: string;
  data: Record<string, any>;
  result_kind: 'result' | 'selection';
  selection?: string[];
}

export interface WorkflowTestRunOptions {
  target_node_id?: string;
  pins?: Record<string, PinnedNode>;
}

export function pinUnavailableReason(result?: NodeRunState): string {
  if (!result || result.status !== 'completed') return 'Run this step successfully before pinning its output.';
  if (result.data_omitted || result.data === undefined) return 'The full output is not available in this preview.';
  if ((result.invocations ?? 0) > 1) return 'A single preview cannot represent all loop invocations.';
  if (!result.pin_signature || !['result', 'selection'].includes(result.result_kind ?? '')) return 'This node or its fan-out output cannot be pinned.';
  return '';
}

export function pinNodeOutput(result: NodeRunState): PinnedNode {
  const reason = pinUnavailableReason(result);
  if (reason) throw new Error(reason);
  return JSON.parse(JSON.stringify({
    signature: result.pin_signature, data: result.data,
    result_kind: result.result_kind, selection: result.selection,
  }));
}

export function buildTestRunOptions(target: string | null, pins: Record<string, PinnedNode>, usePins: boolean): WorkflowTestRunOptions | undefined {
  const applicable = usePins ? Object.fromEntries(Object.entries(pins).filter(([id]) => id !== target)) : {};
  if (!target && Object.keys(applicable).length === 0) return undefined;
  return {
    ...(target ? { target_node_id: target } : {}),
    ...(Object.keys(applicable).length ? { pins: JSON.parse(JSON.stringify(applicable)) } : {}),
  };
}
