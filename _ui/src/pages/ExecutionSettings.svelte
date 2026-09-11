<script lang="ts">
  import { onMount } from 'svelte';
  import { workspaceAPI } from '../lib/api/workspaces';
  import { workspaceTransport } from '../lib/api/transport';
  import { isNativeAdmin } from '../lib/store/auth.svelte';
  import { authErrorMessage } from '../lib/api/auth';
  interface Policy { workspace_id: string; mode: string; allowed_tools: string[]; allowed_nodes: string[]; version: number; granted_by?: string }
  let policy = $state<Policy | null>(null); let tools = $state(''); let nodes = $state(''); let busy = $state(false); let error = $state(''); let notice = $state('');
  const path = `workspaces/${encodeURIComponent(workspaceTransport.selected)}/execution-policy`;
  async function load() { busy = true; error = ''; try { policy = (await workspaceAPI.get(path)).data.policy; tools = (policy?.allowed_tools || []).join('\n'); nodes = (policy?.allowed_nodes || []).join('\n'); } catch (e) { error = authErrorMessage(e, 'Execution policy is unavailable. Select a workspace and retry.'); } finally { busy = false; } }
  onMount(() => { if (workspaceTransport.selected) void load(); });
  async function save(e: SubmitEvent) { e.preventDefault(); if (!policy || busy) return; busy = true; error = notice = ''; try { policy = (await workspaceAPI.put(path, { ...policy, allowed_tools: tools.split('\n').map(v => v.trim()).filter(Boolean), allowed_nodes: nodes.split('\n').map(v => v.trim()).filter(Boolean) })).data; notice = 'Execution policy saved. Existing runs with the previous policy version will stop.'; } catch (e) { error = authErrorMessage(e, 'Could not save policy. Reload the latest version before retrying.'); } finally { busy = false; } }
</script>
<div class="settings-page settings-form"><header><h1 class="text-2xl font-semibold">Execution policy</h1><p class="settings-note mt-2">Platform-controlled execution authority for the selected workspace.</p></header>
  {#if error}<p role="alert" class="settings-error">{error}</p>{/if}{#if notice}<p role="status" class="settings-note">{notice}</p>{/if}<button class="settings-button" disabled={busy || !workspaceTransport.selected} onclick={load}>Reload policy</button>
  {#if policy}<form class="settings-section space-y-5" onsubmit={save}><fieldset disabled={!isNativeAdmin() || busy} class="space-y-5">
    <label>Execution mode<select bind:value={policy.mode}><option value="restricted">Restricted</option><option value="trusted_host">Trusted host</option><option value="isolated_worker" disabled>Isolated worker — unavailable</option></select></label>
    <p class="settings-note">{policy.mode === 'trusted_host' ? 'Trusted host permits explicitly allowed host execution. This is a trusted deployment mode, not tenant isolation.' : 'Restricted mode permits only supported, explicitly allowed operations. Host bash and executable handlers require trusted-host authority.'} An isolated worker runner is not implemented.</p>
    <label>Allowed tool names<textarea rows="6" bind:value={tools} placeholder="One tool name per line"></textarea><span class="settings-note">Use exact registered names. Dynamic entries use skill_tool:&lt;skill-id&gt;:&lt;tool-name&gt;, mcp_tool:&lt;set-or-endpoint&gt;:&lt;tool-name&gt;, or delegate:&lt;agent-or-workflow-id&gt;:&lt;tool-name&gt;.</span></label>
    <label>Allowed node types<textarea rows="5" bind:value={nodes} placeholder="One node type per line"></textarea></label>
    <p class="settings-note">Empty lists allow no tools or nodes. All actions still require live membership, capability, resource and policy checks. Version {policy.version}.</p>
    {#if isNativeAdmin()}<button class="settings-primary">Save execution policy</button>{:else}<p class="settings-note">Only an installation administrator can change execution policy. Workspace administrator roles do not grant host authority.</p>{/if}
  </fieldset></form>{:else if !workspaceTransport.selected}<p class="settings-note">Select a workspace first.</p>{/if}
</div>
