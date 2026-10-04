<script lang="ts">
  import { X } from 'lucide-svelte';
  import { saveLocalChatProviders, type LocalChatProvider } from '@/lib/api/local-providers';
  import { REDACTED } from '@/lib/helper/local-mcp';
  import { localProviderNameProblem, localProviderUrlProblem } from '@/lib/helper/local-providers';

  interface Props {
    /** Record being edited; absent for a new provider. */
    provider?: LocalChatProvider;
    providers: LocalChatProvider[];
    /** Receives the stored list after a successful save. */
    onsaved: (providers: LocalChatProvider[], edited?: LocalChatProvider) => void;
    oncancel: () => void;
  }

  let { provider, providers, onsaved, oncancel }: Props = $props();

  // Seeded once: the parent remounts this editor per record.
  // svelte-ignore state_referenced_locally
  let name = $state(provider?.name ?? '');
  // svelte-ignore state_referenced_locally
  let baseUrl = $state(provider?.base_url ?? '');
  // Arrives as the sentinel when stored; replaying it preserves the key.
  // svelte-ignore state_referenced_locally
  let apiKey = $state(provider?.api_key ?? '');
  // svelte-ignore state_referenced_locally
  let headers = $state<Record<string, string>>({ ...(provider?.headers ?? {}) });
  let headerKey = $state('');
  let headerValue = $state('');
  let error = $state('');
  let saving = $state(false);

  function addHeader() {
    const key = headerKey.trim();
    if (!key) return;
    headers = { ...headers, [key]: headerValue };
    headerKey = '';
    headerValue = '';
  }

  function removeHeader(key: string) {
    const next = { ...headers };
    delete next[key];
    headers = next;
  }

  async function save() {
    const problem = localProviderNameProblem(name) || localProviderUrlProblem(baseUrl);
    if (problem) {
      error = problem;

      return;
    }

    const id = provider?.id ?? '';
    const entry: LocalChatProvider = {
      id,
      name: name.trim().toLowerCase(),
      base_url: baseUrl.trim(),
      api_key: apiKey.trim() || undefined,
      headers: Object.keys(headers).length > 0 ? headers : undefined,
    };
    const next = id ? providers.map(p => (p.id === id ? entry : p)) : [...providers, entry];

    saving = true;
    try {
      const stored = await saveLocalChatProviders(next);
      onsaved(stored, id ? stored.find(p => p.id === id) : undefined);
    } catch (e: any) {
      error = e?.response?.data?.message || 'Failed to save';
    } finally {
      saving = false;
    }
  }
</script>

<div class="mt-1.5 border border-gray-300 dark:border-dark-border-subtle p-2.5 space-y-2">
  <div class="grid grid-cols-4 gap-2 items-center">
    <label class="contents">
      <span class="text-xs text-gray-600 dark:text-dark-text-secondary">Name</span>
      <input bind:value={name} placeholder="ollama" class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs dark:bg-dark-elevated dark:text-dark-text" />
    </label>
    <label class="contents">
      <span class="text-xs text-gray-600 dark:text-dark-text-secondary">Base URL</span>
      <input bind:value={baseUrl} placeholder="http://127.0.0.1:11434/v1" class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text" />
    </label>
    <div class="col-start-2 col-span-3 text-[10px] text-gray-400 dark:text-dark-text-muted">
      OpenAI-compatible API root. Your browser calls <code>/models</code> and <code>/chat/completions</code> under it directly; plain http only for local addresses.
    </div>
    <label class="contents">
      <span class="text-xs text-gray-600 dark:text-dark-text-secondary">API key</span>
      {#if apiKey === REDACTED}
        <div class="col-span-3 flex items-center gap-2">
          <span class="text-[11px] text-gray-500 dark:text-dark-text-muted">Stored</span>
          <button onclick={() => (apiKey = '')} class="px-2 py-0.5 text-[10px] border border-gray-300 dark:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted">Replace or remove</button>
        </div>
      {:else}
        <input bind:value={apiKey} type="password" placeholder="optional" autocomplete="off" class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text" />
      {/if}
    </label>
  </div>

  <div class="grid grid-cols-4 gap-2 items-start">
    <span class="text-xs text-gray-600 dark:text-dark-text-secondary pt-1">Headers</span>
    <div class="col-span-3 space-y-1">
      {#each Object.entries(headers) as [hk, hv]}
        <div class="flex items-center gap-1">
          <span class="text-[10px] font-mono text-gray-600 dark:text-dark-text-secondary">{hk}:</span>
          <span class="text-[10px] font-mono text-gray-400 dark:text-dark-text-muted truncate">{hv === REDACTED ? 'stored' : '••••'}</span>
          <button
            onclick={() => removeHeader(hk)}
            class="ml-auto p-0.5 text-gray-400 hover:text-red-500"
            aria-label={`Remove header ${hk}`}
          ><X size={10} /></button>
        </div>
      {/each}
      <div class="flex items-center gap-1">
        <input bind:value={headerKey} placeholder="X-Header" class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-[11px] font-mono dark:bg-dark-elevated dark:text-dark-text" />
        <input bind:value={headerValue} placeholder="value" type="password" class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-[11px] font-mono dark:bg-dark-elevated dark:text-dark-text" />
        <button onclick={addHeader} class="px-2 py-1 text-[10px] border border-gray-300 dark:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted">Add</button>
      </div>
      <p class="text-[10px] text-gray-400 dark:text-dark-text-muted">The key and header values are stored encrypted and fetched only when this browser calls the provider.</p>
    </div>
  </div>

  {#if error}
    <p class="text-[11px] text-red-600 dark:text-red-400">{error}</p>
  {/if}
  <div class="flex items-center gap-2">
    <button onclick={save} disabled={saving} class="px-2.5 py-1 text-xs border border-gray-900 dark:border-accent bg-gray-900 dark:bg-accent text-white disabled:opacity-50">
      {saving ? 'Saving…' : 'Save'}
    </button>
    <button onclick={oncancel} class="px-2.5 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary">Cancel</button>
  </div>
</div>
