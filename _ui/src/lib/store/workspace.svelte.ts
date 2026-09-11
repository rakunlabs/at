import { getCapabilities, listWorkspaces, type EffectiveAccess, type Workspace } from '../api/workspaces';
import { workspaceTransport, switchWorkspace } from '../api/transport';
export const workspaceState = $state<{ items: Workspace[]; access: EffectiveAccess | null; loading: boolean }>({ items: [], access: null, loading: true });
export async function loadWorkspaceAccess() {
  workspaceState.loading = true;
  try {
    const items = await listWorkspaces();
    workspaceState.items = items;
    const selected = items.find(w => w.id === workspaceTransport.selected && !w.archived) || items.find(w => !w.archived);
    if (workspaceState.access && workspaceState.access.workspace_id !== (selected?.id || '')) {
      switchWorkspace(selected?.id || '');
      return;
    }
    if (selected) {
      if (workspaceTransport.selected !== selected.id) workspaceTransport.select(selected.id);
      workspaceState.access = await getCapabilities(selected.id);
    } else { workspaceState.access = null; if (workspaceTransport.selected) workspaceTransport.select(''); }
  } finally { workspaceState.loading = false; }
}
export function can(capability: string) { return workspaceState.access?.capabilities?.includes(capability) === true; }
