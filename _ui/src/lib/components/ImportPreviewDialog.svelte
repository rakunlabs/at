<script lang="ts">
  import { X, AlertTriangle, Check, SkipForward, RefreshCw } from 'lucide-svelte';
  import type { BundlePreview } from '@/lib/api/organizations';

  interface Props {
    preview: BundlePreview;
    onconfirm: (actions: Record<string, string>) => void;
    oncancel: () => void;
    importing?: boolean;
  }

  let { preview, onconfirm, oncancel, importing = false }: Props = $props();

  // Per-entity action decisions. Default: create_new for no conflict, ask for conflicts.
  let actions = $state<Record<string, string>>({});

  // Initialize actions from preview data.
  function initActions() {
    const a: Record<string, string> = {};
    if (preview.organization) {
      a[`organization:${preview.organization.name}`] = preview.organization.conflict ? 'skip' : 'create_new';
    }
    for (const agent of preview.agents || []) {
      a[`agent:${agent.name}`] = agent.conflict ? 'skip' : 'create_new';
    }
    for (const skill of preview.skills || []) {
      a[`skill:${skill.name}`] = skill.conflict ? 'skip' : 'create_new';
    }
    for (const ms of preview.mcp_sets || []) {
      a[`mcp_set:${ms.name}`] = ms.conflict ? 'skip' : 'create_new';
    }
    for (const ms of preview.mcp_servers || []) {
      a[`mcp_server:${ms.name}`] = ms.conflict ? 'skip' : 'create_new';
    }
    actions = a;
  }

  initActions();

  let hasConflicts = $derived(
    (preview.organization?.conflict ? 1 : 0) +
    (preview.agents || []).filter(a => a.conflict).length +
    (preview.skills || []).filter(s => s.conflict).length +
    (preview.mcp_sets || []).filter(m => m.conflict).length +
    (preview.mcp_servers || []).filter(m => m.conflict).length
  );

  function handleConfirm() {
    onconfirm(actions);
  }
</script>

<div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
  <div class="bg-dark-surface border border-dark-border w-full max-w-2xl max-h-[80vh] overflow-hidden flex flex-col shadow-lg">
    <!-- Header -->
    <div class="flex items-center justify-between px-4 py-3 border-b border-dark-border">
      <div class="flex items-center gap-2">
        <span class="text-sm font-medium text-dark-text">Import Preview</span>
        {#if hasConflicts > 0}
          <span class="flex items-center gap-1 px-2 py-0.5 text-xs bg-amber-900/20 text-amber-400 border border-amber-800">
            <AlertTriangle size={11} />
            {hasConflicts} conflict{hasConflicts > 1 ? 's' : ''}
          </span>
        {/if}
      </div>
      <button onclick={oncancel} class="p-1 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text-secondary">
        <X size={14} />
      </button>
    </div>

    <!-- Content -->
    <div class="flex-1 overflow-y-auto p-4 space-y-4">
      <!-- Organization -->
      {#if preview.organization}
        <div>
          <h3 class="text-xs font-medium text-dark-text-muted mb-2">Organization</h3>
          {@render entityRow('organization', preview.organization.name, preview.organization.conflict)}
        </div>
      {/if}

      <!-- Agents -->
      {#if (preview.agents || []).length > 0}
        <div>
          <h3 class="text-xs font-medium text-dark-text-muted mb-2">Agents ({preview.agents.length})</h3>
          <div class="space-y-1">
            {#each preview.agents as agent}
              {@render entityRow('agent', agent.name, agent.conflict)}
            {/each}
          </div>
        </div>
      {/if}

      <!-- Skills -->
      {#if (preview.skills || []).length > 0}
        <div>
          <h3 class="text-xs font-medium text-dark-text-muted mb-2">Skills ({preview.skills.length})</h3>
          <div class="space-y-1">
            {#each preview.skills as skill}
              {@render entityRow('skill', skill.name, skill.conflict)}
            {/each}
          </div>
        </div>
      {/if}

      <!-- MCP Sets -->
      {#if (preview.mcp_sets || []).length > 0}
        <div>
          <h3 class="text-xs font-medium text-dark-text-muted mb-2">MCP Sets ({preview.mcp_sets.length})</h3>
          <div class="space-y-1">
            {#each preview.mcp_sets as ms}
              {@render entityRow('mcp_set', ms.name, ms.conflict)}
            {/each}
          </div>
        </div>
      {/if}

      <!-- MCP Servers -->
      {#if (preview.mcp_servers || []).length > 0}
        <div>
          <h3 class="text-xs font-medium text-dark-text-muted mb-2">MCP Servers ({preview.mcp_servers.length})</h3>
          <div class="space-y-1">
            {#each preview.mcp_servers as ms}
              {@render entityRow('mcp_server', ms.name, ms.conflict)}
            {/each}
          </div>
        </div>
      {/if}

      <!-- Relationships -->
      {#if (preview.relationships || []).length > 0}
        <div>
          <h3 class="text-xs font-medium text-dark-text-muted mb-2">Relationships ({preview.relationships.length})</h3>
          <div class="space-y-1">
            {#each preview.relationships as rel}
              <div class="flex items-center gap-2 px-3 py-1.5 bg-dark-base/50 border border-dark-border text-xs">
                <span class="font-mono text-dark-text-secondary">{rel.agent_name}</span>
                {#if rel.role}
                  <span class="px-1.5 py-0.5 bg-blue-900/20 text-blue-400 border border-blue-800">{rel.role}</span>
                {/if}
                {#if rel.title}
                  <span class="text-dark-text-muted">- {rel.title}</span>
                {/if}
                {#if rel.is_head}
                  <span class="px-1.5 py-0.5 bg-green-900/20 text-green-400 border border-green-800">Head</span>
                {/if}
              </div>
            {/each}
          </div>
        </div>
      {/if}
    </div>

    <!-- Footer -->
    <div class="flex justify-end gap-2 px-4 py-3 border-t border-dark-border">
      <button
        onclick={oncancel}
        class="px-3 py-1.5 text-sm border border-dark-border-subtle hover:bg-dark-elevated text-dark-text-secondary"
      >
        Cancel
      </button>
      <button
        onclick={handleConfirm}
        disabled={importing}
        class="flex items-center gap-1.5 px-4 py-1.5 text-sm bg-accent text-dark-base hover:bg-accent-hover disabled:opacity-50"
      >
        {#if importing}
          <RefreshCw size={14} class="animate-spin" />
          Importing...
        {:else}
          <Check size={14} />
          Confirm Import
        {/if}
      </button>
    </div>
  </div>
</div>

{#snippet entityRow(type: string, name: string, conflict?: string)}
  <div class="flex items-center justify-between px-3 py-1.5 bg-dark-base/50 border border-dark-border">
    <div class="flex items-center gap-2 min-w-0">
      <span class="font-mono text-sm text-dark-text-secondary truncate">{name}</span>
      {#if conflict}
        <span class="shrink-0 flex items-center gap-1 px-1.5 py-0.5 text-[10px] bg-amber-900/20 text-amber-400 border border-amber-800">
          <AlertTriangle size={9} />
          exists
        </span>
      {/if}
    </div>
    {#if conflict}
      <select
        value={actions[`${type}:${name}`] || 'skip'}
        onchange={(e) => { actions[`${type}:${name}`] = (e.target as HTMLSelectElement).value; }}
        class="text-xs border border-dark-border-subtle bg-dark-elevated px-2 py-1 text-dark-text-secondary focus:outline-none"
      >
        <option value="create_new">Create New</option>
        <option value="skip">Skip</option>
        <option value="overwrite">Overwrite</option>
      </select>
    {:else}
      <span class="flex items-center gap-1 text-[10px] text-green-400">
        <Check size={10} />
        Will create
      </span>
    {/if}
  </div>
{/snippet}
