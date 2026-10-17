<script lang="ts">
  import { onMount } from 'svelte';
  import { freeTierCatalog, providerUsePolicy, saveProviderUsePolicy, createPersonalProfile, findPersonalProfile, type FreeTierDraft, type ProviderUsePolicy } from '@/lib/api/free-tier';
  import { createPersonalProvider, listPersonalProviders } from '@/lib/api/providers';
  import { createToken } from '@/lib/api/tokens';
  import { can } from '@/lib/store/workspace.svelte';
  import { isFeatureEnabled } from '@/lib/store/features.svelte';
  import { freeTierConfig, freeTierTargets, freeTierModels, canAdoptFreeTierProfile, uncertainTokenCreation } from '@/lib/helper/free-tier-setup';
  import { deploymentUrl } from '@/lib/helper/deployment-url';
  import ProviderQuota from './ProviderQuota.svelte';
  interface Props { ondone: () => void; }
  let { ondone }: Props = $props();
  let drafts = $state<FreeTierDraft[]>([]);
  let policy = $state<ProviderUsePolicy | null>(null);
  let hosts = $state('');
  let restrictHosts = $state(false);
  let loadIssue = $state('');
  let policyIssue = $state('');
  let issue = $state('');
  let busy = $state(false);
  let savingPolicy = $state(false);
  let attempted = $state(false);
  let acknowledged = $state(false);
  let profile = $state('');
  let name = $state(`free-${crypto.randomUUID().slice(0, 8)}`);
  let fallback = $state('');
  let mintToken = $state(false);
  let token = $state('');
  let tokenCreated = $state(false);
  let uncertainToken = $state(false);
  let canUsePersonal = $derived(can('personal_providers.manage'));
  let canWritePolicy = $derived(can('workspace.write'));
  let canMint = $derived(can('tokens.write') && isFeatureEnabled('api_tokens'));
  let selected = $derived(drafts.filter((d) => d.selected));
  let totalTargets = $derived(selected.reduce((n, d) => n + freeTierModels(d.models).length, fallback.trim() ? 1 : 0));

  async function load() {
    loadIssue = '';
    try {
      const [entries, current] = await Promise.all([canUsePersonal ? freeTierCatalog() : Promise.resolve([]), providerUsePolicy()]);
      drafts = entries.map((entry) => ({ entry, selected: false, key: '', models: entry.model_hint, quota: {} }));
      policy = current; restrictHosts = current.allowed_personal_hosts !== null;
      hosts = current.allowed_personal_hosts?.join('\n') || '';
    } catch { loadIssue = 'Could not load the free-tier catalog and workspace policy. Retry before entering credentials.'; }
  }
  onMount(load);

  async function savePolicy() {
    if (!policy) return;
    savingPolicy = true; policyIssue = '';
    try {
      policy = await saveProviderUsePolicy({ ...policy, allowed_personal_hosts: restrictHosts ? hosts.split(/[\n,]/).map((h) => h.trim()).filter(Boolean) : null });
    } catch (e: any) { policyIssue = e?.response?.data?.message || 'Policy save failed. Reload before retrying a conflicting change.'; }
    finally { savingPolicy = false; }
  }

  async function install() {
    issue = '';
    if (!acknowledged || !selected.length) { issue = 'Select a provider and confirm the terms and data-use notice.'; return; }
    if (!/^[a-zA-Z0-9._-]{1,100}$/.test(name)) { issue = 'Use letters, digits, dots, underscores or hyphens for the profile name (up to 100 characters).'; return; }
    if (totalTargets > 16) { issue = 'Choose at most 16 targets including the optional paid fallback.'; return; }
    if (fallback.trim() && !/^[^/]+\/.+$/.test(fallback.trim())) { issue = 'The fallback must be an existing provider/model reference.'; return; }
    const continuing = attempted;
    for (const d of selected) d.saved_key ||= `${name}-${d.entry.id}`;
    busy = true;
    let stage = 'providers';
    try {
      // On explicit continuation, GET by the stable setup key before any new
      // create. A response lost after commit never duplicates that provider.
      if (continuing) {
        for (const d of selected.filter((d) => !d.created_reference)) {
          const found = (await listPersonalProviders({ key: d.saved_key, _limit: 2 } as any)).data.find((p) => p.key === d.saved_key);
          if (!found) continue;
          if (found.config.free_tier !== d.entry.id || found.config.base_url !== d.entry.base_url || JSON.stringify(found.config.models) !== JSON.stringify(freeTierModels(d.models))) throw new Error(`Existing provider ${d.saved_key} does not match this setup. Review My providers first.`);
          d.created_reference = found.reference || `provider:${found.id}`;
        }
      }
      // Validate every remaining config before creating the first resource.
      for (const d of selected.filter((d) => !d.created_reference)) freeTierConfig(d);
      attempted = true;
      for (const draft of selected.filter((d) => !d.created_reference)) {
        const cfg = freeTierConfig(draft);
        const created = await createPersonalProvider(draft.saved_key!, cfg, 'personal');
        draft.created_reference = created.reference || `provider:${created.id}`;
        draft.key = ''; cfg.api_key = '';
      }
      const targets = freeTierTargets(selected, fallback);
      stage = 'profile';
      if (!profile) {
        const existing = await findPersonalProfile(name);
        if (existing && !canAdoptFreeTierProfile(existing, targets)) throw new Error('That profile name belongs to a different setup or routing strategy. Choose another name and continue; saved providers will be reused.');
        const created = existing || await createPersonalProfile(name, targets);
        profile = created.name;
      }
      stage = 'token';
      if (mintToken && canMint && !tokenCreated && !uncertainToken) {
        const result = await createToken({ name: `${profile} gateway`, scope: 'personal',
          allowed_providers_mode: 'none', allowed_models_mode: 'list', allowed_models: targets,
          allowed_mcps_mode: 'none', allowed_webhooks_mode: 'none',
          expires_at: new Date(Date.now() + 30 * 86400000).toISOString() });
        token = result.token;
        tokenCreated = true;
      }
    } catch (e: any) {
      uncertainToken = stage === 'token' && uncertainTokenCreation(e?.response?.status);
      issue = e?.response?.data?.message || e.message || 'Setup stopped. A request may have completed before the connection failed.';
      issue += uncertainToken
        ? ' The token response may have been lost. Check API Tokens and rotate that token; do not create another automatically.'
        : ' Saved steps are retained. Correct the remaining inputs and choose Continue setup; existing providers will be checked and reused, not recreated.';
    } finally {
      // Secrets never enter localStorage, saved presets or logs.
      for (const draft of drafts) draft.key = '';
      busy = false; ondone();
    }
  }
</script>

<details class="border border-dark-border mb-4 text-xs">
  <summary class="cursor-pointer px-4 py-3 text-dark-text-secondary">Free-tier setup and workspace policy</summary>
  <div class="border-t border-dark-border p-4 space-y-4">
    <p class="text-dark-text-secondary max-w-prose">Bring your own accounts. AT does not provide tokens or combine other users' keys. Free plans, included credits and model availability change; check each provider before use. No paid fallback is added unless you choose one.</p>
    {#if loadIssue}<p role="alert" class="text-oc-red">{loadIssue}</p><button type="button" onclick={load} class="border border-dark-border px-3 py-1.5">Retry catalog</button>{/if}
    {#if policy}
      <details class="border-y border-dark-border py-3">
        <summary class="cursor-pointer text-dark-text-secondary">Workspace policy · {policy.block_free_tier ? 'free-tier endpoints blocked' : 'free-tier setup allowed'}</summary>
        <div class="pt-3 space-y-3">
          <p class="text-dark-text-muted max-w-prose">Applies to creation and live server-side use of personal providers in this workspace. Blocking known free-tier endpoints also blocks paid accounts on the same hosts. For stricter control, allow only approved hosts. This is not a network sandbox: trusted-host tools, browser-local providers and external relays need separate controls.</p>
          <label class="flex items-center gap-2"><input type="checkbox" bind:checked={policy.block_free_tier} disabled={!canWritePolicy || savingPolicy} />Block catalog free-tier personal providers and known endpoints</label>
          <label class="flex items-center gap-2"><input type="checkbox" bind:checked={restrictHosts} disabled={!canWritePolicy || savingPolicy} />Restrict all personal providers to approved HTTPS hosts</label>
          {#if restrictHosts}
            <label for="free-tier-hosts" class="block text-dark-text-secondary">Exact hostnames, one per line. Empty blocks all personal endpoints.</label>
            <textarea id="free-tier-hosts" rows="3" bind:value={hosts} disabled={!canWritePolicy || savingPolicy} class="w-full border border-dark-border bg-dark-base p-2" placeholder="api.example.com"></textarea>
          {/if}
          {#if canWritePolicy}<button type="button" onclick={savePolicy} disabled={savingPolicy} class="border border-dark-border px-3 py-1.5 hover:bg-dark-elevated">{savingPolicy ? 'Saving…' : 'Save workspace policy'}</button>{/if}
          {#if policyIssue}<p role="alert" class="text-oc-red">{policyIssue}</p>{/if}
        </div>
      </details>
    {/if}
    {#if canUsePersonal && policy && !policy.block_free_tier}
      <fieldset disabled={busy} class="space-y-4 disabled:opacity-75">
        <legend class="mb-3 text-sm text-dark-text">Connect personal free-tier accounts</legend>
        {#each drafts as draft}
          <div class="border-b border-dark-border pb-3 space-y-2">
            <label class="flex items-center gap-2 text-dark-text-secondary"><input type="checkbox" bind:checked={draft.selected} disabled={attempted} />{draft.entry.name}<span class="border border-dark-border px-1.5 text-dark-text-muted">Free-tier candidate</span></label>
            <div class="text-dark-text-muted break-words">{draft.entry.note}</div>
            <div class="flex flex-wrap gap-x-4 gap-y-1"><a href={draft.entry.signup_url} target="_blank" rel="noreferrer" class="text-accent underline">Get API key</a><a href={draft.entry.source_url} target="_blank" rel="noreferrer" class="text-accent underline">Limits and eligibility</a><span class="text-dark-text-muted">{draft.entry.reviewed_at ? `Reviewed ${draft.entry.reviewed_at}` : 'Check current terms; not recently verified'}</span></div>
            {#if draft.selected}
              <p class="text-oc-peach max-w-prose">{draft.entry.privacy}</p>
              <fieldset disabled={!!draft.created_reference} class="grid gap-3 sm:grid-cols-2">
                <label class="space-y-1 text-dark-text-secondary"><span class="block">Your API key</span><input aria-label={`${draft.entry.name} API key`} type="password" autocomplete="off" bind:value={draft.key} class="w-full min-w-0 border border-dark-border bg-dark-base px-2 py-1.5" /></label>
                <label class="space-y-1 text-dark-text-secondary"><span class="block">Eligible model IDs (comma separated)</span><input disabled={attempted} aria-label={`${draft.entry.name} models`} bind:value={draft.models} class="w-full min-w-0 border border-dark-border bg-dark-base px-2 py-1.5" /></label>
              </fieldset>
              <label class="flex flex-wrap items-center gap-2 text-dark-text-secondary">Requests / minute (optional)<input disabled={!!draft.created_reference} type="number" min="0" step="1" bind:value={draft.rpm} class="w-28 border border-dark-border bg-dark-base px-2 py-1.5" /></label>
              <fieldset disabled={!!draft.created_reference}><ProviderQuota bind:value={draft.quota} prefix={`free-${draft.entry.id}`} /></fieldset>
            {/if}
          </div>
        {/each}
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="space-y-1 text-dark-text-secondary"><span class="block">Personal profile name</span><input disabled={!!profile} bind:value={name} class="w-full min-w-0 border border-dark-border bg-dark-base px-2 py-1.5" /></label>
          <label class="space-y-1 text-dark-text-secondary"><span class="block">Existing paid fallback (optional)</span><input disabled={!!profile} bind:value={fallback} placeholder="provider/model" class="w-full min-w-0 border border-dark-border bg-dark-base px-2 py-1.5" /></label>
        </div>
        {#if fallback.trim()}<p class="text-oc-peach">The final fallback can incur charges when free targets fail or reach their quota.</p>{/if}
        <label class="flex items-start gap-2 text-dark-text-secondary"><input type="checkbox" bind:checked={acknowledged} class="mt-0.5" />I checked my account's eligibility, limits, billing settings and data-use terms for the selected models. Keys remain personal; AT does not guarantee zero charges.</label>
        {#if canMint}<label class="flex items-center gap-2 text-dark-text-secondary"><input type="checkbox" bind:checked={mintToken} />Also create a personal gateway token (these models only, expires in 30 days)</label>{:else}<p class="text-dark-text-muted">Creating a gateway token needs tokens.write. Ask your workspace administrator for that permission or use an existing personal token.</p>{/if}
        <button type="button" onclick={install} disabled={!selected.length || !acknowledged || uncertainToken || (!!profile && (!mintToken || tokenCreated))} class="bg-accent text-dark-base px-3 py-1.5 font-medium disabled:opacity-50">{busy ? 'Setting up…' : attempted ? 'Continue setup' : 'Create personal providers and profile'}</button>
      </fieldset>
    {/if}
    {#if issue}<p role="alert" class="text-oc-red max-w-prose">{issue}</p>{/if}
    {#each drafts.filter((d) => d.created_reference) as saved}<p class="break-all text-dark-text-secondary">Saved {saved.entry.name}: <code>{saved.created_reference}</code></p>{/each}
    {#if profile}
      <section aria-label="Gateway connection" class="border-t border-dark-border pt-3 space-y-2 text-dark-text-secondary">
        <h3 class="text-sm text-dark-text">Personal gateway ready</h3>
        <div class="break-all">Base URL: <code>{deploymentUrl('gateway/v1')}</code></div>
        <div>Model: <code>{profile}</code></div>
        {#if token}
          <label class="block">Gateway token — copy now; closing this page loses the plaintext<input readonly value={token} aria-label="New gateway token" onclick={(e) => e.currentTarget.select()} class="mt-1 w-full border border-dark-border bg-dark-base p-2" /></label>
          <button type="button" onclick={() => token = ''} class="border border-dark-border px-3 py-1.5">Hide token</button>
        {/if}
        <p>Use an owner-scoped personal AT token, not the upstream keys. Shared workspace tokens cannot use this profile.</p>
        <a href="#/docs" class="text-accent underline">Client configuration examples (Claude Code, OpenCode and SDKs)</a>
      </section>
    {/if}
  </div>
</details>
