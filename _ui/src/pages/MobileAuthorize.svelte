<script lang="ts">
  import AuthShell from '@/lib/components/AuthShell.svelte';
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

<AuthShell
  title={phase === 'finished' ? 'Return to your mobile app' : 'Allow mobile sign-in?'}
  subtitle={phase === 'ready' ? 'Review the request before approving it.' : ''}
  width="lg"
>
  {#if phase === 'loading'}
    <p role="status" class="settings-note">Loading the sign-in request...</p>
  {:else if phase === 'error'}
    <p role="alert" class="settings-error">{message}</p>
  {:else if phase === 'finished'}
    <div role="status" class="space-y-2 text-sm text-gray-600 dark:text-dark-text-secondary leading-relaxed">
      <p>{message}</p>
      <p>If nothing opened, switch to the mobile app yourself. If sign-in did not finish, start a new request there. This page will not send the decision again.</p>
      <p>You can close this page.</p>
    </div>
  {:else if request}
    <p class="text-sm text-gray-600 dark:text-dark-text-secondary leading-relaxed">Approve only if you just started this sign-in in the mobile app you intended to use. Approval lets that app sign in to this server as your account.</p>
    <dl class="settings-list">
      <div class="px-4 py-3"><dt class="settings-note">Signed in as</dt><dd class="mt-0.5 text-sm font-medium break-all"><bdi>{storeAuth.identity?.name || storeAuth.identity?.subject}</bdi></dd></div>
      <div class="px-4 py-3"><dt class="settings-note">Device name (unverified)</dt><dd class="mt-0.5 text-sm font-medium break-all"><bdi>{request.device_name || 'No device name supplied'}</bdi></dd></div>
      <div class="px-4 py-3"><dt class="settings-note">Server issuer</dt><dd class="mt-0.5 text-sm break-all">{request.issuer}</dd></div>
      <div class="px-4 py-3"><dt class="settings-note">Remember this mobile sign-in</dt><dd class="mt-0.5 text-sm">{request.remember_me ? 'Requested: yes (extended session)' : 'Requested: no (standard session)'}</dd></div>
      <div class="px-4 py-3"><dt class="settings-note">Request deadline</dt><dd class="mt-0.5 text-sm"><time datetime={request.expires_at}>{new Date(request.expires_at).toLocaleString()}</time></dd></div>
    </dl>
    <p class="settings-note leading-relaxed">Any app can create a request and choose its device name. The name is not proof of identity. If you did not start this request, choose Deny. Signing in to this page does not automatically approve it.</p>
    {#if expired}<p role="alert" class="settings-error">This request has expired. Start a new sign-in from your mobile app.</p>{/if}
    <!-- Deny stays first, as before: on a confirmation screen the safe choice is
         the one the thumb reaches first. -->
    <div class="flex flex-col gap-2 sm:flex-row" aria-busy={phase === 'sending'}>
      <button type="button" onclick={() => decide(false)} disabled={phase !== 'ready' || expired} class="settings-button flex-1 min-h-11 sm:min-h-0">Deny</button>
      <button type="button" onclick={() => decide(true)} disabled={phase !== 'ready' || expired} class="settings-primary flex-1 min-h-11 sm:min-h-0">Approve sign-in</button>
    </div>
    {#if phase === 'sending'}<p role="status" class="settings-note">Recording your decision. Do not close this page...</p>{/if}
  {/if}
</AuthShell>
