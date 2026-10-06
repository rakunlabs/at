<script lang="ts">
  import { onMount } from 'svelte';
  import { Plus, Pencil, Trash2, History, Loader2 } from 'lucide-svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { workspaceState } from '@/lib/store/workspace.svelte';
  import { isNativeAdmin } from '@/lib/store/auth.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { workspaceAPI, type Member } from '@/lib/api/workspaces';
  import { listTokens, type APIToken } from '@/lib/api/tokens';
  import { getInfo, type InfoProvider } from '@/lib/api/gateway';
  import {
    listTracePrivacyRules, createTracePrivacyRule, updateTracePrivacyRule, deleteTracePrivacyRule,
    applyTracePrivacyRule, getTracePrivacySettings, saveTracePrivacySettings,
    type TracePrivacyRule, type TracePrivacySettings, type TracePrivacyApplyResult,
  } from '@/lib/api/trace-privacy';

  storeNavbar.title = 'Trace privacy';

  const sources = ['gateway', 'gateway_stream', 'gateway_passthrough', 'responses', 'embeddings', 'decisions', 'chat', 'agent', 'workflow', 'developer'];

  let rules = $state<TracePrivacyRule[]>([]);
  let settings = $state<TracePrivacySettings>({ allow_user_opt_out: false });
  let members = $state<Member[]>([]);
  let tokens = $state<APIToken[]>([]);
  let providers = $state<InfoProvider[]>([]);
  let loading = $state(true);
  let error = $state('');
  let saving = $state(false);

  let editing = $state<Partial<TracePrivacyRule> | null>(null);
  let applying = $state<{ rule: TracePrivacyRule; preview: TracePrivacyApplyResult | null; busy: boolean } | null>(null);

  let admin = $derived(isNativeAdmin());
  let workspaceName = $derived(workspaceState.items.find(w => w.id === workspaceState.access?.workspace_id)?.name || 'this workspace');
  let installationRules = $derived(rules.filter(r => r.scope === 'installation'));
  let workspaceRules = $derived(rules.filter(r => r.scope === 'workspace'));
  let models = $derived([...new Set(providers.flatMap(p => p.models || []))].sort());
  let selectedProviderModels = $derived(editing?.provider ? (providers.find(p => p.key === editing?.provider)?.models || []) : models);

  function message(e: any, fallback: string) { return e?.response?.data?.message || fallback; }
  function tokenLabel(id: string) { const t = tokens.find(t => t.id === id); return t ? `${t.name} (${t.token_prefix}…)` : id; }

  async function load() {
    loading = true;
    error = '';
    try {
      [rules, settings] = await Promise.all([listTracePrivacyRules(), getTracePrivacySettings()]);
    } catch (e) {
      error = message(e, 'Could not load trace privacy rules.');
    } finally {
      loading = false;
    }
    // Pickers are conveniences; a failure leaves free-text entry working.
    const ws = workspaceState.access?.workspace_id;
    const [m, t, i] = await Promise.allSettled([
      ws ? workspaceAPI.get(`workspaces/${encodeURIComponent(ws)}/members`) : Promise.reject(),
      listTokens({ _limit: 500 }),
      getInfo(),
    ]);
    if (m.status === 'fulfilled') members = (m.value.data.items || []).filter((x: Member) => x.status === 'active');
    if (t.status === 'fulfilled') tokens = t.value.data || [];
    if (i.status === 'fulfilled') providers = i.value.providers || [];
  }

  function describe(r: TracePrivacyRule) {
    const parts: string[] = [];
    if (r.user_id) parts.push(`user ${r.user_id}`);
    if (r.token_id) parts.push(`token ${tokenLabel(r.token_id)}`);
    if (r.provider) parts.push(`provider ${r.provider}`);
    if (r.model) parts.push(`model ${r.model}`);
    if (r.source) parts.push(`source ${r.source}`);
    return parts.length ? parts.join(' · ') : 'Every observation';
  }

  function startNew(scope: 'workspace' | 'installation') {
    editing = { scope, action: 'skip', enabled: true, description: '', user_id: '', token_id: '', provider: '', model: '', source: '' };
  }

  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (!editing || saving) return;
    saving = true;
    try {
      if (editing.id) await updateTracePrivacyRule(editing.id, editing);
      else await createTracePrivacyRule(editing);
      editing = null;
      rules = await listTracePrivacyRules();
      addToast('Rule saved. It applies to new observations within seconds.', 'info');
    } catch (e) {
      addToast(message(e, 'Could not save the rule.'), 'alert');
    } finally {
      saving = false;
    }
  }

  async function toggle(r: TracePrivacyRule) {
    try {
      await updateTracePrivacyRule(r.id, { ...r, enabled: !r.enabled });
      rules = await listTracePrivacyRules();
    } catch (e) {
      addToast(message(e, 'Could not update the rule.'), 'alert');
    }
  }

  async function remove(r: TracePrivacyRule) {
    if (!confirm('Delete this rule? Observations it suppressed stay suppressed; new ones will be recorded again.')) return;
    try {
      await deleteTracePrivacyRule(r.id);
      rules = rules.filter(x => x.id !== r.id);
    } catch (e) {
      addToast(message(e, 'Could not delete the rule.'), 'alert');
    }
  }

  async function openApply(r: TracePrivacyRule) {
    applying = { rule: r, preview: null, busy: true };
    try {
      applying.preview = await applyTracePrivacyRule(r.id, true);
    } catch (e) {
      addToast(message(e, 'Could not preview the change.'), 'alert');
      applying = null;
      return;
    }
    applying.busy = false;
  }

  async function confirmApply() {
    if (!applying || applying.busy) return;
    applying.busy = true;
    try {
      const res = await applyTracePrivacyRule(applying.rule.id, false);
      addToast(res.action === 'skip' ? `Deleted ${res.traces} traces (${res.observations} observations).` : `Removed content from ${res.traces} traces (${res.observations} observations).`, 'info');
      applying = null;
    } catch (e) {
      addToast(message(e, 'Could not apply the rule.'), 'alert');
      if (applying) applying.busy = false;
    }
  }

  async function saveSettings() {
    try {
      settings = await saveTracePrivacySettings(settings);
      addToast('Saved.', 'info');
    } catch (e) {
      addToast(message(e, 'Could not save settings.'), 'alert');
      settings = await getTracePrivacySettings().catch(() => settings);
    }
  }

  onMount(() => { void load(); });
</script>

<svelte:head><title>AT | Trace privacy</title></svelte:head>
<div class="settings-page">
  <header>
    <h1 class="settings-title">Trace privacy</h1>
    <p class="settings-subtitle">Choose which users, API keys, providers or models are left out of traces. Usage, cost and budgets are always recorded.</p>
  </header>

  {#if error}
    <div role="alert" class="settings-error"><p>{error}</p><button type="button" class="settings-button mt-2" onclick={load}>Retry</button></div>
  {/if}
  {#if loading}<p class="settings-note" role="status">Loading trace privacy rules…</p>{/if}

  {#snippet ruleTable(list: TracePrivacyRule[], scope: 'workspace' | 'installation', title: string, note: string)}
    <section class="border border-dark-border bg-dark-surface">
      <div class="px-4 py-3 bg-dark-base border-b border-dark-border flex flex-wrap items-center justify-between gap-2">
        <div><h2 class="settings-section-title">{title}</h2><p class="settings-note">{note}</p></div>
        <button type="button" class="settings-button inline-flex items-center gap-1 min-h-11 sm:min-h-0" onclick={() => startNew(scope)}><Plus size={14} /> Add rule</button>
      </div>
      {#if list.length === 0}
        <p class="p-4 settings-note">No rules. Everything is traced.</p>
      {:else}
        <ul class="divide-y divide-dark-border">
          {#each list as r (r.id)}
            <li class="px-4 py-3 flex flex-wrap items-center gap-3 text-sm">
              <span class={["px-1.5 py-0.5 text-xs font-medium border", r.action === 'skip' ? 'border-red-800 text-red-400' : 'border-amber-800 text-amber-400']}>{r.action === 'skip' ? 'Do not record' : 'Hide content'}</span>
              <div class="min-w-0 flex-1">
                <p class={["break-all", r.enabled ? '' : 'opacity-50']}>{describe(r)}</p>
                {#if r.description}<p class="settings-note">{r.description}</p>{/if}
              </div>
              <label class="flex items-center gap-1 text-xs"><input type="checkbox" checked={r.enabled} onchange={() => toggle(r)} /> Enabled</label>
              <button type="button" class="settings-button min-h-11 sm:min-h-0" title="Apply to existing traces" onclick={() => openApply(r)}><History size={14} /></button>
              <button type="button" class="settings-button min-h-11 sm:min-h-0" title="Edit" onclick={() => editing = { ...r }}><Pencil size={14} /></button>
              <button type="button" class="settings-button min-h-11 sm:min-h-0" title="Delete" onclick={() => remove(r)}><Trash2 size={14} /></button>
            </li>
          {/each}
        </ul>
      {/if}
    </section>
  {/snippet}

  {#if !loading && !error}
    <div class="space-y-4">
      {@render ruleTable(workspaceRules, 'workspace', 'Workspace rules', `Apply to observations recorded in ${workspaceName}.`)}
      {#if admin}
        {@render ruleTable(installationRules, 'installation', 'Installation rules', 'Apply in every workspace. Only installation administrators see and manage these.')}
        <section class="border border-dark-border bg-dark-surface">
          <div class="px-4 py-3 bg-dark-base border-b border-dark-border"><h2 class="settings-section-title">Personal opt-out</h2></div>
          <div class="p-4 space-y-2">
            <label class="flex items-start gap-2 text-sm">
              <input type="checkbox" class="mt-0.5" bind:checked={settings.allow_user_opt_out} onchange={saveSettings} />
              <span>Let accounts turn off tracing for themselves<span class="settings-note block mt-1">Adds a switch under Account security. It covers the account's browser use, Sessions and personal API tokens. Leave off if you need traces for auditing.</span></span>
            </label>
          </div>
        </section>
      {/if}
      <p class="settings-note">"Do not record" keeps the whole trace out of AT and trace export. "Hide content" keeps timing, tokens and cost but drops prompts, responses and tool content. When one observation matches, the rest of its trace gets the same treatment. Rules affect new traces; use <History size={12} class="inline" /> to apply one to existing traces.</p>
    </div>
  {/if}
</div>

{#if editing}
  <div class="fixed inset-0 z-50 flex items-start justify-center bg-black/40 p-4 overflow-y-auto" role="presentation" onclick={(e) => { if (e.target === e.currentTarget) editing = null; }} onkeydown={(e) => { if (e.key === 'Escape') editing = null; }}>
    <form class="w-full max-w-lg border border-dark-border bg-dark-surface settings-form" onsubmit={save}>
      <div class="px-4 py-3 bg-dark-base border-b border-dark-border">
        <h2 class="settings-section-title">{editing.id ? 'Edit rule' : 'New rule'} · {editing.scope === 'installation' ? 'Installation' : 'Workspace'}</h2>
        <p class="settings-note">Empty fields match everything. Filled fields must all match.</p>
      </div>
      <fieldset class="p-4 space-y-3 border-0" disabled={saving}>
        <label class="settings-label">Action
          <select class="settings-input" bind:value={editing.action}>
            <option value="skip">Do not record the trace</option>
            <option value="redact">Record, but hide content</option>
          </select>
        </label>
        <label class="settings-label">User
          <input class="settings-input" list="tp-users" bind:value={editing.user_id} placeholder="Any user" spellcheck="false" />
          <datalist id="tp-users">{#each members as m}<option value={m.user_id}>{m.role}</option>{/each}</datalist>
        </label>
        <label class="settings-label">API token
          <select class="settings-input" bind:value={editing.token_id}>
            <option value="">Any token</option>
            {#each tokens as t}<option value={t.id}>{t.name} ({t.token_prefix}…)</option>{/each}
            {#if editing.token_id && !tokens.some(t => t.id === editing?.token_id)}<option value={editing.token_id}>{editing.token_id}</option>{/if}
          </select>
        </label>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <label class="settings-label">Provider
            <input class="settings-input" list="tp-providers" bind:value={editing.provider} placeholder="Any provider" spellcheck="false" />
            <datalist id="tp-providers">{#each providers as p}<option value={p.key}>{p.type}</option>{/each}</datalist>
          </label>
          <label class="settings-label">Model
            <input class="settings-input" list="tp-models" bind:value={editing.model} placeholder="Any model, e.g. gpt-5*" spellcheck="false" />
            <datalist id="tp-models">{#each selectedProviderModels as m}<option value={m}></option>{/each}</datalist>
          </label>
        </div>
        <label class="settings-label">Source
          <select class="settings-input" bind:value={editing.source}>
            <option value="">Any source</option>
            {#each sources as s}<option value={s}>{s}</option>{/each}
          </select>
        </label>
        <label class="settings-label">Note<input class="settings-input" bind:value={editing.description} maxlength="512" placeholder="Why this rule exists" /></label>
        <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={editing.enabled} /> Enabled</label>
        <p class="settings-note">Model accepts * and ? wildcards; include a slash (provider/model) to match both.</p>
      </fieldset>
      <div class="px-4 py-3 border-t border-dark-border flex justify-end gap-2">
        <button type="button" class="settings-button" onclick={() => editing = null}>Cancel</button>
        <button type="submit" class="settings-primary" disabled={saving}>{saving ? 'Saving…' : 'Save rule'}</button>
      </div>
    </form>
  </div>
{/if}

{#if applying}
  <div class="fixed inset-0 z-50 flex items-start justify-center bg-black/40 p-4" role="presentation" onclick={(e) => { if (e.target === e.currentTarget && !applying?.busy) applying = null; }} onkeydown={(e) => { if (e.key === 'Escape' && !applying?.busy) applying = null; }}>
    <div class="w-full max-w-md border border-dark-border bg-dark-surface">
      <div class="px-4 py-3 bg-dark-base border-b border-dark-border"><h2 class="settings-section-title">Apply to existing traces</h2></div>
      <div class="p-4 space-y-2 text-sm">
        <p>{describe(applying.rule)}</p>
        {#if !applying.preview}
          <p class="settings-note inline-flex items-center gap-2"><Loader2 size={14} class="animate-spin motion-reduce:animate-none" /> Counting matching traces…</p>
        {:else if applying.preview.traces === 0}
          <p class="settings-note">No stored traces match this rule.</p>
        {:else}
          <p>{applying.preview.action === 'skip' ? 'This permanently deletes' : 'This permanently removes the content of'} <strong>{applying.preview.traces}</strong> traces ({applying.preview.observations} observations){applying.preview.action === 'skip' ? ', including their scores and bookmarks' : ''}.</p>
          <p class="settings-error">This cannot be undone. Usage and cost records are not affected.</p>
        {/if}
      </div>
      <div class="px-4 py-3 border-t border-dark-border flex justify-end gap-2">
        <button type="button" class="settings-button" disabled={applying.busy && !!applying.preview} onclick={() => applying = null}>Cancel</button>
        {#if applying.preview && applying.preview.traces > 0}
          <button type="button" class="settings-primary" disabled={applying.busy} onclick={confirmApply}>{applying.busy ? 'Applying…' : applying.preview.action === 'skip' ? 'Delete traces' : 'Remove content'}</button>
        {/if}
      </div>
    </div>
  </div>
{/if}
