<script lang="ts">
  import { onMount } from 'svelte';
  import { Plus, Save, Trash2, ShieldCheck, Boxes, WalletCards, X } from 'lucide-svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listProviders, type ProviderRecord } from '@/lib/api/providers';
  import { listWorkspaces, type Workspace } from '@/lib/api/workspaces';
  import {
    createVirtualProvider,
    deleteProviderBudget,
    deleteProviderBudgetOverride,
    deleteVirtualProvider,
    deleteVirtualProviderGrant,
    getProviderBudget,
    listProviderBudgetOverrides,
    listVirtualProviderGrants,
    listVirtualProviders,
    saveProviderBudget,
    saveProviderBudgetOverride,
    saveVirtualProviderGrant,
    updateVirtualProvider,
    type BudgetOverrideMode,
    type BudgetResourceKind,
    type ProviderBudgetOverride,
    type ProviderBudgetPolicy,
    type VirtualProvider,
    type VirtualProviderGrant,
    type VirtualProviderModel,
  } from '@/lib/api/provider-governance';

  storeNavbar.title = 'Provider governance';

  interface BudgetResource { id: string; kind: BudgetResourceKind; key: string; label: string; }
  let tab = $state<'budgets' | 'virtual'>('budgets');
  let providers = $state<ProviderRecord[]>([]);
  let virtualProviders = $state<VirtualProvider[]>([]);
  let workspaces = $state<Workspace[]>([]);
  let budgets = $state<Record<string, ProviderBudgetPolicy | null>>({});
  let loading = $state(true);
  let busy = $state(false);
  let error = $state('');

  let editingBudget = $state<BudgetResource | null>(null);
  let budgetForm = $state<ProviderBudgetPolicy>(blankBudget('provider', ''));
  let overrides = $state<ProviderBudgetOverride[]>([]);
  let overrideUser = $state('');
  let overrideMode = $state<BudgetOverrideMode>('custom');
  let overrideUsd = $state(200);

  let editingVirtual = $state<VirtualProvider | null>(null);
  let virtualForm = $state<VirtualProvider>(blankVirtual());
  let grants = $state<VirtualProviderGrant[]>([]);
  let grantWorkspace = $state('');
  let grantPatterns = $state('*');
  let grantMaxUsd = $state(0);
  let grantAllowOverrides = $state(false);

  let resources = $derived<BudgetResource[]>([
    ...providers.map(p => ({ id: p.id, kind: 'provider' as const, key: p.key, label: p.key })),
    ...virtualProviders.filter(v => !!v.id).map(v => ({ id: v.id!, kind: 'virtual_provider' as const, key: v.key, label: v.name })),
  ]);

  function blankBudget(kind: BudgetResourceKind, id: string): ProviderBudgetPolicy {
    return { resource_kind: kind, resource_id: id, total_limit_cents: 0, default_user_limit_cents: 0, budget_period: 'monthly', budget_reset_day: 1, budget_reset_time: '00:00', budget_timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC', enforce_unpriced: true };
  }
  function blankVirtual(): VirtualProvider {
    return { key: '', name: '', description: '', default_model: '', disabled: false, models: [{ alias: '', provider_ref: providers[0]?.key || '', model: providers[0]?.config.model || '' }] };
  }
  function budgetKey(resource: BudgetResource) { return `${resource.kind}:${resource.id}`; }
  function usd(cents = 0) { return new Intl.NumberFormat(undefined, { style: 'currency', currency: 'USD' }).format(cents / 100); }
  function percent(policy: ProviderBudgetPolicy | null) {
    if (!policy?.total_limit_cents) return 0;
    return Math.min(100, ((policy.spent_cents || 0) + (policy.reserved_cents || 0)) / policy.total_limit_cents * 100);
  }
  function modelsFor(providerRef: string) {
    const p = providers.find(item => item.key === providerRef);
    const models = [...(p?.config.models || [])];
    if (p?.config.model && !models.includes(p.config.model)) models.unshift(p.config.model);
    return models;
  }

  async function load() {
    loading = true; error = '';
    try {
      const [providerResult, virtualResult, workspaceRows] = await Promise.all([listProviders({ _limit: 500 }), listVirtualProviders(), listWorkspaces()]);
      providers = providerResult.data || [];
      virtualProviders = virtualResult.data || [];
      workspaces = workspaceRows || [];
      const next: Record<string, ProviderBudgetPolicy | null> = {};
      await Promise.all([
        ...providers.map(async p => { next[`provider:${p.id}`] = await getProviderBudget('provider', p.id); }),
        ...virtualProviders.filter(v => !!v.id).map(async v => { next[`virtual_provider:${v.id}`] = await getProviderBudget('virtual_provider', v.id!); }),
      ]);
      budgets = next;
    } catch (e: any) {
      error = e?.response?.data?.message || 'Provider governance could not be loaded.';
    } finally { loading = false; }
  }
  onMount(load);

  async function editBudget(resource: BudgetResource) {
    editingBudget = resource;
    const current = budgets[budgetKey(resource)];
    budgetForm = current ? structuredClone(current) : blankBudget(resource.kind, resource.id);
    overrides = current?.id ? await listProviderBudgetOverrides(current.id) : [];
  }
  async function saveBudgetForm() {
    if (!editingBudget) return;
    busy = true;
    try {
      const payload = { ...budgetForm, total_limit_cents: Math.round(budgetForm.total_limit_cents), default_user_limit_cents: Math.round(budgetForm.default_user_limit_cents), budget_reset_day: budgetForm.budget_period === 'daily' ? 0 : budgetForm.budget_reset_day };
      const saved = await saveProviderBudget(editingBudget.kind, editingBudget.id, payload);
      budgets = { ...budgets, [budgetKey(editingBudget)]: saved };
      budgetForm = structuredClone(saved);
      overrides = saved.id ? await listProviderBudgetOverrides(saved.id) : [];
      addToast('Provider budget saved', 'info');
    } catch (e: any) { addToast(e?.response?.data?.message || 'Budget could not be saved', 'alert'); }
    finally { busy = false; }
  }
  async function removeBudget() {
    if (!editingBudget || !budgetForm.id || !confirm('Remove this budget and every user override?')) return;
    busy = true;
    try { await deleteProviderBudget(editingBudget.kind, editingBudget.id); budgets = { ...budgets, [budgetKey(editingBudget)]: null }; editingBudget = null; addToast('Provider budget removed', 'info'); }
    catch (e: any) { addToast(e?.response?.data?.message || 'Budget could not be removed', 'alert'); }
    finally { busy = false; }
  }
  async function addOverride() {
    if (!budgetForm.id || !overrideUser.trim()) return;
    busy = true;
    try {
      await saveProviderBudgetOverride(budgetForm.id, { user_id: overrideUser.trim(), mode: overrideMode, limit_cents: overrideMode === 'custom' ? Math.round(overrideUsd * 100) : 0 });
      overrides = await listProviderBudgetOverrides(budgetForm.id); overrideUser = ''; addToast('User allowance saved', 'info');
    } catch (e: any) { addToast(e?.response?.data?.message || 'User allowance could not be saved', 'alert'); }
    finally { busy = false; }
  }
  async function removeOverride(userId: string) {
    if (!budgetForm.id) return;
    await deleteProviderBudgetOverride(budgetForm.id, userId); overrides = overrides.filter(item => item.user_id !== userId);
  }

  function startVirtual(record?: VirtualProvider) {
    editingVirtual = record || null;
    virtualForm = record ? structuredClone(record) : blankVirtual();
    grants = [];
    if (record?.id) void listVirtualProviderGrants(record.id).then(rows => grants = rows);
  }
  function addModel() {
    const p = providers[0];
    virtualForm.models = [...virtualForm.models, { alias: '', provider_ref: p?.key || '', model: p?.config.model || '' }];
  }
  function updateModel(index: number, patch: Partial<VirtualProviderModel>) {
    virtualForm.models = virtualForm.models.map((model, i) => i === index ? { ...model, ...patch } : model);
    if (!virtualForm.default_model && virtualForm.models[0]?.alias) virtualForm.default_model = virtualForm.models[0].alias;
  }
  function removeModel(index: number) {
    const removed = virtualForm.models[index];
    virtualForm.models = virtualForm.models.filter((_, i) => i !== index);
    if (virtualForm.default_model === removed.alias) virtualForm.default_model = virtualForm.models[0]?.alias || '';
  }
  async function saveVirtual() {
    busy = true;
    try {
      virtualForm.default_model ||= virtualForm.models[0]?.alias || '';
      const saved = editingVirtual?.id ? await updateVirtualProvider(editingVirtual.id, virtualForm) : await createVirtualProvider(virtualForm);
      editingVirtual = saved; virtualForm = structuredClone(saved); await load(); tab = 'virtual'; addToast('Virtual provider saved', 'info');
    } catch (e: any) { addToast(e?.response?.data?.message || 'Virtual provider could not be saved', 'alert'); }
    finally { busy = false; }
  }
  async function removeVirtual(record: VirtualProvider) {
    if (!record.id || !confirm(`Delete virtual provider “${record.name}”?`)) return;
    await deleteVirtualProvider(record.id); if (editingVirtual?.id === record.id) startVirtual(); await load(); tab = 'virtual';
  }
  async function addGrant() {
    if (!editingVirtual?.id || !grantWorkspace) return;
    busy = true;
    try {
      await saveVirtualProviderGrant(editingVirtual.id, { virtual_provider_id: editingVirtual.id, workspace_id: grantWorkspace, model_patterns: grantPatterns.split(',').map(v => v.trim()).filter(Boolean), allow_user_overrides: grantAllowOverrides, max_user_limit_cents: Math.round(grantMaxUsd * 100) });
      grants = await listVirtualProviderGrants(editingVirtual.id); addToast('Workspace access saved', 'info');
    } catch (e: any) { addToast(e?.response?.data?.message || 'Workspace access could not be saved', 'alert'); }
    finally { busy = false; }
  }
  async function removeGrant(workspaceId: string) {
    if (!editingVirtual?.id) return;
    await deleteVirtualProviderGrant(editingVirtual.id, workspaceId); grants = grants.filter(g => g.workspace_id !== workspaceId);
  }
</script>

<svelte:head><title>AT | Provider governance</title></svelte:head>

<div class="space-y-4">
  <header class="flex flex-wrap items-start justify-between gap-3">
    <div><h1 class="text-lg font-semibold text-gray-900 dark:text-dark-text">Provider governance</h1><p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-dark-text-muted">Control provider spend, assign account allowances, and publish deterministic model collections to workspaces.</p></div>
    <div class="inline-flex border border-gray-300 dark:border-dark-border">
      <button class={['px-3 py-2 text-xs font-medium', tab === 'budgets' ? 'bg-gray-900 text-white dark:bg-accent' : 'bg-white text-gray-600 dark:bg-dark-surface dark:text-dark-text-secondary']} onclick={() => tab = 'budgets'}><WalletCards size={14} class="mr-1 inline" />Budgets</button>
      <button class={['border-l border-gray-300 px-3 py-2 text-xs font-medium dark:border-dark-border', tab === 'virtual' ? 'bg-gray-900 text-white dark:bg-accent' : 'bg-white text-gray-600 dark:bg-dark-surface dark:text-dark-text-secondary']} onclick={() => tab = 'virtual'}><Boxes size={14} class="mr-1 inline" />Virtual providers</button>
    </div>
  </header>

  {#if error}<div role="alert" class="border border-red-300 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300">{error} <button class="underline" onclick={load}>Retry</button></div>{/if}
  {#if loading}<div class="border border-gray-200 bg-white p-8 text-center text-sm text-gray-500 dark:border-dark-border dark:bg-dark-surface dark:text-dark-text-muted">Loading provider governance…</div>
  {:else if tab === 'budgets'}
    <div class="grid gap-4 xl:grid-cols-[minmax(0,1.25fr)_minmax(24rem,.75fr)]">
      <section class="border border-gray-200 bg-white dark:border-dark-border dark:bg-dark-surface">
        <div class="border-b border-gray-200 bg-gray-50 px-4 py-3 dark:border-dark-border dark:bg-dark-base"><h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Provider allowances</h2><p class="mt-1 text-xs text-gray-500 dark:text-dark-text-muted">Spend is shared across Chats, Sessions, agents and personal API tokens owned by the same account.</p></div>
        {#if resources.length === 0}<p class="p-6 text-sm text-gray-500 dark:text-dark-text-muted">Create a provider before defining an allowance.</p>{:else}
          <div class="divide-y divide-gray-200 dark:divide-dark-border">
            {#each resources as resource}
              {@const policy = budgets[budgetKey(resource)]}
              <button class="grid w-full gap-3 px-4 py-3 text-left hover:bg-gray-50 focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-accent dark:hover:bg-dark-base sm:grid-cols-[minmax(0,1fr)_9rem_9rem]" onclick={() => editBudget(resource)}>
                <div class="min-w-0"><div class="flex items-center gap-2"><strong class="truncate text-sm text-gray-900 dark:text-dark-text">{resource.label}</strong><span class="border border-gray-200 px-1.5 py-0.5 text-[10px] uppercase text-gray-500 dark:border-dark-border dark:text-dark-text-muted">{resource.kind === 'provider' ? 'Physical' : 'Virtual'}</span></div><p class="mt-1 truncate font-mono text-[11px] text-gray-400">{resource.key}</p>{#if policy?.total_limit_cents}<div class="mt-2 h-1.5 bg-gray-100 dark:bg-dark-base"><div class="h-full bg-accent" style={`width:${percent(policy)}%`}></div></div>{/if}</div>
                <div><span class="block text-[10px] uppercase tracking-wide text-gray-400">Provider</span><span class="font-mono text-xs text-gray-700 dark:text-dark-text-secondary">{policy?.total_limit_cents ? `${usd(policy.spent_cents)} / ${usd(policy.total_limit_cents)}` : 'Unlimited'}</span></div>
                <div><span class="block text-[10px] uppercase tracking-wide text-gray-400">Per account</span><span class="font-mono text-xs text-gray-700 dark:text-dark-text-secondary">{policy?.default_user_limit_cents ? usd(policy.default_user_limit_cents) : 'Unlimited'}</span></div>
              </button>
            {/each}
          </div>
        {/if}
      </section>

      <aside class="border border-gray-200 bg-white dark:border-dark-border dark:bg-dark-surface">
        {#if !editingBudget}<div class="p-8 text-center"><ShieldCheck size={28} class="mx-auto text-gray-300 dark:text-dark-text-muted" /><p class="mt-3 text-sm text-gray-500 dark:text-dark-text-muted">Select a provider to configure its spending policy.</p></div>{:else}
          <div class="flex items-center justify-between border-b border-gray-200 bg-gray-50 px-4 py-3 dark:border-dark-border dark:bg-dark-base"><div><h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">{editingBudget.label}</h2><p class="text-xs text-gray-500 dark:text-dark-text-muted">Zero means unlimited.</p></div><button aria-label="Close budget editor" class="p-1 text-gray-400 hover:text-gray-700 dark:hover:text-dark-text" onclick={() => editingBudget = null}><X size={17} /></button></div>
          <div class="space-y-5 p-4">
            <div class="grid grid-cols-2 gap-3"><label class="text-xs text-gray-600 dark:text-dark-text-secondary">Provider limit (USD)<input type="number" min="0" step="0.01" value={budgetForm.total_limit_cents / 100} oninput={e => budgetForm.total_limit_cents = Number(e.currentTarget.value) * 100} /></label><label class="text-xs text-gray-600 dark:text-dark-text-secondary">Default per account (USD)<input type="number" min="0" step="0.01" value={budgetForm.default_user_limit_cents / 100} oninput={e => budgetForm.default_user_limit_cents = Number(e.currentTarget.value) * 100} /></label></div>
            <div class="grid grid-cols-2 gap-3"><label class="text-xs text-gray-600 dark:text-dark-text-secondary">Reset period<select bind:value={budgetForm.budget_period}><option value="daily">Daily</option><option value="weekly">Weekly</option><option value="monthly">Monthly</option></select></label><label class="text-xs text-gray-600 dark:text-dark-text-secondary">Timezone<input bind:value={budgetForm.budget_timezone} /></label></div>
            <label class="flex items-start gap-2 text-xs text-gray-600 dark:text-dark-text-secondary"><input class="mt-0.5" type="checkbox" bind:checked={budgetForm.enforce_unpriced} /><span><strong class="block text-gray-800 dark:text-dark-text">Require model pricing</strong>Block unpriced calls instead of letting them bypass the budget.</span></label>
            <div class="flex gap-2"><button class="settings-primary inline-flex items-center gap-2" disabled={busy} onclick={saveBudgetForm}><Save size={14} />Save budget</button>{#if budgetForm.id}<button class="settings-button text-red-600 dark:text-red-400" disabled={busy} onclick={removeBudget}>Remove</button>{/if}</div>
            {#if budgetForm.id}
              <div class="border-t border-gray-200 pt-4 dark:border-dark-border"><h3 class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-dark-text-muted">Account overrides</h3><div class="mt-3 grid gap-2 sm:grid-cols-[minmax(0,1fr)_7rem_6rem_auto]"><input aria-label="User ID" placeholder="Account user ID" bind:value={overrideUser} /><select aria-label="Override mode" bind:value={overrideMode}><option value="custom">Custom</option><option value="unlimited">Unlimited</option><option value="blocked">Blocked</option></select><input aria-label="Custom limit in USD" type="number" min="0.01" step="0.01" bind:value={overrideUsd} disabled={overrideMode !== 'custom'} /><button class="settings-button" disabled={busy || !overrideUser.trim()} onclick={addOverride}>Add</button></div>
                <div class="mt-3 divide-y divide-gray-100 dark:divide-dark-border">{#each overrides as item}<div class="flex items-center justify-between gap-3 py-2"><div class="min-w-0"><p class="truncate font-mono text-xs text-gray-700 dark:text-dark-text-secondary">{item.user_id}</p><p class="text-[11px] text-gray-400">{item.mode === 'custom' ? usd(item.limit_cents) : item.mode}</p></div><button aria-label={`Remove override for ${item.user_id}`} class="p-1 text-gray-400 hover:text-red-600" onclick={() => removeOverride(item.user_id)}><Trash2 size={14} /></button></div>{/each}{#if overrides.length === 0}<p class="py-3 text-xs text-gray-400">Everyone inherits the default allowance.</p>{/if}</div>
              </div>
            {/if}
          </div>
        {/if}
      </aside>
    </div>
  {:else}
    <div class="grid gap-4 xl:grid-cols-[20rem_minmax(0,1fr)]">
      <section class="border border-gray-200 bg-white dark:border-dark-border dark:bg-dark-surface"><div class="flex items-center justify-between border-b border-gray-200 bg-gray-50 px-4 py-3 dark:border-dark-border dark:bg-dark-base"><div><h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Virtual providers</h2><p class="text-xs text-gray-500 dark:text-dark-text-muted">Model collections with direct mappings.</p></div><button class="settings-button p-1.5" aria-label="New virtual provider" onclick={() => startVirtual()}><Plus size={15} /></button></div>
        <div class="divide-y divide-gray-200 dark:divide-dark-border">{#each virtualProviders as record}<button class={['w-full px-4 py-3 text-left hover:bg-gray-50 dark:hover:bg-dark-base', editingVirtual?.id === record.id ? 'bg-gray-50 dark:bg-dark-base' : '']} onclick={() => startVirtual(record)}><strong class="block truncate text-sm text-gray-900 dark:text-dark-text">{record.name}</strong><span class="font-mono text-[11px] text-gray-400">{record.key} · {record.models.length} models</span></button>{/each}{#if virtualProviders.length === 0}<p class="p-5 text-sm text-gray-500 dark:text-dark-text-muted">No virtual providers yet.</p>{/if}</div>
      </section>
      <section class="border border-gray-200 bg-white dark:border-dark-border dark:bg-dark-surface"><div class="flex items-center justify-between border-b border-gray-200 bg-gray-50 px-4 py-3 dark:border-dark-border dark:bg-dark-base"><div><h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">{editingVirtual?.id ? `Edit ${editingVirtual.name}` : 'Build a virtual provider'}</h2><p class="text-xs text-gray-500 dark:text-dark-text-muted">Every public alias maps to one exact physical model.</p></div>{#if editingVirtual?.id}<button aria-label="Delete virtual provider" class="p-1 text-gray-400 hover:text-red-600" onclick={() => removeVirtual(editingVirtual!)}><Trash2 size={16} /></button>{/if}</div>
        <div class="space-y-5 p-4"><div class="grid gap-3 md:grid-cols-2"><label class="text-xs text-gray-600 dark:text-dark-text-secondary">Display name<input bind:value={virtualForm.name} placeholder="Company AI" /></label><label class="text-xs text-gray-600 dark:text-dark-text-secondary">Provider key<input class="font-mono" bind:value={virtualForm.key} placeholder="company-ai" /></label></div><label class="text-xs text-gray-600 dark:text-dark-text-secondary">Description<textarea rows="2" bind:value={virtualForm.description}></textarea></label>
          <div><div class="flex items-center justify-between"><div><h3 class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-dark-text-muted">Model catalogue</h3><p class="mt-1 text-xs text-gray-400">Aliases are the model IDs consumers see.</p></div><button class="settings-button inline-flex items-center gap-1" onclick={addModel}><Plus size={13} />Model</button></div>
            <div class="mt-3 space-y-2">{#each virtualForm.models as model, index}<div class="grid items-end gap-2 border border-gray-200 bg-gray-50 p-3 dark:border-dark-border dark:bg-dark-base md:grid-cols-[minmax(8rem,.7fr)_minmax(9rem,1fr)_minmax(12rem,1.3fr)_auto]"><label class="text-[11px] text-gray-500">Public alias<input class="font-mono" value={model.alias} oninput={e => updateModel(index, { alias: e.currentTarget.value })} /></label><label class="text-[11px] text-gray-500">Physical provider<select value={model.provider_ref} onchange={e => { const ref = e.currentTarget.value; updateModel(index, { provider_ref: ref, model: modelsFor(ref)[0] || '' }); }}>{#each providers as p}<option value={p.key}>{p.key}</option>{/each}</select></label><label class="text-[11px] text-gray-500">Physical model<select value={model.model} onchange={e => updateModel(index, { model: e.currentTarget.value })}>{#each modelsFor(model.provider_ref) as name}<option value={name}>{name}</option>{/each}</select></label><button aria-label={`Remove model ${model.alias || index + 1}`} class="settings-button p-2 text-red-600" disabled={virtualForm.models.length === 1} onclick={() => removeModel(index)}><Trash2 size={14} /></button></div>{/each}</div>
          </div>
          <label class="max-w-sm text-xs text-gray-600 dark:text-dark-text-secondary">Default public model<select bind:value={virtualForm.default_model}>{#each virtualForm.models.filter(m => m.alias) as model}<option value={model.alias}>{model.alias}</option>{/each}</select></label>
          <button class="settings-primary inline-flex items-center gap-2" disabled={busy || !virtualForm.name || !virtualForm.key || virtualForm.models.some(m => !m.alias || !m.provider_ref || !m.model)} onclick={saveVirtual}><Save size={14} />{editingVirtual?.id ? 'Save virtual provider' : 'Create virtual provider'}</button>
          {#if editingVirtual?.id}<div class="border-t border-gray-200 pt-4 dark:border-dark-border"><h3 class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-dark-text-muted">Workspace access</h3><p class="mt-1 text-xs text-gray-400">Recipients see only the virtual aliases; physical credentials remain private.</p><div class="mt-3 grid items-end gap-2 md:grid-cols-[minmax(10rem,1fr)_minmax(8rem,.8fr)_7rem_auto]"><label class="text-[11px] text-gray-500">Workspace<select bind:value={grantWorkspace}><option value="">Select…</option>{#each workspaces as workspace}<option value={workspace.id}>{workspace.name}</option>{/each}</select></label><label class="text-[11px] text-gray-500">Model patterns<input class="font-mono" bind:value={grantPatterns} placeholder="*" /></label><label class="text-[11px] text-gray-500">Max user USD<input type="number" min="0" step="0.01" bind:value={grantMaxUsd} /></label><button class="settings-button" disabled={!grantWorkspace || busy} onclick={addGrant}>Share</button></div><label class="mt-2 flex items-center gap-2 text-xs text-gray-500"><input type="checkbox" bind:checked={grantAllowOverrides} />Workspace admins may customize account allowances</label>
            <div class="mt-3 divide-y divide-gray-100 dark:divide-dark-border">{#each grants as grant}<div class="flex items-center justify-between gap-3 py-2"><div><p class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">{workspaces.find(w => w.id === grant.workspace_id)?.name || grant.workspace_id}</p><p class="font-mono text-[11px] text-gray-400">{grant.model_patterns.join(', ')} · {grant.max_user_limit_cents ? `max ${usd(grant.max_user_limit_cents)}` : 'no override ceiling'}</p></div><button aria-label="Remove workspace access" class="p-1 text-gray-400 hover:text-red-600" onclick={() => removeGrant(grant.workspace_id)}><Trash2 size={14} /></button></div>{/each}{#if grants.length === 0}<p class="py-3 text-xs text-gray-400">This provider is available only in its owner workspace.</p>{/if}</div></div>{/if}
        </div>
      </section>
    </div>
  {/if}
</div>
