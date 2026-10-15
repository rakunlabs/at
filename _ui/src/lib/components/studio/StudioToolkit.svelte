<script lang="ts">
  import { untrack } from 'svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listMCPSets, type MCPSet } from '@/lib/api/mcp-sets';
  import { listSkills, type Skill } from '@/lib/api/skills';
  import { listBuiltinTools, type BuiltinToolDef } from '@/lib/api/mcp';
  import {
    CAPABILITY_INFO,
    STUDIO_CAPABILITIES,
    STUDIO_LIBRARY_SKILL,
    capabilityConfigured,
    type StudioCapability,
    type StudioToolkit,
  } from '@/lib/helper/studio-toolkit';
  import { Loader2, Plus, Save, X } from 'lucide-svelte';

  interface Props {
    toolkit: StudioToolkit;
    agentCount: number;
    onSave: (toolkit: StudioToolkit) => Promise<void>;
  }
  let { toolkit, agentCount, onSave }: Props = $props();

  type SourceKey = 'mcp_sets' | 'skills' | 'builtin_tools';
  const SOURCE_LABEL: Record<SourceKey, string> = { mcp_sets: 'MCP set', skills: 'Skill', builtin_tools: 'Built-in' };
  // Studio state tools are attached to every production agent already.
  const RESERVED_SKILLS = new Set([STUDIO_LIBRARY_SKILL, 'series_library']);

  let draft = $state<StudioToolkit>(untrack(() => structuredClone($state.snapshot(toolkit))));
  let baseline = $state(untrack(() => JSON.stringify(toolkit)));
  let mcpSets = $state<MCPSet[]>([]);
  let skills = $state<Skill[]>([]);
  let builtins = $state<BuiltinToolDef[]>([]);
  let catalogError = $state('');
  let loadingCatalog = $state(true);
  let saving = $state(false);
  let picks = $state<Record<StudioCapability, string>>({ image: '', video: '', speech: '', lipsync: '', stock: '' });

  let dirty = $derived(JSON.stringify(draft) !== baseline);

  $effect(() => {
    const next = JSON.stringify(toolkit);
    untrack(() => {
      if (next !== baseline && !dirty) {
        draft = structuredClone($state.snapshot(toolkit));
        baseline = next;
      }
    });
  });

  async function loadCatalog() {
    loadingCatalog = true;
    catalogError = '';
    const [sets, skillList, builtinList] = await Promise.allSettled([
      listMCPSets({ _limit: 500 }),
      listSkills({ _limit: 500 }),
      listBuiltinTools(),
    ]);
    if (sets.status === 'fulfilled') mcpSets = sets.value.data || [];
    if (skillList.status === 'fulfilled') skills = (skillList.value.data || []).filter((s) => !RESERVED_SKILLS.has(s.name));
    if (builtinList.status === 'fulfilled') builtins = builtinList.value.tools || [];
    const failed = [
      sets.status === 'rejected' ? 'MCP sets' : '',
      skillList.status === 'rejected' ? 'skills' : '',
      builtinList.status === 'rejected' ? 'built-in tools' : '',
    ].filter(Boolean);
    if (failed.length) catalogError = `Could not load ${failed.join(', ')}.`;
    loadingCatalog = false;
  }

  function encode(kind: SourceKey, name: string) {
    return `${kind}:${name}`;
  }

  function add(cap: StudioCapability) {
    const value = picks[cap];
    if (!value) return;
    const at = value.indexOf(':');
    const kind = value.slice(0, at) as SourceKey;
    const name = value.slice(at + 1);
    const list = draft.capabilities[cap][kind];
    if (!list.includes(name)) list.push(name);
    picks[cap] = '';
  }

  function remove(cap: StudioCapability, kind: SourceKey, name: string) {
    draft.capabilities[cap][kind] = draft.capabilities[cap][kind].filter((n) => n !== name);
  }

  function description(kind: SourceKey, name: string): string {
    if (kind === 'mcp_sets') return mcpSets.find((s) => s.name === name)?.description || '';
    if (kind === 'skills') return skills.find((s) => s.name === name)?.description || '';
    return builtins.find((t) => t.name === name)?.description || '';
  }

  function known(kind: SourceKey, name: string): boolean {
    if (loadingCatalog || catalogError) return true;
    if (kind === 'mcp_sets') return mcpSets.some((s) => s.name === name);
    if (kind === 'skills') return skills.some((s) => s.name === name);
    return builtins.some((t) => t.name === name);
  }

  async function save() {
    saving = true;
    try {
      await onSave(structuredClone($state.snapshot(draft)));
      baseline = JSON.stringify(draft);
    } catch (e: any) {
      addToast(e?.response?.data?.message || e?.message || 'Failed to save toolkit', 'alert');
    } finally {
      saving = false;
    }
  }

  loadCatalog();
</script>

<div class="space-y-4">
  <div class="flex flex-col sm:flex-row sm:items-start gap-3">
    <div class="min-w-0 flex-1">
      <h2 class="text-sm font-semibold text-dark-text">Toolkit</h2>
      <p class="mt-0.5 text-xs leading-5 text-dark-text-muted">
        Pick which tools the studio agents use for each capability: any MCP set (FAL, Replicate, Runway, your own server…), skill or built-in tool.
        Saving attaches them to the {agentCount} studio agent{agentCount === 1 ? '' : 's'}; agents read this page through <code>studio_toolkit</code> and call the real tools.
      </p>
    </div>
    <button onclick={save} disabled={saving || !dirty} class="inline-flex items-center justify-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-accent text-dark-base hover:bg-accent-hover disabled:opacity-50">
      {#if saving}<Loader2 size={12} class="animate-spin" />{:else}<Save size={12} />{/if} Save and apply
    </button>
  </div>

  {#if catalogError}
    <p class="px-3 py-2 text-xs bg-amber-950 text-amber-300">{catalogError} <button class="underline" onclick={loadCatalog}>Retry</button></p>
  {/if}

  <div class="border border-dark-border divide-y divide-dark-border">
    {#each STUDIO_CAPABILITIES as cap (cap)}
      {@const sources = draft.capabilities[cap]}
      <section class="px-4 py-3 grid grid-cols-1 lg:grid-cols-[180px_1fr] gap-3">
        <div>
          <div class="flex items-center gap-2">
            <h3 class="text-xs font-semibold text-dark-text">{CAPABILITY_INFO[cap].label}</h3>
            <span class={['text-[10px]', capabilityConfigured(sources) ? 'text-oc-green' : 'text-dark-text-faint']}>{capabilityConfigured(sources) ? 'configured' : 'not set'}</span>
          </div>
          <p class="mt-1 text-[11px] leading-4 text-dark-text-muted">{CAPABILITY_INFO[cap].hint}</p>
        </div>
        <div class="space-y-2 min-w-0">
          <div class="flex flex-wrap gap-1.5">
            {#each ['mcp_sets', 'skills', 'builtin_tools'] as const as kind}
              {#each sources[kind] as name (name)}
                <span class={['inline-flex items-center gap-1 border px-2 py-0.5 text-[11px]', known(kind, name) ? 'border-dark-border text-dark-text-secondary' : 'border-oc-red text-oc-red']} title={known(kind, name) ? description(kind, name) : 'Not found: it was deleted or is not visible in this workspace'}>
                  <span class="text-dark-text-faint">{SOURCE_LABEL[kind]}</span> {name}
                  <button onclick={() => remove(cap, kind, name)} class="text-dark-text-muted hover:text-oc-red" aria-label={`Remove ${name}`}><X size={10} /></button>
                </span>
              {/each}
            {/each}
          </div>
          <div class="flex gap-1.5">
            <select bind:value={picks[cap]} aria-label={`Add a ${CAPABILITY_INFO[cap].label} tool source`} disabled={loadingCatalog} class="min-w-0 flex-1 px-2 py-1.5 text-xs border border-dark-border bg-dark-base text-dark-text">
              <option value="">{loadingCatalog ? 'Loading tools…' : 'Add a tool source…'}</option>
              {#if mcpSets.length}
                <optgroup label="MCP sets">
                  {#each mcpSets.filter((s) => !sources.mcp_sets.includes(s.name)) as set (set.id)}<option value={encode('mcp_sets', set.name)}>{set.name}{set.description ? ` — ${set.description.slice(0, 60)}` : ''}</option>{/each}
                </optgroup>
              {/if}
              {#if skills.length}
                <optgroup label="Skills">
                  {#each skills.filter((s) => !sources.skills.includes(s.name)) as skill (skill.id)}<option value={encode('skills', skill.name)}>{skill.name}{skill.description ? ` — ${skill.description.slice(0, 60)}` : ''}</option>{/each}
                </optgroup>
              {/if}
              {#if builtins.length}
                <optgroup label="Built-in tools">
                  {#each builtins.filter((t) => !sources.builtin_tools.includes(t.name)) as tool (tool.name)}<option value={encode('builtin_tools', tool.name)}>{tool.name}</option>{/each}
                </optgroup>
              {/if}
            </select>
            <button onclick={() => add(cap)} disabled={!picks[cap]} class="inline-flex items-center gap-1 px-2.5 py-1.5 text-xs border border-dark-border text-dark-text-secondary hover:bg-dark-elevated disabled:opacity-40"><Plus size={11} /> Add</button>
          </div>
          <input bind:value={sources.notes} aria-label={`${CAPABILITY_INFO[cap].label} guidance`} placeholder="Guidance for agents (e.g. prefer kling for finals, veo only on request, keep clips ≤ 8s)" class="w-full px-2 py-1.5 text-xs border border-dark-border bg-dark-base text-dark-text" />
        </div>
      </section>
    {/each}
  </div>

  <label class="block text-xs text-dark-text-muted">
    General guidance
    <textarea bind:value={draft.notes} rows="2" placeholder="Budget rules, default quality, anything every production agent should know." class="mt-1 w-full px-2 py-1.5 text-xs border border-dark-border bg-dark-base text-dark-text resize-y"></textarea>
  </label>
  <p class="text-[11px] leading-4 text-dark-text-muted">
    Need a provider that is not listed? Add it as an MCP set on the <a href="#/mcps" class="underline underline-offset-2">MCP sets</a> page (or install one from Marketplaces), then pick it here. Credentials stay with the MCP set.
  </p>
</div>
