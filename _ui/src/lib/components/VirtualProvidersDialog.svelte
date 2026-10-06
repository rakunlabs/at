<script lang="ts">
  import { onMount } from 'svelte';
  import { Plus, Save, Trash2, X } from 'lucide-svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listProviders, type ProviderRecord } from '@/lib/api/providers';
  import { listWorkspaces, type Workspace } from '@/lib/api/workspaces';
  import {
    createVirtualProvider,
    deleteVirtualProvider,
    deleteVirtualProviderGrant,
    listVirtualProviderGrants,
    listVirtualProviders,
    saveVirtualProviderGrant,
    updateVirtualProvider,
    type VirtualProvider,
    type VirtualProviderGrant,
    type VirtualProviderModel,
  } from '@/lib/api/provider-governance';

  interface Props {
    onclose: () => void;
  }

  let { onclose }: Props = $props();

  let providers = $state<ProviderRecord[]>([]);
  let virtualProviders = $state<VirtualProvider[]>([]);
  let workspaces = $state<Workspace[]>([]);
  let editingVirtual = $state<VirtualProvider | null>(null);
  let virtualForm = $state<VirtualProvider>(blankVirtual());
  let grants = $state<VirtualProviderGrant[]>([]);
  let loading = $state(true);
  let busy = $state(false);
  let error = $state('');
  let grantWorkspace = $state('');
  let grantPatterns = $state('*');
  let grantMaxUsd = $state(0);
  let grantAllowOverrides = $state(false);

  function blankVirtual(): VirtualProvider {
    return {
      key: '',
      name: '',
      description: '',
      default_model: '',
      disabled: false,
      models: [{ alias: '', provider_ref: providers[0]?.key || '', model: providers[0]?.config.model || '' }],
    };
  }

  function usd(cents = 0) {
    return new Intl.NumberFormat(undefined, { style: 'currency', currency: 'USD' }).format(cents / 100);
  }

  function modelsFor(providerRef: string) {
    const provider = providers.find(item => item.key === providerRef);
    const models = [...(provider?.config.models || [])];
    if (provider?.config.model && !models.includes(provider.config.model)) models.unshift(provider.config.model);
    return models;
  }

  async function load() {
    loading = true;
    error = '';
    try {
      const [providerResult, virtualResult, workspaceRows] = await Promise.all([
        listProviders({ _limit: 500 }),
        listVirtualProviders(),
        listWorkspaces(),
      ]);
      providers = providerResult.data || [];
      virtualProviders = virtualResult.data || [];
      workspaces = workspaceRows || [];
      if (!editingVirtual) virtualForm = blankVirtual();
    } catch (e: any) {
      error = e?.response?.data?.message || 'Virtual providers could not be loaded.';
    } finally {
      loading = false;
    }
  }

  onMount(load);

  function startVirtual(record?: VirtualProvider) {
    editingVirtual = record || null;
    virtualForm = record ? structuredClone(record) : blankVirtual();
    grants = [];
    if (record?.id) {
      void listVirtualProviderGrants(record.id).then(rows => grants = rows);
    }
  }

  function addModel() {
    const provider = providers[0];
    virtualForm.models = [...virtualForm.models, { alias: '', provider_ref: provider?.key || '', model: provider?.config.model || '' }];
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
      const saved = editingVirtual?.id
        ? await updateVirtualProvider(editingVirtual.id, virtualForm)
        : await createVirtualProvider(virtualForm);
      editingVirtual = saved;
      virtualForm = structuredClone(saved);
      await load();
      addToast('Virtual provider saved', 'info');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Virtual provider could not be saved', 'alert');
    } finally {
      busy = false;
    }
  }

  async function removeVirtual(record: VirtualProvider) {
    if (!record.id || !confirm(`Delete virtual provider “${record.name}”?`)) return;
    busy = true;
    try {
      await deleteVirtualProvider(record.id);
      startVirtual();
      await load();
      addToast('Virtual provider deleted', 'info');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Virtual provider could not be deleted', 'alert');
    } finally {
      busy = false;
    }
  }

  async function addGrant() {
    if (!editingVirtual?.id || !grantWorkspace) return;
    busy = true;
    try {
      await saveVirtualProviderGrant(editingVirtual.id, {
        virtual_provider_id: editingVirtual.id,
        workspace_id: grantWorkspace,
        model_patterns: grantPatterns.split(',').map(value => value.trim()).filter(Boolean),
        allow_user_overrides: grantAllowOverrides,
        max_user_limit_cents: Math.round(grantMaxUsd * 100),
      });
      grants = await listVirtualProviderGrants(editingVirtual.id);
      addToast('Workspace access saved', 'info');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Workspace access could not be saved', 'alert');
    } finally {
      busy = false;
    }
  }

  async function removeGrant(workspaceId: string) {
    if (!editingVirtual?.id) return;
    await deleteVirtualProviderGrant(editingVirtual.id, workspaceId);
    grants = grants.filter(grant => grant.workspace_id !== workspaceId);
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
  class="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto p-4 bg-black/60 sm:p-6"
  role="presentation"
  onclick={event => { if (event.target === event.currentTarget) onclose(); }}
  onkeydown={event => { if (event.key === 'Escape') onclose(); }}
>
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div role="dialog" aria-modal="true" aria-labelledby="virtual-provider-title" tabindex="-1" class="my-auto w-full max-w-6xl border shadow-xl border-dark-border bg-dark-surface" onclick={event => event.stopPropagation()}>
    <div class="flex items-center justify-between border-b px-4 py-3 border-dark-border bg-dark-elevated">
      <div>
        <h2 id="virtual-provider-title" class="text-sm font-medium text-dark-text">Virtual providers</h2>
        <p class="mt-0.5 text-xs text-dark-text-muted">Expose stable model aliases backed by exact physical provider models.</p>
      </div>
      <button aria-label="Close virtual providers" class="p-1 text-dark-text-muted hover:bg-dark-highest hover:text-dark-text" onclick={onclose}><X size={16} /></button>
    </div>

    {#if error}
      <div role="alert" class="border-b px-4 py-3 text-sm border-red-900 bg-red-950/30 text-red-300">{error} <button class="underline" onclick={load}>Retry</button></div>
    {/if}

    {#if loading}
      <div class="p-10 text-center text-sm text-dark-text-muted">Loading virtual providers…</div>
    {:else}
      <div class="grid min-h-[32rem] lg:grid-cols-[18rem_minmax(0,1fr)]">
        <aside class="border-b border-dark-border lg:border-b-0 lg:border-r">
          <div class="flex items-center justify-between border-b px-4 py-3 border-dark-border">
            <span class="text-xs font-semibold uppercase tracking-wide text-dark-text-muted">Configured</span>
            <button class="settings-button inline-flex items-center gap-1" onclick={() => startVirtual()}><Plus size={13} />New</button>
          </div>
          <div class="divide-y divide-dark-border">
            {#each virtualProviders as record}
              <button class={['w-full px-4 py-3 text-left hover:bg-dark-base', editingVirtual?.id === record.id ? 'bg-dark-base' : '']} onclick={() => startVirtual(record)}>
                <strong class="block truncate text-sm text-dark-text">{record.name}</strong>
                <span class="font-mono text-[11px] text-dark-text-muted">{record.key} · {record.models.length} models</span>
              </button>
            {/each}
            {#if virtualProviders.length === 0}<p class="p-5 text-sm text-dark-text-muted">No virtual providers yet.</p>{/if}
          </div>
        </aside>

        <section>
          <div class="flex items-center justify-between border-b px-4 py-3 border-dark-border">
            <div>
              <h3 class="text-sm font-medium text-dark-text">{editingVirtual?.id ? `Edit ${editingVirtual.name}` : 'New virtual provider'}</h3>
              <p class="mt-0.5 text-xs text-dark-text-muted">Every public alias maps to one exact physical model.</p>
            </div>
            {#if editingVirtual?.id}<button aria-label="Delete virtual provider" class="p-1 text-dark-text-muted hover:text-red-600" disabled={busy} onclick={() => removeVirtual(editingVirtual!)}><Trash2 size={16} /></button>{/if}
          </div>

          <div class="space-y-5 p-4">
            {#if providers.length === 0}
              <div class="border px-3 py-2 text-xs border-amber-800 bg-amber-900/20 text-amber-300">Create a workspace provider before creating a virtual provider.</div>
            {/if}
            <div class="grid gap-3 md:grid-cols-2">
              <label class="text-xs text-dark-text-secondary">Display name<input bind:value={virtualForm.name} placeholder="Company AI" /></label>
              <label class="text-xs text-dark-text-secondary">Provider key<input class="font-mono" bind:value={virtualForm.key} placeholder="company-ai" /></label>
            </div>
            <label class="text-xs text-dark-text-secondary">Description<textarea rows="2" bind:value={virtualForm.description}></textarea></label>

            <div>
              <div class="flex items-center justify-between gap-3">
                <div><h4 class="text-xs font-semibold uppercase tracking-wide text-dark-text-muted">Model catalogue</h4><p class="mt-1 text-xs text-dark-text-muted">Aliases are the model IDs consumers see.</p></div>
                <button class="settings-button inline-flex items-center gap-1" disabled={providers.length === 0} onclick={addModel}><Plus size={13} />Model</button>
              </div>
              <div class="mt-3 space-y-2">
                {#each virtualForm.models as model, index}
                  <div class="grid items-end gap-2 border p-3 border-dark-border bg-dark-base md:grid-cols-[minmax(8rem,.7fr)_minmax(9rem,1fr)_minmax(12rem,1.3fr)_auto]">
                    <label class="text-[11px] text-dark-text-muted">Public alias<input class="font-mono" value={model.alias} oninput={event => updateModel(index, { alias: event.currentTarget.value })} /></label>
                    <label class="text-[11px] text-dark-text-muted">Physical provider<select value={model.provider_ref} onchange={event => { const ref = event.currentTarget.value; updateModel(index, { provider_ref: ref, model: modelsFor(ref)[0] || '' }); }}>{#each providers as provider}<option value={provider.key}>{provider.key}</option>{/each}</select></label>
                    <label class="text-[11px] text-dark-text-muted">Physical model<select value={model.model} onchange={event => updateModel(index, { model: event.currentTarget.value })}>{#each modelsFor(model.provider_ref) as name}<option value={name}>{name}</option>{/each}</select></label>
                    <button aria-label={`Remove model ${model.alias || index + 1}`} class="settings-button p-2 text-red-600" disabled={virtualForm.models.length === 1} onclick={() => removeModel(index)}><Trash2 size={14} /></button>
                  </div>
                {/each}
              </div>
            </div>

            <label class="max-w-sm text-xs text-dark-text-secondary">Default public model<select bind:value={virtualForm.default_model}>{#each virtualForm.models.filter(model => model.alias) as model}<option value={model.alias}>{model.alias}</option>{/each}</select></label>
            <button class="settings-primary inline-flex items-center gap-2" disabled={busy || providers.length === 0 || !virtualForm.name || !virtualForm.key || virtualForm.models.some(model => !model.alias || !model.provider_ref || !model.model)} onclick={saveVirtual}><Save size={14} />{editingVirtual?.id ? 'Save virtual provider' : 'Create virtual provider'}</button>

            {#if editingVirtual?.id}
              <div class="border-t pt-4 border-dark-border">
                <h4 class="text-xs font-semibold uppercase tracking-wide text-dark-text-muted">Workspace access</h4>
                <p class="mt-1 text-xs text-dark-text-muted">Recipients see only the virtual aliases; physical credentials remain private.</p>
                <div class="mt-3 grid items-end gap-2 md:grid-cols-[minmax(10rem,1fr)_minmax(8rem,.8fr)_7rem_auto]">
                  <label class="text-[11px] text-dark-text-muted">Workspace<select bind:value={grantWorkspace}><option value="">Select…</option>{#each workspaces as workspace}<option value={workspace.id}>{workspace.name}</option>{/each}</select></label>
                  <label class="text-[11px] text-dark-text-muted">Model patterns<input class="font-mono" bind:value={grantPatterns} placeholder="*" /></label>
                  <label class="text-[11px] text-dark-text-muted">Max user USD<input type="number" min="0" step="0.01" bind:value={grantMaxUsd} /></label>
                  <button class="settings-button" disabled={!grantWorkspace || busy} onclick={addGrant}>Share</button>
                </div>
                <label class="mt-2 flex items-center gap-2 text-xs text-dark-text-muted"><input type="checkbox" bind:checked={grantAllowOverrides} />Workspace admins may customize account allowances</label>
                <div class="mt-3 divide-y divide-dark-border">
                  {#each grants as grant}
                    <div class="flex items-center justify-between gap-3 py-2"><div><p class="text-xs font-medium text-dark-text-secondary">{workspaces.find(workspace => workspace.id === grant.workspace_id)?.name || grant.workspace_id}</p><p class="font-mono text-[11px] text-dark-text-muted">{grant.model_patterns.join(', ')} · {grant.max_user_limit_cents ? `max ${usd(grant.max_user_limit_cents)}` : 'no override ceiling'}</p></div><button aria-label="Remove workspace access" class="p-1 text-dark-text-muted hover:text-red-600" onclick={() => removeGrant(grant.workspace_id)}><Trash2 size={14} /></button></div>
                  {/each}
                  {#if grants.length === 0}<p class="py-3 text-xs text-dark-text-muted">This provider is available only in its owner workspace.</p>{/if}
                </div>
              </div>
            {/if}
          </div>
        </section>
      </div>
    {/if}
  </div>
</div>
