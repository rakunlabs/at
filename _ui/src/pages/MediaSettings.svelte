<script lang="ts">
  import { onMount } from 'svelte';
  import { CheckCircle2, Loader2, XCircle } from 'lucide-svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { isNativeAdmin } from '@/lib/store/auth.svelte';
  import {
    MEDIA_ALLOWED_LABEL,
    emptyMediaSettings,
    getMediaSettings,
    isMediaSettingsConflict,
    mediaErrorMessage,
    mediaSettingsErrorMessage,
    putMediaSettings,
    testMediaSettings,
    type MediaBackend,
    type MediaSettings,
  } from '@/lib/api/media';

  storeNavbar.title = 'Media storage';

  let settings = $state<MediaSettings | null>(null);
  let busy = $state(false);
  let testing = $state(false);
  let error = $state('');
  let notice = $state('');
  /** A 409 means the stored version moved on; offer a reload, never clobber. */
  let conflict = $state(false);
  let testResult = $state<{ ok: boolean; message: string } | null>(null);

  /** The secret is write-only: the server always returns `''`. */
  let secretInput = $state('');
  let replacingSecret = $state(false);
  let secretEditable = $derived(!settings?.secret_access_key_set || replacingSecret);

  /** Client-side guard for the shapes the server rejects with a 400. */
  function checkForm(s: MediaSettings): string {
    if (s.backend === 'filesystem') {
      const root = s.filesystem.root.trim();
      if (!root) return 'Enter the folder the images are written to.';
      if (!root.startsWith('/')) return 'The folder must be an absolute path, starting with "/".';
      return '';
    }
    if (s.backend === 's3') {
      if (!s.s3.endpoint.trim()) return 'Enter the S3 endpoint URL.';
      if (!/^https?:\/\//i.test(s.s3.endpoint.trim())) return 'The endpoint must start with http:// or https://.';
      if (!s.s3.bucket.trim()) return 'Enter the bucket name.';
      return '';
    }
    return '';
  }

  let problem = $derived(settings ? checkForm(settings) : '');

  /** The document to submit: the typed secret, or `''` to keep the stored one. */
  function formSettings(): MediaSettings {
    const s = settings!;
    return { ...s, s3: { ...s.s3, secret_access_key: secretEditable ? secretInput : '' } };
  }

  async function load() {
    busy = true;
    error = notice = '';
    conflict = false;
    testResult = null;
    try {
      settings = await getMediaSettings();
      secretInput = '';
      replacingSecret = false;
    } catch (e) {
      // A never-configured installation still deserves a usable form.
      settings ??= emptyMediaSettings();
      error = mediaErrorMessage(e, 'Could not load media storage settings. Retry when the server is available.');
    } finally {
      busy = false;
    }
  }

  onMount(() => {
    if (isNativeAdmin()) void load();
    return () => { secretInput = ''; };
  });

  function selectBackend(value: string) {
    if (!settings) return;
    settings.backend = value as MediaBackend;
    testResult = null;
    notice = error = '';
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    if (!settings || busy) return;
    const invalid = checkForm(settings);
    if (invalid) { error = invalid; return; }
    busy = true;
    error = notice = '';
    conflict = false;
    try {
      settings = await putMediaSettings(formSettings());
      secretInput = '';
      replacingSecret = false;
      notice = settings.backend
        ? 'Media storage settings saved. New Playground images are stored from now on.'
        : 'Media storage is now disabled. New Playground images will not be saved to conversation history.';
    } catch (err) {
      conflict = isMediaSettingsConflict(err);
      error = mediaSettingsErrorMessage(err);
    } finally {
      busy = false;
    }
  }

  /** Probes the submitted values, so it works before the first save. */
  async function runTest() {
    if (!settings || testing) return;
    const invalid = checkForm(settings);
    if (invalid) { testResult = { ok: false, message: invalid }; return; }
    testing = true;
    testResult = null;
    try {
      const res = await testMediaSettings(formSettings());
      testResult = { ok: res?.ok === true, message: res?.message || '' };
    } catch (err) {
      testResult = { ok: false, message: mediaErrorMessage(err, mediaSettingsErrorMessage(err)) };
    } finally {
      testing = false;
    }
  }
</script>

<svelte:head><title>AT | Media storage</title></svelte:head>

<div class="settings-page settings-form">
  <header>
    <h1 class="text-2xl font-semibold">Media storage</h1>
    <p class="settings-note mt-2">
      Where Playground image attachments are kept so they survive a reload. Without a backend, an image is sent to
      the model but only a placeholder is written to conversation history.
    </p>
  </header>

  {#if !isNativeAdmin()}
    <p class="settings-note">Only installation administrators can configure media storage.</p>
  {:else}
    {#if error}
      <p role="alert" class="settings-error">
        {error}
        {#if conflict}
          <button type="button" class="settings-button ml-2 align-middle" disabled={busy} onclick={load}>Reload saved settings</button>
        {/if}
      </p>
    {/if}
    {#if notice}<p role="status" class="settings-note">{notice}</p>{/if}

    <button class="settings-button" disabled={busy} onclick={load}>{busy ? 'Working…' : 'Reload settings'}</button>

    {#if settings}
      <form class="settings-section space-y-5" onsubmit={save}>
        <fieldset disabled={!isNativeAdmin() || busy} class="space-y-5">
          <h2 class="text-lg font-semibold">Backend</h2>
          <label>
            Storage backend
            <select value={settings.backend} onchange={e => selectBackend(e.currentTarget.value)}>
              <option value="">Disabled — do not store images</option>
              <option value="filesystem">Local folder on the server</option>
              <option value="s3">S3-compatible object storage</option>
            </select>
          </label>

          {#if settings.backend === ''}
            <p class="settings-note">
              Media storage is off. Playground images are still sent to the model for the turn you send them in, but
              conversation history keeps only a “not saved to history” placeholder, so reopening the conversation — or
              re-sending it to a model — will not include the image. Pick a backend to keep them.
            </p>
          {:else if settings.backend === 'filesystem'}
            <label>
              Folder
              <input
                bind:value={settings.filesystem.root}
                placeholder="/var/lib/at/media"
                spellcheck="false"
                autocomplete="off"
                aria-describedby="media-root-help"
              />
            </label>
            <p class="settings-note" id="media-root-help">
              Must be an <strong>absolute path</strong> that is writable by the user the AT service runs as. A relative
              path is resolved against the process working directory, which changes with how the service is started —
              so images could land somewhere you did not expect, or fail to write at all. Put the folder on a volume
              that persists across restarts and is included in your backups.
            </p>
          {:else}
            <div class="grid sm:grid-cols-2 gap-5">
              <label>
                Endpoint URL
                <input bind:value={settings.s3.endpoint} type="url" placeholder="https://s3.us-east-1.amazonaws.com" spellcheck="false" autocomplete="off" />
              </label>
              <label>
                Region
                <input bind:value={settings.s3.region} placeholder="us-east-1" spellcheck="false" autocomplete="off" />
              </label>
              <label>
                Bucket
                <input bind:value={settings.s3.bucket} placeholder="at-media" spellcheck="false" autocomplete="off" />
              </label>
              <label>
                Key prefix
                <input bind:value={settings.s3.prefix} placeholder="playground/" spellcheck="false" autocomplete="off" />
              </label>
              <label>
                Access key ID
                <input bind:value={settings.s3.access_key_id} spellcheck="false" autocomplete="off" />
              </label>
              <label>
                Secret access key
                {#if secretEditable}
                  <input
                    bind:value={secretInput}
                    type="password"
                    autocomplete="new-password"
                    placeholder={settings.secret_access_key_set ? 'Enter the replacement secret' : 'Enter the secret access key'}
                    aria-describedby="media-secret-help"
                  />
                {:else}
                  <input value="•••••• (stored)" readonly aria-describedby="media-secret-help" />
                {/if}
              </label>
            </div>

            <p class="settings-note" id="media-secret-help">
              {#if settings.secret_access_key_set}
                A secret access key is stored. It is never sent back to this page, so it cannot be shown or copied.
                {#if secretEditable}
                  Leave this field <strong>blank</strong> to keep the stored secret; type a new value to replace it.
                {:else}
                  Saving keeps the stored secret unchanged.
                {/if}
              {:else}
                No secret access key is stored yet.
              {/if}
            </p>
            {#if settings.secret_access_key_set}
              <button
                type="button"
                class="settings-button"
                onclick={() => { replacingSecret = !replacingSecret; secretInput = ''; }}
              >
                {replacingSecret ? 'Keep the stored secret' : 'Replace stored secret'}
              </button>
            {/if}

            <label><input type="checkbox" bind:checked={settings.s3.use_path_style} />Use path-style URLs</label>
            <p class="settings-note">
              Path-style addressing (<code class="font-mono">endpoint/bucket/key</code>) is required for MinIO and
              Cloudflare R2, and is the safer default for self-hosted gateways. Leave it off for AWS S3, which uses
              virtual-hosted addressing. Any S3-compatible provider works — Backblaze B2, Wasabi, Ceph, DigitalOcean
              Spaces — as long as the endpoint, region and credentials match what that provider expects.
            </p>
          {/if}

          <p class="settings-note">
            Uploads are capped at 16 MB per image and must be {MEDIA_ALLOWED_LABEL}; the type is detected from the
            file contents, not its name. Configuration version {settings.version}.
          </p>

          <div class="flex flex-wrap items-center gap-3">
            <button class="settings-primary" disabled={busy || !!problem}>Save media storage settings</button>
            <button type="button" class="settings-button" disabled={testing || !settings.backend} onclick={runTest}>
              {testing ? 'Testing…' : 'Test connection'}
            </button>
            {#if problem}<span class="settings-error">{problem}</span>{/if}
          </div>

          <div aria-live="polite" class="min-h-6">
            {#if testing}
              <p class="settings-note flex items-center gap-2"><Loader2 size={14} class="animate-spin shrink-0" />Testing the settings in this form…</p>
            {:else if testResult?.ok}
              <p class="settings-note flex items-center gap-2 text-green-700 dark:text-green-300">
                <CheckCircle2 size={14} class="shrink-0" />Connection succeeded. These settings are not saved yet.
              </p>
            {:else if testResult}
              <p class="settings-error flex items-start gap-2">
                <XCircle size={14} class="shrink-0 mt-1" />
                <span>Connection failed: <span class="break-all">{testResult.message || 'the server did not report a reason.'}</span></span>
              </p>
            {/if}
          </div>

          <p class="settings-note">
            Test connection probes the values currently in this form without saving them, so credentials can be
            verified before the first save.
          </p>
        </fieldset>
      </form>
    {/if}
  {/if}
</div>
