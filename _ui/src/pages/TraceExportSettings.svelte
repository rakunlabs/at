<script lang="ts">
  import { onMount } from 'svelte';
  import { Loader2, Plus, Trash2, CheckCircle2, XCircle } from 'lucide-svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { workspaceState } from '@/lib/store/workspace.svelte';
  import { getTraceExportSettings, saveTraceExportSettings, testTraceExport, type TraceExportSettings, type TraceExportTestResult } from '@/lib/api/trace-export';

  storeNavbar.title = 'Trace export';
  let settings = $state<TraceExportSettings | null>(null);
  let headers = $state<Array<{ name: string; value: string }>>([]);
  let loading = $state(true);
  let saving = $state(false);
  let testing = $state(false);
  let error = $state('');
  let notice = $state('');
  let conflict = $state(false);
  let result = $state<TraceExportTestResult | null>(null);
  let busy = $derived(loading || saving || testing);
  let workspaceName = $derived(workspaceState.items.find(w => w.id === workspaceState.access?.workspace_id)?.name || 'Selected workspace');
  let endpointHint = $derived(settings?.target === 'langfuse'
    ? 'https://cloud.langfuse.com/api/public/otel/v1/traces'
    : settings?.protocol === 'grpc' ? 'http://otel-collector:4317' : 'http://otel-collector:4318/v1/traces');

  function message(e: any, fallback: string) { return e?.response?.data?.message || fallback; }
  function adopt(s: TraceExportSettings) {
    settings = s;
    headers = Object.entries(s.headers || {}).map(([name, value]) => ({ name, value }));
  }
  function edited() { result = null; notice = ''; }
  function targetChanged() {
    if (settings?.target === 'langfuse') settings.protocol = 'http';
    edited();
  }
  function payload(): TraceExportSettings {
    if (!settings) throw new Error('Settings have not loaded.');
    const values: Record<string, string> = {};
    const seen = new Set<string>();
    for (const row of headers) {
      const name = row.name.trim();
      if (!name) throw new Error('Enter a name for every header or remove the empty row.');
      if (seen.has(name.toLowerCase())) throw new Error('Header names must be unique.');
      seen.add(name.toLowerCase());
      values[name] = row.value;
    }
    return { ...settings, endpoint: settings.endpoint.trim(), headers: values };
  }
  async function load() {
    loading = true;
    error = notice = '';
    result = null;
    try { adopt(await getTraceExportSettings()); conflict = false; }
    catch (e) { error = message(e, 'Could not load trace export settings. Retry to continue.'); }
    finally { loading = false; }
  }
  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (busy || !settings) return;
    error = notice = '';
    let data: TraceExportSettings;
    try { data = payload(); } catch (e) { error = (e as Error).message; return; }
    saving = true;
    try {
      adopt(await saveTraceExportSettings(data));
      conflict = false;
      notice = data.enabled ? 'Saved. New observations from this workspace will be exported.' : 'Saved. Trace export is disabled for this workspace.';
    } catch (e: any) {
      conflict = e?.response?.status === 409;
      error = message(e, 'Could not save trace export settings. Try again.');
    } finally { saving = false; }
  }
  async function test() {
    if (busy || !settings) return;
    error = notice = '';
    result = null;
    let data: TraceExportSettings;
    try { data = payload(); } catch (e) { error = (e as Error).message; return; }
    testing = true;
    try { result = await testTraceExport(data); }
    catch (e: any) {
      conflict = e?.response?.status === 409;
      error = message(e, 'Connection test could not complete. Check the server connection and retry.');
    } finally { testing = false; }
  }
  onMount(() => { void load(); });
</script>

<svelte:head><title>AT | Trace export</title></svelte:head>
<div class="settings-page">
  <header>
    <h1 class="settings-title">Trace export</h1>
    <p class="settings-subtitle">Send {workspaceName} observations to your OpenTelemetry collector or Langfuse project.</p>
  </header>

  {#if error}
    <div role="alert" class="settings-error">
      <p>{error}</p>
      {#if !settings || conflict}<button type="button" class="settings-button mt-2" disabled={busy} onclick={load}>{conflict ? 'Reload saved settings' : 'Retry'}</button>{/if}
    </div>
  {/if}
  {#if loading}<p class="settings-note" role="status">Loading trace export settings…</p>{/if}

  {#if settings}
    <form onsubmit={save} oninput={edited} onchange={edited} class="settings-form">
      <fieldset disabled={busy} class="min-w-0 border-0 p-0 space-y-4">
        <section class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
          <div class="px-4 py-3 bg-gray-50 dark:bg-dark-base border-b border-gray-200 dark:border-dark-border"><h2 class="settings-section-title">Destination</h2></div>
          <div class="p-4 space-y-4">
            <label class="flex items-start gap-2 text-sm">
              <input type="checkbox" bind:checked={settings.enabled} class="mt-0.5" />
              <span>Enable trace export<span class="settings-note block mt-1">Applies only to this workspace. Existing AT trace records are kept.</span></span>
            </label>
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <label class="settings-label">Target
                <select class="settings-input" bind:value={settings.target} onchange={targetChanged}><option value="collector">OpenTelemetry collector</option><option value="langfuse">Langfuse</option></select>
              </label>
              <label class="settings-label">Protocol
                <select class="settings-input" bind:value={settings.protocol} disabled={settings.target === 'langfuse'}><option value="http">OTLP HTTP / protobuf</option><option value="grpc">OTLP gRPC</option></select>
              </label>
            </div>
            <label class="settings-label">Endpoint
              <input class="settings-input" type="url" bind:value={settings.endpoint} placeholder={endpointHint} required={settings.enabled} spellcheck="false" />
              <span class="settings-note">{settings.protocol === 'grpc' ? 'Use http:// for plaintext or https:// for TLS. Include the port; no URL path.' : 'Enter the complete trace endpoint URL, including /v1/traces.'}</span>
            </label>
            {#if settings.target === 'langfuse'}
              <p class="settings-note break-all">Example: {endpointHint}. Use your region or self-hosted address. Langfuse supports HTTP, not gRPC.</p>
              <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
                <label class="settings-label">Public key<input class="settings-input" bind:value={settings.public_key} placeholder="pk-lf-…" autocomplete="off" spellcheck="false" /></label>
                <label class="settings-label">Secret key<input class="settings-input" type="password" bind:value={settings.secret_key} placeholder="sk-lf-…" autocomplete="new-password" /></label>
              </div>
              <p class="settings-note">A value of *** keeps the stored secret. Replace it to update, or clear it to remove.</p>
            {/if}
            <div class="space-y-2">
              <div class="flex flex-wrap items-center justify-between gap-2"><h3 class="text-sm font-medium">Additional headers</h3><button type="button" class="settings-button min-h-11 sm:min-h-0" disabled={headers.length >= 16} onclick={() => { headers.push({ name: '', value: '' }); edited(); }}><Plus size={14} /> Add header</button></div>
              <p class="settings-note">For example, Authorization with Bearer credentials. Values are hidden after saving; *** keeps a saved value.</p>
              {#each headers as row, i}
                <div class="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] items-end gap-2">
                  <label class="settings-label">Name<input class="settings-input" bind:value={row.name} placeholder="Authorization" autocomplete="off" spellcheck="false" /></label>
                  <label class="settings-label">Value<input class="settings-input" type="password" bind:value={row.value} placeholder="Bearer …" autocomplete="new-password" /></label>
                  <button type="button" class="settings-button min-h-11 sm:min-h-0" aria-label={`Remove header ${i + 1}`} onclick={() => { headers.splice(i, 1); edited(); }}><Trash2 size={14} /></button>
                </div>
              {/each}
            </div>
          </div>
        </section>
        <section class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
          <div class="px-4 py-3 bg-gray-50 dark:bg-dark-base border-b border-gray-200 dark:border-dark-border"><h2 class="settings-section-title">Trace content</h2></div>
          <div class="p-4 space-y-3">
            <p class="settings-note">Exports new model calls, tool observations and lifecycle events, including timing, token counts, cost and trace relationships.</p>
            <label class="flex items-start gap-2 text-sm"><input type="checkbox" class="mt-0.5" bind:checked={settings.include_content} /><span>Include prompts, responses and tool content<span class="settings-note block mt-1">Requires the installation’s full-body capture feature to be enabled. Each content field is limited to 16 KiB.</span></span></label>
            <p class="settings-note">Delivery runs in the background with a bounded queue and retry. Historical records are not backfilled; failed deliveries and queue overflow are reported in server logs.</p>
          </div>
        </section>
        <div class="flex flex-wrap items-center gap-2">
          <button type="submit" class="settings-primary min-h-11 sm:min-h-0" disabled={conflict}>{saving ? 'Saving…' : 'Save settings'}</button>
          <button type="button" class="settings-button min-h-11 sm:min-h-0" disabled={!settings.endpoint.trim() || conflict} onclick={test}>{#if testing}<Loader2 size={14} class="animate-spin motion-reduce:animate-none" />{/if}{testing ? 'Testing connection…' : 'Test connection'}</button>
        </div>
      </fieldset>
      <p class="settings-note">Test connection sends a synthetic trace using the values above, even before saving or enabling export. It does not change your saved settings.</p>
    </form>
  {/if}
  {#if notice}<p role="status" class="text-sm text-green-700 dark:text-green-400">{notice}</p>{/if}
  {#if result}
    <div role="status" class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-4 space-y-2">
      <p class="flex items-center gap-2 text-sm font-medium">{#if result.ok}<CheckCircle2 size={16} class="text-green-700 dark:text-green-400 shrink-0" />Receiver accepted the test trace{:else}<XCircle size={16} class="text-red-600 dark:text-red-400 shrink-0" />Connection test failed{/if}</p>
      <p class="settings-note">{result.message}</p>
      <p class="settings-note">Duration: {result.duration_ms} ms</p>
      <p class="settings-note break-all">Trace ID: <code>{result.trace_id}</code></p>
    </div>
  {/if}
</div>
