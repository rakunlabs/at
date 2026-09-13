<script lang="ts">
  import { onMount } from 'svelte';
  import { workspaceAPI } from '../lib/api/workspaces';
  import { workspaceTransport } from '../lib/api/transport';
  import { isNativeAdmin } from '../lib/store/auth.svelte';
  import { authErrorMessage } from '../lib/api/auth';

  interface Policy {
    workspace_id: string;
    mode: string;
    allowed_tools: string[];
    allowed_nodes: string[];
    allow_all_tools: boolean;
    allow_all_nodes: boolean;
    version: number;
    granted_by?: string;
  }
  let policy = $state<Policy | null>(null);
  let availableTools = $state<string[]>([]);
  let availableNodes = $state<string[]>([]);
  let toolSearch = $state('');
  let extraTools = $state('');
  let busy = $state(false);
  let error = $state('');
  let notice = $state('');
  let filteredTools = $derived(availableTools.filter(name => name.includes(toolSearch.trim().toLowerCase())));
  const path = () => `workspaces/${encodeURIComponent(workspaceTransport.selected)}/execution-policy`;

  async function load() {
    busy = true;
    error = notice = '';
    try {
      const data = (await workspaceAPI.get(path())).data;
      availableTools = data.available_tools || [];
      availableNodes = data.available_nodes || [];
      policy = { ...data.policy, allowed_tools: data.policy.allowed_tools || [], allowed_nodes: data.policy.allowed_nodes || [], allow_all_tools: !!data.policy.allow_all_tools, allow_all_nodes: !!data.policy.allow_all_nodes };
      extraTools = policy!.allowed_tools.filter(name => !availableTools.includes(name)).join('\n');
    } catch (e) {
      error = authErrorMessage(e, 'Execution policy is unavailable. Select a workspace and retry.');
    } finally {
      busy = false;
    }
  }
  onMount(() => { if (workspaceTransport.selected) void load(); });

  function allowEverything() {
    if (!policy) return;
    policy.mode = 'trusted_host';
    policy.allow_all_tools = true;
    policy.allow_all_nodes = true;
    notice = 'All tools and node types selected with Trusted host mode. Save to apply.';
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    if (!policy || busy) return;
    busy = true;
    error = notice = '';
    try {
      const allowed_tools = [...new Set([
        ...policy.allowed_tools.filter(name => availableTools.includes(name)),
        ...extraTools.split('\n').map(name => name.trim()).filter(Boolean),
      ])];
      policy = (await workspaceAPI.put(path(), { ...policy, allowed_tools })).data;
      notice = 'Execution policy saved. Renew the execution identity for existing bots and MCP servers to use these permissions.';
    } catch (e) {
      error = authErrorMessage(e, 'Could not save policy. Reload the latest version before retrying.');
    } finally {
      busy = false;
    }
  }
</script>

<div class="settings-page settings-form">
  <header>
    <h1 class="text-2xl font-semibold">Execution policy</h1>
    <p class="settings-note mt-2">Choose what agents, bots and MCP servers can run in the selected workspace.</p>
  </header>
  {#if error}<p role="alert" class="settings-error">{error}</p>{/if}
  {#if notice}<p role="status" class="settings-note">{notice}</p>{/if}
  <button class="settings-button" disabled={busy || !workspaceTransport.selected} onclick={load}>Reload policy</button>
  {#if policy}
    <form class="settings-section space-y-5" onsubmit={save}>
      <fieldset disabled={!isNativeAdmin() || busy} class="space-y-5">
        {#if isNativeAdmin()}
          <div class="space-y-2">
            <button type="button" class="settings-button" onclick={allowEverything}>Allow all tools and node types</button>
            <p class="settings-note">Selects Trusted host mode and all current and future tools, skills, MCP tools, delegates and node types. Save below to apply.</p>
          </div>
        {/if}
        <label>Execution mode
          <select bind:value={policy.mode}>
            <option value="restricted">Restricted</option>
            <option value="trusted_host">Trusted host</option>
            <option value="isolated_worker" disabled>Isolated worker — unavailable</option>
          </select>
        </label>
        <p class="settings-note">{policy.mode === 'trusted_host' ? 'Tools and handlers may run on this host according to the permissions below.' : 'Restricted mode supports only non-host operations. Bash, skill handlers and builtin management tools require Trusted host.'}</p>

        <section class="space-y-3" aria-labelledby="execution-tools-title">
          <h2 id="execution-tools-title" class="font-medium">Tools</h2>
          <label class="flex items-center gap-2">
            <input type="checkbox" bind:checked={policy.allow_all_tools} />
            <span>Allow all tools</span>
          </label>
          {#if policy.allow_all_tools}
            <p class="settings-note">Includes builtin tools, skills, MCP tools, inline tools and delegation. No names to enter.</p>
          {:else}
            <label>Find a tool<input type="search" bind:value={toolSearch} placeholder="Search tools…" /></label>
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-2 max-h-64 overflow-y-auto p-1">
              {#each filteredTools as name (name)}
                <label class="flex items-start gap-2 min-w-0">
                  <input type="checkbox" bind:group={policy.allowed_tools} value={name} />
                  <span class="text-sm break-all">{name.replaceAll('_', ' ')}</span>
                </label>
              {:else}<p class="settings-note">No matching tools.</p>{/each}
            </div>
            <details>
              <summary class="cursor-pointer text-sm">Advanced: specific skill, MCP or delegation permissions</summary>
              <label class="mt-3">Additional permissions
                <textarea rows="4" bind:value={extraTools} placeholder="One permission per line"></textarea>
                <span class="settings-note">Formats: skill_tool:&lt;skill-id&gt;:&lt;tool-name&gt;, mcp_tool:&lt;set-or-endpoint&gt;:&lt;tool-name&gt;, delegate:&lt;agent-or-workflow-id&gt;:&lt;tool-name&gt;.</span>
              </label>
            </details>
          {/if}
        </section>

        <section class="space-y-3" aria-labelledby="execution-nodes-title">
          <h2 id="execution-nodes-title" class="font-medium">Workflow node types</h2>
          <label class="flex items-center gap-2">
            <input type="checkbox" bind:checked={policy.allow_all_nodes} />
            <span>Allow all node types</span>
          </label>
          {#if policy.allow_all_nodes}
            <p class="settings-note">Includes all current and future registered node types.</p>
          {:else}
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-2">
              {#each availableNodes as name (name)}
                <label class="flex items-start gap-2 min-w-0">
                  <input type="checkbox" bind:group={policy.allowed_nodes} value={name} />
                  <span class="text-sm break-all">{name.replaceAll('_', ' ')}</span>
                </label>
              {/each}
            </div>
          {/if}
        </section>
        <p class="settings-note">Account and workspace permissions still apply. Policy version {policy.version}.</p>
        {#if isNativeAdmin()}
          <button class="settings-primary">Save execution policy</button>
        {:else}
          <p class="settings-note">Only an installation administrator can change execution policy.</p>
        {/if}
      </fieldset>
    </form>
  {:else if !workspaceTransport.selected}
    <p class="settings-note">Select a workspace first.</p>
  {/if}
</div>
