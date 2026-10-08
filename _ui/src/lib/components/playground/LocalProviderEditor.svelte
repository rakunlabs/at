<script lang="ts">
  import { Eye, EyeOff, X } from 'lucide-svelte';
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
  let showApiKey = $state(false);
  let showHeaderValue = $state(false);
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

<div class="mt-1.5 border border-dark-border-subtle p-2.5 space-y-2">
  <div class="grid grid-cols-4 gap-2 items-center">
    <label class="contents">
      <span class="text-xs text-dark-text-secondary">Name</span>
      <input bind:value={name} placeholder="ollama" class="col-span-3 border border-dark-border-subtle px-2 py-1 text-xs bg-dark-elevated text-dark-text" />
    </label>
    <label class="contents">
      <span class="text-xs text-dark-text-secondary">Base URL</span>
      <input bind:value={baseUrl} placeholder="http://127.0.0.1:11434/v1" class="col-span-3 border border-dark-border-subtle px-2 py-1 text-xs font-mono bg-dark-elevated text-dark-text" />
    </label>
    <div class="col-start-2 col-span-3 text-[10px] text-dark-text-muted">
      OpenAI-compatible API root. Your browser calls <code>/models</code> and <code>/chat/completions</code> under it directly; plain http only for local addresses.
      Another AT server: <code>https://&lt;host&gt;/gateway/v1</code> with one of its API tokens.
      The browser cannot skip certificate checks; for an untrusted certificate add it on the Providers page with <em>Insecure skip verify</em> instead.
    </div>
    <label class="contents">
      <span class="text-xs text-dark-text-secondary">API key</span>
      {#if apiKey === REDACTED}
        <div class="col-span-3 flex items-center gap-2">
          <span class="text-[11px] text-dark-text-muted">Stored</span>
          <button onclick={() => (apiKey = '')} class="px-2 py-0.5 text-[10px] border border-dark-border-subtle text-dark-text-muted">Replace or remove</button>
        </div>
      {:else}
        <div class="col-span-3 flex items-center border border-dark-border-subtle bg-dark-elevated">
          <input
            bind:value={apiKey}
            type={showApiKey ? 'text' : 'password'}
            placeholder="optional"
            autocomplete="off"
            spellcheck="false"
            class="flex-1 min-w-0 px-2 py-1 text-xs font-mono bg-transparent text-dark-text"
          />
          <button
            type="button"
            onclick={() => (showApiKey = !showApiKey)}
            class="px-1.5 py-1 text-dark-text-muted hover:text-dark-text"
            aria-label={showApiKey ? 'Hide API key' : 'Show API key'}
            aria-pressed={showApiKey}
            title={showApiKey ? 'Hide value' : 'Show value'}
          >
            {#if showApiKey}<EyeOff size={12} />{:else}<Eye size={12} />{/if}
          </button>
        </div>
      {/if}
    </label>
  </div>

  <div class="grid grid-cols-4 gap-2 items-start">
    <span class="text-xs text-dark-text-secondary pt-1">Headers</span>
    <div class="col-span-3 space-y-1">
      {#each Object.entries(headers) as [hk, hv]}
        <div class="flex items-center gap-1">
          <span class="text-[10px] font-mono text-dark-text-secondary">{hk}:</span>
          <span class="text-[10px] font-mono text-dark-text-muted truncate">{hv === REDACTED ? 'stored' : '••••'}</span>
          <button
            onclick={() => removeHeader(hk)}
            class="ml-auto p-0.5 text-dark-text-muted hover:text-red-500"
            aria-label={`Remove header ${hk}`}
          ><X size={10} /></button>
        </div>
      {/each}
      <div class="flex items-center gap-1">
        <input bind:value={headerKey} placeholder="X-Header" class="flex-1 border border-dark-border-subtle px-2 py-1 text-[11px] font-mono bg-dark-elevated text-dark-text" />
        <div class="flex-1 flex items-center border border-dark-border-subtle bg-dark-elevated">
          <input
            bind:value={headerValue}
            placeholder="value"
            type={showHeaderValue ? 'text' : 'password'}
            autocomplete="off"
            spellcheck="false"
            class="flex-1 min-w-0 px-2 py-1 text-[11px] font-mono bg-transparent text-dark-text"
          />
          <button
            type="button"
            onclick={() => (showHeaderValue = !showHeaderValue)}
            class="px-1.5 py-1 text-dark-text-muted hover:text-dark-text"
            aria-label={showHeaderValue ? 'Hide header value' : 'Show header value'}
            aria-pressed={showHeaderValue}
            title={showHeaderValue ? 'Hide value' : 'Show value'}
          >
            {#if showHeaderValue}<EyeOff size={11} />{:else}<Eye size={11} />{/if}
          </button>
        </div>
        <button onclick={addHeader} class="px-2 py-1 text-[10px] border border-dark-border-subtle text-dark-text-muted">Add</button>
      </div>
      <p class="text-[10px] text-dark-text-muted">The key and header values are stored encrypted and fetched only when this browser calls the provider.</p>
    </div>
  </div>

  {#if error}
    <p class="text-[11px] text-red-400">{error}</p>
  {/if}
  <div class="flex items-center gap-2">
    <button onclick={save} disabled={saving} class="px-2.5 py-1 text-xs border border-accent bg-accent text-dark-base disabled:opacity-50">
      {saving ? 'Saving…' : 'Save'}
    </button>
    <button onclick={oncancel} class="px-2.5 py-1 text-xs border border-dark-border-subtle text-dark-text-secondary">Cancel</button>
  </div>
</div>
