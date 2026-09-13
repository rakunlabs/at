import { getCapabilities, listWorkspaces, getWorkspacePreferences, rememberWorkspaceSelection, type EffectiveAccess, type Workspace, type WorkspacePreferences } from '../api/workspaces';
import { workspaceTransport, switchWorkspace, bindWorkspaceIdentity, adoptWorkspaceSelection } from '../api/transport';
import { storeAuth } from './auth.svelte';
import { chooseWorkspace } from '../helper/workspace-selection';
export const workspaceState = $state<{ items: Workspace[]; access: EffectiveAccess | null; loading: boolean; preferences: WorkspacePreferences }>({ items: [], access: null, loading: true, preferences: { mode: 'default', workspace_id: '', last_workspace_id: '' } });
export async function loadWorkspaceAccess() {
  workspaceState.loading = true;
  try {
    const identity = storeAuth.identity;
    if (!identity) return;
    bindWorkspaceIdentity(identity.subject, identity.claims?.session_id || '');
    const [items, preferences] = await Promise.all([listWorkspaces(), getWorkspacePreferences()]);
    workspaceState.items = items;
    workspaceState.preferences = preferences;
    const selected = chooseWorkspace(items, workspaceTransport.selected, preferences);
    if (workspaceState.access && workspaceState.access.workspace_id !== (selected?.id || '')) {
      await switchWorkspace(selected?.id || '');
      return;
    }
    if (selected) {
      if (workspaceTransport.selected !== selected.id) {
        await rememberWorkspaceSelection(selected.id);
        adoptWorkspaceSelection(selected.id);
        workspaceState.preferences = { ...preferences, last_workspace_id: selected.id };
      }
      workspaceState.access = await getCapabilities(selected.id);
    } else { workspaceState.access = null; if (workspaceTransport.selected) workspaceTransport.select(''); }
  } finally { workspaceState.loading = false; }
}
export function can(capability: string) { return workspaceState.access?.capabilities?.includes(capability) === true; }
