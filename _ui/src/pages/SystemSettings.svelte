<script lang="ts">
  import { onMount } from 'svelte';
  import { storeInfo, storeNavbar } from '../lib/store/store.svelte';
  import { rotateKey } from '../lib/api/admin';
  import { isNativeAdmin } from '../lib/store/auth.svelte';
  import { authErrorMessage } from '../lib/api/auth';
  import { getSystemSettings, saveSystemSettings, type SystemSettings, type SystemSettingsResponse, type KubernetesSandbox } from '../lib/api/system-settings';
  let key = $state(''); let confirm = $state(''); let error = $state(''); let notice = $state(''); let busy = $state(false);
  storeNavbar.title = 'System';
  let settings = $state<SystemSettings | null>(null);
  let active = $state<SystemSettings | null>(null);
  let loading = $state(false);
  let saving = $state(false);
  let settingsError = $state('');
  let settingsNotice = $state('');
  let applyError = $state('');
  let runtimeNotice = $state('');
  let confirmStop = $state(false);
  let singleReplica = $state(false);
  let blockedCIDRs = $state('');
  const defaults: KubernetesSandbox = { namespace: '', deployment_id: '', kubeconfig: '', helper_image: '', storage_class: '', home_storage_class: '', home_access_mode: 'ReadWriteMany', home_size: '5Gi', workspace_size: '20Gi', runtime_class: '', pod_pids_limit: 128, network_policy_enforced: false, single_replica: false, blocked_cidrs: [] };
  let kubernetes = $state<KubernetesSandbox>(structuredClone(defaults));
  const kubeFields = [
    { key: 'namespace', label: 'Dedicated namespace', required: true, placeholder: 'at-sandboxes' },
    { key: 'deployment_id', label: 'Deployment ID', required: true, placeholder: 'at-prod' },
    { key: 'helper_image', label: 'Helper image', required: true, placeholder: 'registry.example.com/at-sandbox-helper:latest' },
    { key: 'kubeconfig', label: 'Kubeconfig file on the AT host', required: false, placeholder: 'Empty uses the in-cluster service account' },
    { key: 'storage_class', label: 'Workspace storage class', required: false, placeholder: 'Empty uses the cluster default' },
    { key: 'home_storage_class', label: 'Home storage class', required: false, placeholder: 'Empty uses the cluster default' },
    { key: 'workspace_size', label: 'Workspace volume size', required: true, placeholder: '20Gi' },
    { key: 'home_size', label: 'Home volume size', required: true, placeholder: '5Gi' },
    { key: 'runtime_class', label: 'Runtime class', required: false, placeholder: 'Optional' },
  ] as const;
  let sandboxChanged = $derived(!!settings && (!!applyError || JSON.stringify(settings.sandbox) !== JSON.stringify(active?.sandbox) || (settings.sandbox.backend === 'kubernetes' && (JSON.stringify(kubernetes) !== JSON.stringify(active?.sandbox.kubernetes) || blockedCIDRs !== (active?.sandbox.kubernetes?.blocked_cidrs || []).join('\n')))));

  function adopt(data: SystemSettingsResponse) {
    settings = data.settings;
    active = data.active;
    kubernetes = structuredClone(data.settings.sandbox.kubernetes || defaults);
    blockedCIDRs = kubernetes.blocked_cidrs.join('\n');
    applyError = data.apply_error || '';
    runtimeNotice = data.runtime.notice || '';
    confirmStop = singleReplica = false;
    storeInfo.name = data.active.name;
  }
  async function load() {
    loading = true; settingsError = settingsNotice = '';
    try { adopt(await getSystemSettings()); }
    catch (e) { settingsError = authErrorMessage(e, 'System settings are unavailable. Retry when the server is available.'); }
    finally { loading = false; }
  }
  async function save(e: SubmitEvent) {
    e.preventDefault(); if (!settings || saving) return;
    saving = true; settingsError = settingsNotice = '';
    const next = structuredClone($state.snapshot(settings));
    next.sandbox = next.sandbox.backend === 'kubernetes'
      ? { backend: 'kubernetes', kubernetes: { ...$state.snapshot(kubernetes), blocked_cidrs: blockedCIDRs.split('\n').map(line => line.trim()).filter(Boolean) } }
      : { backend: 'docker' };
    try {
      adopt(await saveSystemSettings(next, confirmStop, singleReplica));
      settingsNotice = 'System settings saved and applied. No AT restart is required. Retention changes apply to the next hourly cleanup.';
    } catch (e: any) {
      if (e?.response?.data?.settings && e?.response?.data?.active) {
        // The apply-error banner already explains a saved-but-not-applied change.
        adopt(e.response.data);
        settingsError = '';
      } else {
        settingsError = authErrorMessage(e, 'Could not apply system settings. Reload to check saved and active settings before retrying.');
      }
    } finally { saving = false; }
  }
  onMount(() => { if (isNativeAdmin()) void load(); });
  async function rotate(e: SubmitEvent) { e.preventDefault(); error = notice = ''; if (!key || key !== confirm) { error = 'Enter matching encryption passphrases.'; return; } busy = true; try { await rotateKey(key); notice = 'Encryption key rotated.'; } catch { error = 'Key rotation failed. Check server availability and installation administrator access.'; } finally { busy = false; key = confirm = ''; } }
</script>
<svelte:head><title>AT | System</title></svelte:head>
<div class="settings-page settings-form"><header><h1 class="settings-title">System</h1><p class="settings-subtitle">Installation-wide server settings, sandbox runtime and encryption management.</p></header>
  <dl class="grid sm:grid-cols-2 gap-3">{#each [['Store',storeInfo.store_type],['Assets root',storeInfo.assets_root]] as [label,value]}<div><dt class="settings-note">{label}</dt><dd class="mt-1 break-all">{value || 'Unavailable'}</dd></div>{/each}</dl>
  {#if isNativeAdmin()}
    <section class="settings-section space-y-4" aria-labelledby="system-settings-title">
      <div class="flex flex-wrap items-center justify-between gap-3"><h2 id="system-settings-title" class="settings-section-title">Server and sandbox settings</h2><button type="button" class="settings-button" disabled={loading || saving} onclick={load}>{loading ? 'Loading…' : 'Reload settings'}</button></div>
      <p class="settings-note">Saved in PostgreSQL. Changes apply to this AT server immediately. Other replicas must be restarted to load the saved values; live sandbox changes require one AT replica.</p>
      {#if settingsError}<p role="alert" class="settings-error">{settingsError}</p>{/if}
      {#if settingsNotice}<p role="status" class="settings-note">{settingsNotice}</p>{/if}
      {#if applyError}<p role="alert" class="settings-error">Sandbox execution is unavailable. {applyError}</p>{/if}
      {#if settings && active}
        <form class="space-y-5" onsubmit={save}>
          <fieldset disabled={loading || saving} class="space-y-5">
            <div class="grid gap-3 sm:grid-cols-2">
              <label>Server name<input bind:value={settings.name} required maxlength="120" /><span class="settings-note">The application name reported by the server. The sign-in title is configured separately in Authentication.</span></label>
              <label>Public URL<input type="url" bind:value={settings.external_url} placeholder="https://at.example.com" spellcheck="false" /><span class="settings-note">Origin only, without a path. Used for public links and bot OAuth. The deployment base path is added automatically; this does not change sign-in addresses.</span></label>
              <label>Log level<select bind:value={settings.log_level}><option value="debug">Debug</option><option value="info">Info</option><option value="warn">Warning</option><option value="error">Error</option></select></label>
              <label>Workspace retention (hours)<input type="number" min="-1" max="87600" step="1" bind:value={settings.workspace_ttl_hours} required /><span class="settings-note">Default: 24 hours. Use -1 to disable cleanup, or 0 for the default. Applies only to terminal task workspaces and tool-output dumps, never persistent assets or developer volumes. Reducing it can delete older workspaces at the next cleanup.</span></label>
            </div>
            <div class="border-t border-dark-border pt-4 space-y-3">
              <h3 class="settings-subsection-title">Sandbox runtime</h3>
              <p class="settings-note">Active backend: {active.sandbox.backend}. Changing backends or Kubernetes settings interrupts coding turns, isolated agent runs, commands and terminals. Existing volumes stay on their original backend; AT does not migrate projects, homes or packages.</p>
              {#if runtimeNotice}<p class="settings-note">{runtimeNotice}</p>{/if}
              <label>Backend<select bind:value={settings.sandbox.backend}><option value="docker">Docker</option><option value="kubernetes">Kubernetes (experimental)</option></select></label>
              {#if settings.sandbox.backend === 'docker'}
                <p class="settings-note">Uses the Docker CLI and the host's Docker context or DOCKER_HOST. AT does not install Docker or change daemon settings.</p>
              {:else}
                <p class="settings-note">Requires a provisioned cluster, dedicated namespace, helper image, RBAC, storage and a CNI that enforces NetworkPolicy. These declarations do not configure the cluster. Kubernetes is supported with one AT replica only.</p>
                <div class="grid gap-3 sm:grid-cols-2">
                  {#each kubeFields as field}<label>{field.label}<input bind:value={kubernetes[field.key]} required={field.required} placeholder={field.placeholder} spellcheck="false" /></label>{/each}
                  <label>Home access mode<select bind:value={kubernetes.home_access_mode}><option value="ReadWriteMany">ReadWriteMany</option><option value="ReadWriteOnce">ReadWriteOnce</option></select></label>
                  <label>Kubelet pod PID limit<input type="number" min="1" step="1" bind:value={kubernetes.pod_pids_limit} required /><span class="settings-note">The actual podPidsLimit configured on every sandbox node, not a limit AT sets.</span></label>
                </div>
                <label>Additional blocked CIDRs<textarea rows="3" bind:value={blockedCIDRs} spellcheck="false" placeholder="10.0.0.0/8"></textarea><span class="settings-note">One CIDR per line. Cluster and private-service networks to block from sandbox egress.</span></label>
                <label class="flex items-start gap-2"><input type="checkbox" bind:checked={kubernetes.single_replica} required /><span>I have configured this deployment to run one AT replica.</span></label>
                <label class="flex items-start gap-2"><input type="checkbox" bind:checked={kubernetes.network_policy_enforced} required /><span>I have verified that the cluster CNI enforces NetworkPolicy.</span></label>
              {/if}
              {#if sandboxChanged}
                <div class="border border-dark-border p-3 space-y-3">
                  <p class="settings-note">Applying this change stops sandbox activity before switching. A failed drain blocks new sandbox work until a successful retry. Stopped environments can be started again, but Kubernetes root filesystems and installed packages are recreated.</p>
                  <label class="flex items-start gap-2"><input type="checkbox" bind:checked={singleReplica} required /><span>Only one AT server replica is running during this change.</span></label>
                  <label class="flex items-start gap-2"><input type="checkbox" bind:checked={confirmStop} required /><span>Stop active sandbox work and apply these settings now.</span></label>
                </div>
              {/if}
            </div>
            <button class="settings-primary min-h-11 sm:min-h-0" disabled={sandboxChanged && (!confirmStop || !singleReplica)}>{saving ? 'Applying…' : sandboxChanged ? 'Save, stop sandbox work & apply' : 'Save & apply settings'}</button>
          </fieldset>
        </form>
      {:else}<p role="status" class="settings-note">{loading ? 'Loading system settings…' : 'System settings could not be loaded. Use Reload settings to retry.'}</p>{/if}
    </section>
  {/if}
  {#if isNativeAdmin()}<section class="settings-section"><h2 class="settings-section-title">Rotate encryption key</h2><p class="settings-note">Re-encrypt stored credentials with a new passphrase. Your installation administrator session authorizes this operation.</p><form class="space-y-4 max-w-lg" onsubmit={rotate}><label>New encryption passphrase<input type="password" bind:value={key} required autocomplete="new-password" /></label><label>Confirm passphrase<input type="password" bind:value={confirm} required autocomplete="new-password" /></label><button class="settings-primary" disabled={busy}>{busy ? 'Rotating…' : 'Rotate encryption key'}</button></form></section>{/if}
  {#if error}<p role="alert" class="settings-error">{error}</p>{/if}{#if notice}<p role="status" class="settings-note">{notice}</p>{/if}
</div>
