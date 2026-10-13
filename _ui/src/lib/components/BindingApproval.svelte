<script lang="ts">
  import { getExecutionBinding, saveExecutionBinding, type BindingCandidate } from '@/lib/api/execution-bindings';
  import { startBot } from '@/lib/api/bots';
  import { storeAuth } from '@/lib/store/auth.svelte';
  import type { BindingRequest } from '@/lib/helper/binding-approval';

  interface Props { request: BindingRequest }
  let { request }: Props = $props();

  let phase = $state<'loading' | 'pending' | 'approved' | 'dismissed' | 'error'>('loading');
  let candidates = $state<BindingCandidate[]>([]);
  let user = $state('');
  let choosing = $state(false);
  let busy = $state(false);
  let error = $state('');
  let approvedAs = $state('');
  let approvedNote = $state('');

  const nameOf = (id: string) => candidates.find((c) => c.user_id === id)?.name || id;
  const what = $derived(request.kind === 'trigger' ? 'schedule' : request.kind === 'bot' ? 'bot' : 'MCP server');

  $effect(() => { void load(request.kind, request.subjectId); });

  async function load(kind: BindingRequest['kind'], id: string) {
    phase = 'loading';
    error = '';
    try {
      const details = await getExecutionBinding(kind, id);
      candidates = details.candidates || [];
      const self = candidates.find((c) => c.user_id === storeAuth.identity?.subject)?.user_id;
      user = self || candidates[0]?.user_id || '';
      if (details.binding && !details.binding.revoked && details.binding_valid) {
        approvedAs = nameOf(details.binding.user_id);
        phase = 'approved';
      } else {
        phase = 'pending';
      }
    } catch (e: any) {
      // A deleted subject or a member without access: nothing to approve here.
      error = e?.response?.status === 404 ? `This ${what} no longer exists.` : e?.response?.data?.message || `Could not load the ${what}'s Run as account.`;
      phase = 'error';
    }
  }

  async function approve() {
    if (busy || !user) return;
    busy = true;
    error = '';
    try {
      await saveExecutionBinding(request.kind, request.subjectId, user);
      approvedAs = nameOf(user);
      phase = 'approved';
      if (request.kind === 'bot') {
        // Saving restarts nothing by itself; approval is what lets the bot run.
        try {
          await startBot(request.subjectId);
          approvedNote = 'Bot started.';
        } catch (e: any) {
          approvedNote = e?.response?.status === 409 ? 'Bot is running.' : `Bot not started: ${e?.response?.data?.message || 'start it on the Bots page'}.`;
        }
      }
    } catch (e: any) {
      error = e?.response?.data?.message || 'Could not approve. Retry, or set Run as on the settings page.';
    } finally {
      busy = false;
    }
  }
</script>

{#if phase !== 'dismissed'}
  <section aria-label={`Approve ${what}`} class="my-1.5 border border-dark-border px-3 py-2 text-xs">
    {#if phase === 'loading'}
      <p role="status" class="text-dark-text-muted">Checking the {what}'s Run as account…</p>
    {:else if phase === 'approved'}
      <p class="text-dark-text-secondary"><span class="text-oc-green">✓</span> {request.label} runs as <span class="text-dark-text">{approvedAs}</span>.{#if approvedNote} {approvedNote}{/if}</p>
    {:else if phase === 'error'}
      <p role="alert" class="text-dark-text-muted">{error}</p>
    {:else}
      <p class="text-dark-text">The {what} <span class="text-oc-peach">{request.label}</span> needs approval before it can run.</p>
      <p class="mt-0.5 text-dark-text-muted">It will run with the chosen account's permissions.</p>
      {#if candidates.length === 0}
        <p class="mt-1.5 text-dark-text-muted">You have no account that can be chosen here. Ask a workspace administrator to approve it.</p>
      {:else}
        <div class="mt-2 flex flex-wrap items-center gap-2">
          {#if choosing}
            <select bind:value={user} disabled={busy} aria-label="Run as" class="min-w-0 border border-dark-border-subtle bg-dark-elevated px-2 py-1 text-dark-text">
              {#each candidates as c (c.user_id)}<option value={c.user_id}>{c.name} ({c.role})</option>{/each}
            </select>
          {/if}
          <button type="button" onclick={approve} disabled={busy || !user} class="bg-accent px-3 py-1 text-dark-base hover:bg-accent-hover disabled:opacity-50">
            {busy ? 'Approving…' : `Approve as ${nameOf(user)}`}
          </button>
          {#if !choosing && candidates.length > 1}
            <button type="button" onclick={() => { choosing = true; }} disabled={busy} class="underline underline-offset-2 text-dark-text-secondary hover:text-dark-text">Choose another account</button>
          {/if}
          <button type="button" onclick={() => { phase = 'dismissed'; }} disabled={busy} class="underline underline-offset-2 text-dark-text-muted hover:text-dark-text">Not now</button>
        </div>
      {/if}
      {#if error}<p role="alert" class="mt-1.5 text-red-400">{error}</p>{/if}
    {/if}
  </section>
{/if}
