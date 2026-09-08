<script lang="ts">
  import { onMount } from 'svelte';
  import axios from 'axios';
  import { decideMobileAuthRequest, getMobileAuthRequest, isAuthUnauthorized, mobileRequestID, type MobileAuthRequest } from '@/lib/api/auth';
  import { ReauthenticationRequired } from '@/lib/api/session-transport';
  import { storeAuth } from '@/lib/store/auth.svelte';

  interface Props { query: string; enabled: boolean; onlogin: () => void }
  let { query, enabled, onlogin }: Props = $props();
  let request = $state<MobileAuthRequest | null>(null);
  let phase = $state<'loading' | 'ready' | 'sending' | 'finished' | 'error'>('loading');
  let message = $state('');
  let now = $state(Date.now());
  let mounted = false;
  let controller: AbortController;
  let initialHash = '';
  const expired = $derived(request !== null && Date.parse(request.expires_at) <= now);
  const current = () => mounted && window.location.hash === initialHash;

  function fail(error: unknown, deciding: boolean) {
    if (!current()) return;
    if (isAuthUnauthorized(error) || error instanceof ReauthenticationRequired) { onlogin(); return; }
    phase = 'error';
    const status = axios.isAxiosError(error) ? error.response?.status : undefined;
    message = status !== undefined && [400, 404, 409, 410].includes(status)
      ? 'This request has expired, was already used, or is no longer available. Start a new sign-in from your mobile app.'
      : deciding
        ? 'The decision could not be confirmed. It may already have been processed and will not be retried. Return to your mobile app to check or start a new sign-in.'
        : 'Cannot load this request. Check your connection, then reopen the sign-in link from your mobile app.';
  }

  onMount(() => {
    mounted = true;
    initialHash = window.location.hash;
    controller = new AbortController();
    const id = mobileRequestID(query);
    if (!enabled || !id) {
      phase = 'error';
      message = !enabled ? 'Mobile sign-in is unavailable because native authentication is disabled on this server.' : 'This sign-in link is invalid or missing its request. Start a new sign-in from your mobile app.';
    } else {
      void getMobileAuthRequest(id, controller.signal).then(data => {
        if (!current()) return;
        const issuer = new URL('.', document.baseURI).href.replace(/\/$/, '');
        if (!data || data.request_id !== id || data.issuer !== issuer || data.callback_uri !== 'atmobile://auth/callback' || typeof data.device_name !== 'string' || typeof data.remember_me !== 'boolean' || !Number.isFinite(Date.parse(data.expires_at))) throw new Error('Invalid request');
        request = data;
        now = Date.now();
        phase = 'ready';
      }).catch(error => fail(error, false));
    }
    const timer = window.setInterval(() => { now = Date.now(); }, 1000);
    return () => { mounted = false; controller.abort(); window.clearInterval(timer); };
  });

  async function decide(approve: boolean) {
    if (!current() || !enabled || !request || phase !== 'ready') return;
    now = Date.now();
    if (Date.parse(request.expires_at) <= now) return;
    // Lock both actions before awaiting. An aborted/lost response cannot undo a decision.
    phase = 'sending';
    try {
      const callback = await decideMobileAuthRequest(request.request_id, approve, controller.signal);
      if (!current()) return;
      phase = 'finished';
      message = approve ? 'Approval recorded. Your browser was asked to open the mobile app.' : 'Request denied. Your browser was asked to return to the mobile app.';
      try { window.location.assign(callback); } catch { /* No handler is not an authorization failure. */ }
    } catch (error) { fail(error, true); }
  }
</script>

<main class="min-h-full overflow-y-auto bg-gray-50 dark:bg-dark-base px-6 py-12 text-gray-900 dark:text-dark-text">
  <div class="mx-auto w-full max-w-lg">
    <h1 class="text-2xl font-semibold">{phase === 'finished' ? 'Return to your mobile app' : 'Allow mobile sign-in?'}</h1>
    {#if phase === 'loading'}
      <p role="status" class="mt-4 text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">Loading the sign-in request...</p>
    {:else if phase === 'error'}
      <p role="alert" class="mt-4 text-sm leading-6 text-red-700 dark:text-red-300">{message}</p>
    {:else if phase === 'finished'}
      <div role="status" class="mt-4 space-y-4 text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">
        <p>{message}</p>
        <p>If nothing opened, switch to the mobile app yourself. If sign-in did not finish, start a new request there. This page will not send the decision again.</p>
        <p>You can close this page.</p>
      </div>
    {:else if request}
      <p class="mt-3 text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">Approve only if you just started this sign-in in the mobile app you intended to use. Approval lets that app sign in to this server as your account.</p>
      <dl class="mt-6 divide-y divide-gray-200 dark:divide-dark-border border-y border-gray-200 dark:border-dark-border text-sm">
        <div class="py-4"><dt class="text-gray-600 dark:text-dark-text-secondary">Signed in as</dt><dd class="mt-1 font-medium break-all"><bdi>{storeAuth.identity?.name || storeAuth.identity?.subject}</bdi></dd></div>
        <div class="py-4"><dt class="text-gray-600 dark:text-dark-text-secondary">Device name (unverified)</dt><dd class="mt-1 font-medium break-all"><bdi>{request.device_name || 'No device name supplied'}</bdi></dd></div>
        <div class="py-4"><dt class="text-gray-600 dark:text-dark-text-secondary">Server issuer</dt><dd class="mt-1 break-all">{request.issuer}</dd></div>
        <div class="py-4"><dt class="text-gray-600 dark:text-dark-text-secondary">Remember this mobile sign-in</dt><dd class="mt-1">{request.remember_me ? 'Requested: yes (extended session)' : 'Requested: no (standard session)'}</dd></div>
        <div class="py-4"><dt class="text-gray-600 dark:text-dark-text-secondary">Request deadline</dt><dd class="mt-1"><time datetime={request.expires_at}>{new Date(request.expires_at).toLocaleString()}</time></dd></div>
      </dl>
      <p class="mt-5 text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">Any app can create a request and choose its device name. The name is not proof of identity. If you did not start this request, choose Deny. Signing in to this page does not automatically approve it.</p>
      {#if expired}<p role="alert" class="mt-4 text-sm leading-6 text-red-700 dark:text-red-300">This request has expired. Start a new sign-in from your mobile app.</p>{/if}
      <div class="mt-6 flex flex-col gap-3 sm:flex-row" aria-busy={phase === 'sending'}>
        <button type="button" onclick={() => decide(false)} disabled={phase !== 'ready' || expired} class="flex-1 rounded-md border border-gray-300 dark:border-dark-border px-4 py-3 text-sm font-medium hover:bg-gray-100 dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50 disabled:cursor-not-allowed">Deny</button>
        <button type="button" onclick={() => decide(true)} disabled={phase !== 'ready' || expired} class="flex-1 rounded-md bg-accent px-4 py-3 text-sm font-semibold text-gray-950 hover:bg-accent-hover focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50 disabled:cursor-not-allowed">Approve sign-in</button>
      </div>
      {#if phase === 'sending'}<p role="status" class="mt-4 text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">Recording your decision. Do not close this page...</p>{/if}
    {/if}
  </div>
</main>
