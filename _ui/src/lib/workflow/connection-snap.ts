import type { FlowState } from 'kaykay';

// Connection snapping for the workflow canvas.
//
// Kaykay finishes a dragged connection only when the pointer is released
// exactly over a 12px handle; a release a few pixels away silently drops the
// wire, which is what "sometimes I cannot draw an edge" looks like. When the
// release misses, the nearest input handle within `radius` screen pixels is
// tried instead, and a rejected connection reports Kaykay's own reason.

export interface SnapCandidate {
  nodeId: string;
  handleId: string;
  distance: number;
}

export interface SnapResult {
  candidate: SnapCandidate | null;
  reason: string;
}

type Point = { x: number; y: number };

/**
 * Picks the closest input handle (in screen pixels) that accepts the current
 * draft connection. Returns the reason of the closest rejected handle when no
 * valid one is in range, so the caller can explain the failure.
 */
export function findSnapTarget(flow: FlowState, canvasPoint: Point, radius: number): SnapResult {
  const draft = flow.draft_connection;
  if (!draft || draft.reconnect_type === 'source') return { candidate: null, reason: '' };
  const zoom = flow.viewport.zoom || 1;
  let best: SnapCandidate | null = null;
  let rejected: { distance: number; reason: string } | null = null;
  for (const [key, handle] of Object.entries(flow.handle_registry)) {
    if (handle.type !== 'input') continue;
    const nodeId = key.slice(0, key.length - handle.id.length - 1);
    if (!nodeId || nodeId === draft.source_node_id) continue;
    const position = flow.handle_positions[key] ?? handle.absolute_position;
    if (!position) continue;
    const distance = Math.hypot(position.x - canvasPoint.x, position.y - canvasPoint.y) * zoom;
    if (distance > radius) continue;
    const validation = flow.getConnectionValidation(draft.source_node_id, draft.source_handle_id, nodeId, handle.id, draft.reconnect_edge_id);
    if (validation.valid) {
      if (!best || distance < best.distance) best = { nodeId, handleId: handle.id, distance };
    } else if (!rejected || distance < rejected.distance) {
      rejected = { distance, reason: validation.reason ?? 'Connection rejected' };
    }
  }
  return { candidate: best, reason: best ? '' : rejected?.reason ?? '' };
}

/** Explains Kaykay's validation reasons in workflow terms. */
export function describeConnectionRejection(reason: string): string {
  if (/cycle/i.test(reason)) return 'That connection would create a loop; workflows must flow forward.';
  if (/already exists/i.test(reason)) return 'These ports are already connected.';
  if (/not accepted/i.test(reason)) return `Incompatible ports: ${reason}.`;
  if (/itself/i.test(reason)) return 'A step cannot connect to itself.';
  return reason;
}
