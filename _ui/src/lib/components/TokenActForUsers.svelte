<script lang="ts">
  import { UserCheck } from 'lucide-svelte';

  interface Props {
    actForUsers: boolean;
    agentsMode: 'all' | 'none' | 'list';
    agents: string[];
    /** Agent names to choose from. */
    available: string[];
    /** Only workspace administrators may change it; the server enforces it. */
    canManage: boolean;
  }
  let { actForUsers = $bindable(), agentsMode = $bindable(), agents = $bindable(), available, canManage }: Props = $props();

  function toggle(name: string) {
    agents = agents.includes(name) ? agents.filter(a => a !== name) : [...agents, name];
  }
</script>

<div class="grid grid-cols-4 gap-3 mb-3">
  <span class="text-xs text-dark-text-secondary py-2 flex items-start gap-1"><UserCheck size={12} class="mt-0.5" /> Act for users</span>
  <div class="col-span-3 space-y-2">
    <label class="flex items-start gap-2 text-xs text-dark-text-secondary">
      <input type="checkbox" class="mt-0.5 accent-accent" bind:checked={actForUsers} disabled={!canManage} />
      <span>
        Run agents on behalf of signed-in people
        <span class="block text-dark-text-muted">
          For a website or app backend. It calls <code>POST /gateway/v1/agents/&#123;agent&#125;/chat</code> with this token and the person's token from an identity provider that has token exchange on.
          The agent runs as that person — their workspace access, MCP connections and budgets — never as the token's creator.
          {#if !canManage}Only workspace administrators can change this.{/if}
        </span>
      </span>
    </label>
    {#if actForUsers}
      <div class="flex gap-1">
        {#each ['all', 'none', 'list'] as mode}
          <button
            type="button"
            disabled={!canManage}
            onclick={() => { agentsMode = mode as 'all' | 'none' | 'list'; }}
            class={[
              'px-2.5 py-1 text-xs font-medium border',
              agentsMode === mode ? 'bg-accent text-dark-base border-accent' : 'bg-dark-elevated text-dark-text-muted border-dark-border hover:border-dark-border-subtle',
            ]}
          >{mode === 'all' ? 'All agents' : mode === 'none' ? 'None' : 'Custom'}</button>
        {/each}
      </div>
      {#if agentsMode === 'list'}
        {#if available.length}
          <div class="flex flex-wrap gap-1.5">
            {#each available as name}
              <button
                type="button"
                disabled={!canManage}
                onclick={() => toggle(name)}
                class={[
                  'px-2 py-1 text-xs border',
                  agents.includes(name) ? 'bg-accent text-dark-base border-accent' : 'bg-dark-elevated text-dark-text-secondary border-dark-border hover:border-dark-border-subtle',
                ]}
              >{name}</button>
            {/each}
          </div>
        {:else}
          <span class="text-xs text-dark-text-muted">No agents available</span>
        {/if}
      {:else if agentsMode === 'none'}
        <p class="text-xs text-red-400">No agent can be run through this token</p>
      {:else}
        <p class="text-xs text-dark-text-muted">Every agent the person can use. Prefer listing the chatbot's agent.</p>
      {/if}
    {/if}
  </div>
</div>
