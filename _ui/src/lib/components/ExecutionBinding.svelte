<script lang="ts">
  import { getExecutionBinding, saveExecutionBinding, revokeExecutionBinding, type ExecutionBinding, type BindingCandidate } from '@/lib/api/execution-bindings';
  import { startBot } from '@/lib/api/bots';

  interface Props { kind: 'bot' | 'mcp'; subjectId: string; onchange?: () => void }
  let { kind, subjectId, onchange }: Props = $props();
  let binding = $state<ExecutionBinding | null>(null);
  let candidates = $state<BindingCandidate[]>([]);
  let user = $state('');
  let loading = $state(true);
  let busy = $state(false);
  let error = $state('');
  let notice = $state('');
  let generation = 0;
  const inputId = $props.id();

  $effect(() => {
    load(kind, subjectId);
    return () => { generation++; };
  });

  async function load(currentKind: 'bot' | 'mcp', id: string) {
    const request = ++generation;
    loading = true;
    error = notice = '';
    try {
      const result = await getExecutionBinding(currentKind, id);
      if (request !== generation) return;
      binding = result.binding;
      candidates = result.candidates || [];
      user = binding?.user_id || (candidates.length === 1 ? candidates[0].user_id : '');
    } catch (e: any) {
      if (request === generation) error = e?.response?.data?.message || 'Could not load execution identity. Retry below.';
    } finally {
      if (request === generation) loading = false;
    }
  }

  async function save() {
    if (busy || !user) return;
    busy = true;
    error = notice = '';
    try {
      binding = await saveExecutionBinding(kind, subjectId, user);
      notice = 'Execution identity saved.';
      if (kind === 'bot') {
        try {
          await startBot(subjectId);
          notice = 'Execution identity saved. Bot started.';
        } catch (e: any) {
          error = e?.response?.data?.message || 'Identity saved, but the bot could not start. Check its token and permissions, then retry Start.';
        }
      }
      onchange?.();
    } catch (e: any) {
      error = e?.response?.data?.message || 'Could not save execution identity. Reload and retry.';
    } finally {
      busy = false;
    }
  }

  async function revoke() {
    if (busy) return;
    busy = true;
    error = notice = '';
    try {
      binding = await revokeExecutionBinding(kind, subjectId);
      notice = kind === 'bot' ? 'Execution identity revoked. Bot stopped.' : 'Execution identity revoked. New MCP requests will be rejected.';
      onchange?.();
    } catch (e: any) {
      error = e?.response?.data?.message || 'Could not revoke execution identity. Retry.';
    } finally {
      busy = false;
    }
  }
</script>

<section class="space-y-3 border-b border-gray-200 dark:border-dark-border pb-4" aria-label="Execution identity" aria-busy={loading || busy}>
  <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text">Execution identity</h3>
  <p class="text-xs text-gray-600 dark:text-dark-text-secondary max-w-prose">
    {kind === 'bot' ? 'Bot messages' : 'MCP tool calls'} run with the selected account's permissions, independently of your login session.
    After changing permissions or execution policy, renew this binding.
  </p>
  <p class="text-xs text-gray-600 dark:text-dark-text-secondary max-w-prose">
    Configure the workspace's <a href="#/settings/execution" class="underline underline-offset-2 hover:text-gray-900 dark:hover:text-dark-text">execution policy</a> first, including allowed tools. Builtin management tools currently require trusted-host mode.
  </p>
  {#if loading}
    <p role="status" class="text-sm text-gray-600 dark:text-dark-text-secondary">Loading execution identity…</p>
  {:else}
    <p class="text-xs text-gray-600 dark:text-dark-text-secondary">
      {binding ? (binding.revoked ? 'Revoked' : `Bound · version ${binding.version} · policy ${binding.policy_version}`) : 'Setup required — no execution identity is bound.'}
    </p>
    {#if candidates.length > 0}
      <div class="flex flex-col sm:flex-row sm:items-end gap-3">
        <div class="flex-1 min-w-0 space-y-1">
          <label for={inputId} class="block text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Run as</label>
          <select id={inputId} bind:value={user} disabled={busy} class="w-full min-w-0 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated dark:text-dark-text px-3 py-2 text-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50">
            <option value="">Select an account</option>
            {#each candidates as candidate (candidate.user_id)}
              <option value={candidate.user_id}>{candidate.name} ({candidate.role})</option>
            {/each}
          </select>
        </div>
        <button type="button" onclick={save} disabled={busy || !user} class="px-3 py-2 text-sm bg-gray-900 dark:bg-accent text-white hover:bg-gray-700 dark:hover:bg-accent-hover focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50 disabled:cursor-not-allowed">
          {busy ? 'Saving…' : kind === 'bot' ? 'Bind & start bot' : 'Save execution identity'}
        </button>
      </div>
    {:else if !error}
      <p class="text-sm text-gray-600 dark:text-dark-text-secondary">No eligible account is available. Enable execution for this workspace and add an active member, then reload.</p>
    {/if}
    <div class="flex flex-wrap gap-4 text-xs">
      <button type="button" onclick={() => load(kind, subjectId)} disabled={busy} class="underline underline-offset-2 text-gray-700 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text disabled:opacity-50">Reload identity</button>
      {#if binding && !binding.revoked}
        <button type="button" onclick={revoke} disabled={busy} class="underline underline-offset-2 text-red-700 dark:text-red-400 hover:text-red-900 dark:hover:text-red-300 disabled:opacity-50">Revoke identity</button>
      {/if}
    </div>
  {/if}
  {#if error}<p role="alert" class="text-sm text-red-700 dark:text-red-400 break-words">{error}</p>{/if}
  {#if notice}<p role="status" class="text-sm text-gray-700 dark:text-dark-text-secondary">{notice}</p>{/if}
</section>
