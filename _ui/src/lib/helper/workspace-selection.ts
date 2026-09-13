import type { Workspace, WorkspacePreferences } from '../api/workspaces';

export function chooseWorkspace(items: Workspace[], current: string, preference: WorkspacePreferences): Workspace | undefined {
  const available = items.filter(w => !w.archived);
  const requested = preference.mode === 'workspace' ? preference.workspace_id : preference.mode === 'last_used' ? preference.last_workspace_id : '';
  return available.find(w => w.id === current)
    || available.find(w => w.id === requested)
    || available.find(w => w.id === 'legacy-default')
    || available.slice().sort((a, b) => a.created_at.localeCompare(b.created_at) || a.id.localeCompare(b.id))[0];
}

// A new login family starts from the account's preference. Reloads and switches
// within that family keep each tab's selection; another account cannot inherit it.
export function workspaceSelectionKey(basePath: string, userID: string, sessionID: string): string {
  return `at-workspace:${basePath}:${encodeURIComponent(userID)}:${encodeURIComponent(sessionID)}`;
}
