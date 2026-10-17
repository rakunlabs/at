<script lang="ts">
  import type { ProviderQuotaConfig } from '@/lib/api/providers';
  import { quotaUsage, type QuotaUsage } from '@/lib/api/free-tier';
  interface Props { value: ProviderQuotaConfig; providerKey?: string; personal?: boolean; prefix: string; }
  let { value = $bindable({}), providerKey = '', personal = false, prefix }: Props = $props();
  let usage = $state<QuotaUsage[]>([]);
  let issue = $state('');
  let loading = $state(false);
  const dimensions = [
    { key: 'requests_per_day', label: 'Requests / day' },
    { key: 'tokens_per_day', label: 'Tokens / day' },
    { key: 'requests_per_month', label: 'Requests / month' },
    { key: 'tokens_per_month', label: 'Tokens / month' },
  ] as const;
  let modelJSON = $state('');
  let modelIssue = $state('');
  $effect(() => { modelJSON = value.models ? JSON.stringify(value.models, null, 2) : ''; });
  function applyModels() {
    try {
      const models = modelJSON.trim() ? JSON.parse(modelJSON) : undefined;
      if (models && (Array.isArray(models) || typeof models !== 'object')) throw new Error('Use an object keyed by model ID.');
      value = { ...value, models };
      modelIssue = '';
    } catch { modelIssue = 'Invalid model quota JSON. Fix it and click Apply; the previous model quotas are unchanged.'; }
  }
  async function refresh() {
    loading = true;
    try { usage = await quotaUsage(providerKey, personal); issue = ''; }
    catch { issue = 'Quota usage could not be loaded. Retry to see current counters.'; }
    finally { loading = false; }
  }
</script>

<details class="border border-dark-border text-xs">
  <summary class="cursor-pointer px-4 py-3 text-dark-text-secondary">Daily and monthly quota</summary>
  <div class="border-t border-dark-border p-4 space-y-3">
    <p class="text-dark-text-muted max-w-prose">Local ceilings for this provider account. Zero or empty means no ceiling. UTC day/month windows; model pools also consume the shared account pool. Limits from different providers are never added together.</p>
    <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
      {#each dimensions as d}
        <label for={`${prefix}-${d.key}`} class="space-y-1 text-dark-text-secondary">
          <span class="block">{d.label}</span>
          <input id={`${prefix}-${d.key}`} type="number" min="0" max="1000000000000" step="1" bind:value={value[d.key]} class="w-full min-w-0 border border-dark-border bg-dark-base px-2 py-1.5 focus-visible:outline-accent" />
        </label>
      {/each}
    </div>
    <p class="text-dark-text-muted max-w-prose">AT reserves estimated input plus the requested output allowance before a call, then settles reported tokens. Missing usage retains the estimate. Requests outside AT and other keys for the same upstream account are not visible. This is not a guarantee against upstream billing.</p>
    <details>
      <summary class="cursor-pointer py-1 text-dark-text-secondary">Model-specific pools (optional)</summary>
      <label for={`${prefix}-models`} class="block my-2 text-dark-text-muted">JSON keyed by model ID, for example {`{"my-model":{"requests_per_day":100}}`}</label>
      <textarea id={`${prefix}-models`} rows="4" bind:value={modelJSON} class="w-full border border-dark-border bg-dark-base p-2" spellcheck="false"></textarea>
      <button type="button" onclick={applyModels} class="border border-dark-border px-3 py-1.5 hover:bg-dark-elevated">Apply model quotas</button>
      {#if modelIssue}<p role="alert" class="mt-2 text-oc-red">{modelIssue}</p>{/if}
    </details>
    {#if providerKey}
      <button type="button" onclick={refresh} disabled={loading} class="border border-dark-border px-3 py-1.5 hover:bg-dark-elevated disabled:opacity-50">{loading ? 'Loading…' : 'Refresh quota usage'}</button>
      {#if issue}<p role="alert" class="text-oc-red">{issue}</p>{/if}
      {#each usage as u}
        <div class="border-t border-dark-border pt-2 text-dark-text-secondary break-words">
          <div>{u.model || 'Shared account pool'} · {u.period}</div>
          <div>{u.requests} / {u.request_limit || 'uncapped'} requests · {u.tokens} tokens + {u.reserved_tokens} reserved / {u.token_limit || 'uncapped'}</div>
          <div class="text-dark-text-muted">Resets {new Date(u.reset_at).toLocaleString()}</div>
        </div>
      {/each}
    {/if}
  </div>
</details>
