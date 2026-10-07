<script lang="ts">
  import { listAgents, type Agent } from '@/lib/api/agents';

  let { data, providers = [] }: { data: Record<string, any>; providers?: any[] } = $props();

  let agents = $state<Agent[]>([]);
  let agentsLoaded = $state(false);
  let selectedAgent = $derived(agents.find(a => a.id === data.agent_id));
  let groupedAgents = $derived.by(() => {
    const groups = new Map<string, Agent[]>();
    for (const a of [...agents].sort((x, y) => x.name.localeCompare(y.name))) {
      const group = a.config?.group || '';
      groups.set(group, [...(groups.get(group) ?? []), a]);
    }
    return [...groups.entries()].sort(([a], [b]) => (a === '' ? 1 : b === '' ? -1 : a.localeCompare(b)));
  });
  let effectiveProvider = $derived(data.provider || selectedAgent?.config?.provider || '');
  let selectedProvider = $derived(providers.find(p => p.key === effectiveProvider));
  let availableModels = $derived(
    selectedProvider?.config?.models?.length
      ? selectedProvider.config.models
      : selectedProvider?.config?.model
        ? [selectedProvider.config.model]
        : []
  );
  let agentResources = $derived.by(() => {
    const c = selectedAgent?.config;
    if (!c) return [];
    return [
      { label: 'Skills', count: c.skills?.length ?? 0 },
      { label: 'MCP sets', count: c.mcp_sets?.length ?? 0 },
      { label: 'MCP URLs', count: c.mcp_urls?.length ?? 0 },
      { label: 'Built-in tools', count: c.builtin_tools?.length ?? 0 },
      { label: 'Workflows', count: c.workflows?.length ?? 0 },
    ].filter(r => r.count > 0);
  });

  listAgents({ _limit: 500 })
    .then(res => agents = res.data ?? [])
    .catch(() => {})
    .finally(() => agentsLoaded = true);

  function selectAgent(id: string) {
    data.agent_id = id;
    // A stored agent supplies its own provider/model; drop stale inline
    // values so they do not silently override it.
    if (id) {
      data.provider = '';
      data.model = '';
    }
  }
</script>

<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Agent</span>
  <select
    value={data.agent_id ?? ''}
    onchange={(e) => selectAgent((e.currentTarget as HTMLSelectElement).value)}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
  >
    <option value="">Custom (configure inline)</option>
    {#each groupedAgents as [group, items] (group)}
      {#if group}
        <optgroup label={group}>
          {#each items as a (a.id)}
            <option value={a.id}>{a.name}</option>
          {/each}
        </optgroup>
      {:else}
        {#each items as a (a.id)}
          <option value={a.id}>{a.name}</option>
        {/each}
      {/if}
    {/each}
    {#if data.agent_id && agentsLoaded && !selectedAgent}
      <option value={data.agent_id}>Unavailable agent ({data.agent_id})</option>
    {/if}
  </select></label>
  {#if selectedAgent}
    <div class="mt-1 border border-dark-border px-2 py-1.5 text-[10px] text-dark-text-secondary space-y-0.5">
      {#if selectedAgent.config?.description}
        <div class="text-dark-text-secondary">{selectedAgent.config.description}</div>
      {/if}
      <div class="font-mono">{selectedAgent.config?.provider || '—'}{selectedAgent.config?.model ? ' / ' + selectedAgent.config.model : ''}</div>
      {#if agentResources.length}
        <div>{agentResources.map(r => `${r.count} ${r.label}`).join(' · ')}</div>
      {/if}
      <div class="text-dark-text-muted">The agent's system prompt, skills, MCP sets, built-in tools and workflows are used.</div>
    </div>
  {:else if data.agent_id && agentsLoaded}
    <div class="mt-0.5 text-[10px] text-red-500">This agent no longer exists or is not visible to you.</div>
  {:else}
    <div class="mt-0.5 text-[10px] text-dark-text-muted">Pick an agent to reuse its prompt, skills and MCP tools, or configure a provider inline.</div>
  {/if}
</div>

<div class="h-px bg-dark-highest my-2"></div>

<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Provider {data.agent_id ? '(Override)' : ''}</span>
  <select
    bind:value={data.provider}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
  >
    <option value="">{data.agent_id ? 'Agent default' : 'Select provider'}</option>
    {#each providers as p}
      <option value={p.key}>{p.key}</option>
    {/each}
  </select></label>
</div>
<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Model {data.agent_id ? '(Override)' : ''}</span>
  <select
    bind:value={data.model}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
  >
    <option value="">{data.agent_id ? 'Agent default' : 'Select model'}</option>
    {#each availableModels as m}
      <option value={m}>{m}</option>
    {/each}
  </select></label>
</div>
<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">System Prompt {data.agent_id ? '(Appended)' : ''}</span>
  <textarea
    bind:value={data.system_prompt}
    rows={3}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle resize-y"
    placeholder="System prompt (optional)"
  ></textarea></label>
</div>
<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Max Iterations {data.agent_id ? '(Override)' : ''}</span>
  <input
    type="number"
    bind:value={data.max_iterations}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
    placeholder={data.agent_id ? "Default from Agent" : "10"}
    min="1"
  /></label>
  <div class="mt-0.5 text-[10px] text-dark-text-muted">Min 1; the platform clamps to its iteration ceiling</div>
</div>
<!-- Port descriptions -->
<div class="border-t border-dark-border pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="The main user message or instruction sent to the agent. Required. Falls back to 'text' or 'data' inputs if the prompt port is not connected.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">prompt</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Main instruction sent to the agent (required)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">string</div>
      </div>
      <div title="Optional supplementary data appended to the prompt under a 'Context:' header.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">context</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Extra reference data appended to prompt (optional)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">string</div>
      </div>
      <div title="Additional MCP server URLs merged with static config. Connect from an mcp_config node.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">mcp</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— MCP server URLs from mcp_config node (optional)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">string[]</div>
      </div>
      <div title="Additional skill names merged with static config. Connect from a skill_config node.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">skills</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Skill names from skill_config node (optional)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">string[]</div>
      </div>
      <div title="Files sent to the model with the prompt: images, PDFs, audio, video or text. Connect the file output of HTTP Request (save response), Exec paths, or the files/image output of another LLM or Agent Call. Remote URLs must be downloaded with HTTP Request first. Up to 10 files, 20 MB total.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">attachments</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Images, PDFs, audio, text… sent with the prompt (optional)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"file ref | path | [..] | { name, content_base64 } | data: URL"}</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="The final LLM response text after the agentic loop completes (all tool calls resolved).">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">response</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Final agent response after tool-call loop</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"{ response: string }"}</div>
      </div>
      <div title="Every file left in the agent's output directory: files its tools saved (the directory is their working directory) and images the model returned directly. Connect to Email attachments or inline images.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">files</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Files the agent produced ([] when none)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"[{ path, name, content_type, size_bytes }]"}</div>
      </div>
      <div title="The first produced file that is an image. Absent when there is none.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">image</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— First produced image</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"{ path, name, content_type, size_bytes }"}</div>
      </div>
    </div>
  </div>
</div>
