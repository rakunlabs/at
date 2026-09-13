<script lang="ts">
  import { ChevronDown, RefreshCw } from 'lucide-svelte';
  import { authErrorMessage, isAuthUnauthorized, listAuthLoginEvents, type AuthLoginEvent } from '@/lib/api/auth';
  import { returnToLogin } from '@/lib/store/auth.svelte';

  interface Props { userID: string; username: string }
  let { userID, username }: Props = $props();
  let expanded = $state(false);
  let loading = $state(false);
  let events = $state<AuthLoginEvent[]>([]);
  let error = $state('');
  let retention = $state(90);
  const labels: Record<AuthLoginEvent['action'], string> = {
    login_success: 'Signed in', login_failed: 'Incorrect credentials', login_locked: 'Locked after 5 incorrect passwords',
    login_blocked: 'Attempt blocked by password lock', login_unlocked: 'Password sign-in unlocked', sessions_revoked: 'All sessions signed out',
  };
  async function load() {
    if (loading) return;
    loading = true;
    error = '';
    try {
      const result = await listAuthLoginEvents(userID);
      events = result.data;
      retention = result.retention_days;
    } catch (e) {
      if (isAuthUnauthorized(e)) returnToLogin();
      error = authErrorMessage(e, 'Could not load sign-in history. Please retry.');
    } finally { loading = false; }
  }
</script>

<div class="mt-3 text-sm">
  <button type="button" aria-expanded={expanded} aria-controls={`login-history-${userID}`} onclick={() => { expanded = !expanded; if (expanded) void load(); }} class="inline-flex min-h-9 items-center gap-2 rounded px-2 text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent">
    <ChevronDown size={16} class={expanded ? '' : '-rotate-90'} /> Sign-in history
  </button>
  {#if expanded}
    <section id={`login-history-${userID}`} aria-label={`Sign-in history for ${username}`} aria-busy={loading} class="mt-2 border border-gray-200 dark:border-dark-border">
      <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 dark:border-dark-border px-3 py-2">
        <p class="text-xs text-gray-600 dark:text-dark-text-secondary">Latest 50 events · {retention}-day history</p>
        <button type="button" disabled={loading} onclick={load} class="inline-flex min-h-9 items-center gap-2 rounded px-2 text-xs hover:bg-gray-100 dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"><RefreshCw size={14} />Refresh</button>
      </div>
      <p class="px-3 py-2 text-xs leading-5 text-gray-600 dark:text-dark-text-secondary">Source IP is the address AT received the connection from. Behind a reverse proxy, it may be the proxy’s address. Browser information is reported by the client.</p>
      {#if loading}<p role="status" class="p-3 text-gray-600 dark:text-dark-text-secondary">Loading sign-in history…</p>{/if}
      {#if error}<p role="alert" class="p-3 text-red-700 dark:text-red-300">{error}</p>{/if}
      {#if !loading && !error && !events.length}<p class="p-3 text-gray-600 dark:text-dark-text-secondary">No recorded activity. Events are recorded from this update onward.</p>{/if}
      <ol class="divide-y divide-gray-200 dark:divide-dark-border">
        {#each events as event (event.id)}
          <li class="space-y-1 px-3 py-3">
            <div class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
              <span class={['font-medium', ['login_failed', 'login_locked', 'login_blocked'].includes(event.action) ? 'text-red-700 dark:text-red-300' : 'text-gray-900 dark:text-dark-text']}>{labels[event.action] || event.action}</span>
              <time datetime={event.created_at} class="text-xs tabular-nums text-gray-600 dark:text-dark-text-secondary">{new Date(event.created_at).toLocaleString()}</time>
            </div>
            <p class="text-xs text-gray-600 dark:text-dark-text-secondary">Source IP: <span class="font-mono select-text">{event.source_ip || 'Unavailable'}</span></p>
            {#if event.user_agent}<p class="break-all text-xs leading-5 text-gray-600 dark:text-dark-text-secondary">{event.user_agent}</p>{/if}
            {#if event.actor_id}<p class="break-all text-xs text-gray-600 dark:text-dark-text-secondary">Performed by user: <span class="font-mono">{event.actor_id}</span></p>{/if}
          </li>
        {/each}
      </ol>
    </section>
  {/if}
</div>
