import type { NodeRunState } from './run-events';

export interface RunEdge {
  source: string;
  source_handle: string;
  target: string;
}

const errorPort = '__error';

// Label for a connection after a run, in the spirit of n8n's "3 items":
// what actually travelled along this edge. Returns '' for edges the run did
// not take (inactive branch, failed or not yet finished source), so only the
// path the data followed is annotated.
export function edgeRunLabel(edge: RunEdge, source?: NodeRunState, target?: NodeRunState): string {
  if (!source) return '';
  if (edge.source_handle === errorPort) {
    return source.status === 'error' && source.error_policy === 'error_output' ? 'failure' : '';
  }
  if (source.status !== 'completed' || source.skipped) return '';

  if (source.result_kind === 'fan_out') {
    const runs = target?.invocations ?? 0;
    return runs ? itemLabel(runs) : '';
  }
  if (source.result_kind === 'selection' && !(source.selection ?? []).includes(edge.source_handle)) return '';
  if (source.data_omitted || !source.data) return '✓';

  // Mirrors the engine: a port key in the result carries that value, while a
  // selection port routes the whole result.
  const value = edge.source_handle in source.data ? source.data[edge.source_handle] : source.result_kind === 'selection' ? source.data : undefined;
  if (value === undefined) return '';
  return itemLabel(Array.isArray(value) ? value.length : 1);
}

function itemLabel(count: number): string {
  return `${count} ${count === 1 ? 'item' : 'items'}`;
}
