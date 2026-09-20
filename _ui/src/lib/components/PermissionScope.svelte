<script lang="ts">
  import { workspaceAPI } from '../api/workspaces';
  import { authErrorMessage } from '../api/auth';
  import type { ListResult } from '../api/types';

  interface Resource { id: string; name?: string; title?: string; key?: string }
  interface Props {
    capability: string;
    ids?: string[];
    patterns?: string[];
    onids: (value: string[] | undefined) => void;
    onpatterns: (value: string[] | undefined) => void;
  }
  let { capability, ids, patterns, onids, onpatterns }: Props = $props();
  // Only catalogs with record-ID admission. Connections return an unpaged array.
  const catalogs: Record<string, string> = {
    agents: 'agents', organizations: 'organizations', projects: 'projects',
    goals: 'goals', tasks: 'tasks', workflows: 'workflows', bots: 'bots',
    skills: 'skills', providers: 'providers', tokens: 'api-tokens',
    labels: 'labels', variables: 'variables', node_configs: 'node-configs',
    connections: 'connections',
  };
  let endpoint = $derived(catalogs[capability.split('.')[0]]);
  let resources = $state<Resource[]>([]);
  let query = $state('');
  let loading = $state(false);
  let loaded = $state(false);
  let error = $state('');
  let offset = $state(0);
  let more = $state(false);
  let visible = $derived(resources.filter(r => `${r.name || r.title || r.key || ''} ${r.id}`.toLowerCase().includes(query.toLowerCase())));
  let missing = $derived((ids || []).filter(id => !resources.some(r => r.id === id)));

  async function load(reset = false) {
    if (!endpoint || loading) return;
    loading = true; error = '';
    try {
      const start = reset ? 0 : offset;
      const { data } = await workspaceAPI.get<ListResult<Resource> | Resource[]>(endpoint, ['connections', 'labels'].includes(endpoint) ? {} : { params: { _limit: 100, _offset: start, _sort: 'id' } });
      const rows = Array.isArray(data) ? data : data.data || [];
      const merged = reset ? rows : [...resources, ...rows];
      resources = [...new Map(merged.map(r => [r.id, r])).values()];
      offset = start + rows.length;
      more = !Array.isArray(data) && offset < data.meta.total && rows.length > 0;
      loaded = true;
    } catch (e) { error = authErrorMessage(e, 'Could not load resources. Check your read access and retry.'); }
    finally { loading = false; }
  }
  $effect(() => { if (ids !== undefined && endpoint && !loaded && !loading && !error) void load(); });
  function toggle(id: string, checked: boolean) {
    onids(checked ? [...new Set([...(ids || []), id])] : (ids || []).filter(v => v !== id));
  }
  function lines(value: string) { return [...new Set(value.split('\n').map(v => v.trim()).filter(Boolean))]; }
</script>

<div class="space-y-3 mt-3">
  {#if endpoint || ids !== undefined}
    <label>Applies to
      <select value={ids === undefined ? 'all' : 'selected'} onchange={e => onids(e.currentTarget.value === 'all' ? undefined : [])}>
        <option value="all">All resources in this workspace</option>
        <option value="selected">Selected resources only</option>
      </select>
    </label>
    {#if ids !== undefined}
      <p class="settings-note">Only selected existing resources are covered. Creating new resources may require a workspace-wide permission.</p>
      {#if endpoint}
        <label>Find resources<input type="search" bind:value={query} placeholder="Filter loaded resources by name" /></label>
        <div class="flex flex-wrap justify-between gap-2 settings-note"><span>{ids.length} selected · {resources.length} loaded</span><button type="button" class="settings-button" disabled={loading} onclick={() => load(true)}>Refresh resources</button></div>
        {#if error}<p role="alert" class="settings-error">{error}</p>{/if}
        <div class="max-h-56 overflow-y-auto space-y-1" aria-label={`Resources for ${capability}`} aria-busy={loading}>
          {#each visible as resource (resource.id)}
            <label class="min-h-11 sm:min-h-0 py-1.5"><input type="checkbox" checked={ids.includes(resource.id)} disabled={!ids.includes(resource.id) && ids.length >= 100} onchange={e => toggle(resource.id, e.currentTarget.checked)} /><span class="min-w-0"><span class="block break-words">{resource.name || resource.title || resource.key || resource.id}</span><span class="block settings-note break-all">{resource.id}</span></span></label>
          {/each}
          {#if loaded && !visible.length}<p class="settings-note">{query ? 'No loaded resources match your search.' : 'No resources are visible in this workspace.'}</p>{/if}
        </div>
        {#if loading}<p role="status" class="settings-note">Loading resources…</p>{/if}
        {#if more}<button type="button" class="settings-button" disabled={loading} onclick={() => load()}>Load more resources</button>{/if}
      {/if}
      {#if missing.length}
        <div class="space-y-2"><p class="settings-note">Saved selections not in the loaded list. They stay selected until you remove them.</p>
          {#each missing as id (id)}<label class="min-h-11 sm:min-h-0"><input type="checkbox" checked onchange={() => toggle(id, false)} /><span class="break-all">{id}</span></label>{/each}
        </div>
      {/if}
      {#if !ids.length}<p role="status" class="settings-error">Select at least one resource, or choose all resources.</p>{/if}
    {/if}
  {:else}
    <p class="settings-note">Applies throughout this workspace.{capability.startsWith('files.') ? ' Use advanced path restrictions to limit file access.' : ' Resource selection is not available for this permission.'}</p>
  {/if}
  {#if capability.startsWith('files.') || patterns?.length}
    <details open={!!patterns?.length}>
      <summary class="cursor-pointer text-xs font-medium">Advanced: restrict file paths{patterns?.length ? ` · ${patterns.length} patterns` : ''}</summary>
      <div class="space-y-2 mt-3">
        <label>Allowed path patterns<textarea rows="3" value={patterns?.join('\n') || ''} placeholder="media/*.png" spellcheck="false" oninput={e => { const values = lines(e.currentTarget.value); onpatterns(values.length ? values : undefined); }}></textarea></label>
        <p class="settings-note">One pattern per line, relative to the resource root. For example, media/*.png allows PNG files directly inside media. * does not cross folders. Leave blank for all paths.</p>
        {#if !capability.startsWith('files.')}<p class="settings-note">This bundle already has path restrictions on this capability. They are preserved; resources without a matching path will not be granted access.</p>{/if}
        {#if ids !== undefined}<p class="settings-note">A resource must match both the selected IDs and a path pattern.</p>{/if}
      </div>
    </details>
  {/if}
</div>
