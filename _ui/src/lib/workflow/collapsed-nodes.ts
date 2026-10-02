// Collapsing is a viewing preference, not part of the workflow: it must not
// mark the graph dirty, enter undo history or invalidate pinned test outputs.
// It is therefore kept per browser, keyed by workflow.
const storagePrefix = 'at.workflow.collapsed.';

export function loadCollapsedNodes(workflowId: string, storage: Pick<Storage, 'getItem'> | undefined = globalThis.localStorage): Set<string> {
  try {
    const parsed = JSON.parse(storage?.getItem(storagePrefix + workflowId) ?? '[]');
    return new Set(Array.isArray(parsed) ? parsed.filter((id): id is string => typeof id === 'string') : []);
  } catch {
    return new Set();
  }
}

export function saveCollapsedNodes(workflowId: string, ids: Iterable<string>, storage: Pick<Storage, 'setItem' | 'removeItem'> | undefined = globalThis.localStorage): void {
  try {
    const list = [...ids];
    if (list.length) storage?.setItem(storagePrefix + workflowId, JSON.stringify(list));
    else storage?.removeItem(storagePrefix + workflowId);
  } catch {
    // Storage may be unavailable (private mode, quota); collapsing still works for the session.
  }
}
