import { getContext, setContext } from 'svelte';

// Canvas cards are rendered by Kaykay, which only passes id/data/selected.
// The editor provides the few actions a card can trigger through context.
export interface WorkflowNodeActions {
  isCollapsed: (nodeId: string) => boolean;
  toggleCollapsed: (nodeId: string) => void;
}

const key = Symbol('workflow-node-actions');

export function setWorkflowNodeActions(actions: WorkflowNodeActions): void {
  setContext(key, actions);
}

export function getWorkflowNodeActions(): WorkflowNodeActions | undefined {
  return getContext<WorkflowNodeActions | undefined>(key);
}
