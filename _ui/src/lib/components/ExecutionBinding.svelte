<script lang="ts">
  import { getExecutionBinding, saveExecutionBinding, revokeExecutionBinding, type BindingKind, type ExecutionBinding, type BindingCandidate } from '@/lib/api/execution-bindings';
  import { startBot } from '@/lib/api/bots';
  import { routeAllowed } from '@/lib/helper/navigation';
  import { storeAuth } from '@/lib/store/auth.svelte';

  interface Props { kind: BindingKind; subjectId: string; onchange?: () => void }
  let { kind, subjectId, onchange }: Props = $props();
  let binding = $state<ExecutionBinding | null>(null);
  let bindingValid = $state(false);
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

  async function load(currentKind: BindingKind, id: string) {
    const request = ++generation;
    loading = true;
    error = notice = '';
    try {
      const result = await getExecutionBinding(currentKind, id);
      if (request !== generation) return;
      binding = result.binding;
      bindingValid = result.binding_valid;
      candidates = result.candidates || [];
      // Default to the signed-in account: binding is then a single click.
      const self = candidates.find((c) => c.user_id === storeAuth.identity?.subject)?.user_id;
      user = (binding && !binding.revoked ? binding.user_id : '') || self || (candidates.length === 1 ? candidates[0].user_id : '');
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
      bindingValid = true;
      notice = 'Saved. It now runs as this account.';
      if (kind === 'bot') {
        try {
          await startBot(subjectId);
          notice = 'Saved. Bot started.';
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
      bindingValid = false;
      notice = kind === 'bot' ? 'Execution identity revoked. Bot stopped.' : kind === 'trigger' ? 'Execution identity revoked. New webhook requests will be rejected.' : 'Execution identity revoked. New MCP requests will be rejected.';
      onchange?.();
    } catch (e: any) {
      error = e?.response?.data?.message || 'Could not revoke execution identity. Retry.';
    } finally {
      busy = false;
    }
  }
</script>

<section class="space-y-3 border-b border-dark-border pb-4" aria-label="Execution identity" aria-busy={loading || busy}>
  <h3 class="text-sm font-medium text-dark-text">Run as</h3>
  <p class="text-xs text-dark-text-secondary max-w-prose">
    {kind === 'bot' ? 'Bot messages' : kind === 'trigger' ? 'Scheduled and webhook runs' : 'MCP tool calls'} run as the account chosen here, with that account's current permissions and the workspace's current
    {#if routeAllowed('/settings/execution')}<a href="#/settings/execution" class="underline underline-offset-2 hover:text-dark-text">execution policy</a>{:else}execution policy{/if}.
    Choose it once: later permission or policy changes apply automatically.
  </p>
  {#if loading}
    <p role="status" class="text-sm text-dark-text-secondary">Loading execution identity…</p>
  {:else}
    <p class="text-xs text-dark-text-secondary">
      {binding ? (binding.revoked ? 'Not set (revoked). Choose an account to enable it again.' : bindingValid ? `Active · runs as ${candidates.find((c) => c.user_id === binding?.user_id)?.name || binding.user_id}` : 'Not allowed to run: the account is no longer an active member or lacks permission. Choose another account.') : 'Not set yet — choose an account so this can run.'}
    </p>
    {#if candidates.length > 0}
      <div class="flex flex-col sm:flex-row sm:items-end gap-3">
        <div class="flex-1 min-w-0 space-y-1">
          <label for={inputId} class="block text-sm font-medium text-dark-text-secondary">Run as</label>
          <select id={inputId} bind:value={user} disabled={busy} class="w-full min-w-0 border border-dark-border-subtle bg-dark-elevated text-dark-text px-3 py-2 text-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50">
            <option value="">Select an account</option>
            {#each candidates as candidate (candidate.user_id)}
              <option value={candidate.user_id}>{candidate.name} ({candidate.role})</option>
            {/each}
          </select>
        </div>
        <button type="button" onclick={save} disabled={busy || !user} class="px-3 py-2 text-sm bg-accent text-dark-base hover:bg-accent-hover focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50 disabled:cursor-not-allowed">
          {busy ? 'Saving…' : kind === 'bot' ? 'Save & start bot' : 'Save'}
        </button>
      </div>
    {:else if !error}
      <p class="text-sm text-dark-text-secondary">No eligible account is available. Enable execution for this workspace and add an active member, then reload.</p>
    {/if}
    <div class="flex flex-wrap gap-4 text-xs">
      <button type="button" onclick={() => load(kind, subjectId)} disabled={busy} class="underline underline-offset-2 text-dark-text-secondary hover:text-dark-text disabled:opacity-50">Reload</button>
      {#if binding && !binding.revoked}
        <button type="button" onclick={revoke} disabled={busy} class="underline underline-offset-2 text-red-400 hover:text-red-300 disabled:opacity-50">Stop running as this account</button>
      {/if}
    </div>
  {/if}
  {#if error}<p role="alert" class="text-sm text-red-400 break-words">{error}</p>{/if}
  {#if notice}<p role="status" class="text-sm text-dark-text-secondary">{notice}</p>{/if}
</section>
