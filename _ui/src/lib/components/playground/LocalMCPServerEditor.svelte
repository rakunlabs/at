<script lang="ts">
  import { X } from 'lucide-svelte';
  import { saveLocalMCPServers, type LocalMCPServer } from '@/lib/api/local-mcp';
  import { REDACTED, localMCPUrlProblem } from '@/lib/helper/local-mcp';

  interface Props {
    /** Record being edited; absent for a new server. */
    server?: LocalMCPServer;
    servers: LocalMCPServer[];
    /** Receives the stored list after a successful save. */
    onsaved: (servers: LocalMCPServer[], edited?: LocalMCPServer) => void;
    oncancel: () => void;
  }

  let { server, servers, onsaved, oncancel }: Props = $props();

  // The draft is seeded once: the parent remounts this editor per record.
  // svelte-ignore state_referenced_locally
  let name = $state(server?.name ?? '');
  // svelte-ignore state_referenced_locally
  let url = $state(server?.url ?? '');
  // Values arrive redacted; replaying the sentinel preserves the stored one.
  // svelte-ignore state_referenced_locally
  let headers = $state<Record<string, string>>({ ...(server?.headers ?? {}) });
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
    if (!name.trim()) {
      error = 'Name is required';

      return;
    }
    const problem = localMCPUrlProblem(url);
    if (problem) {
      error = problem;

      return;
    }

    const id = server?.id ?? '';
    const entry: LocalMCPServer = {
      id,
      name: name.trim(),
      url: url.trim(),
      headers: Object.keys(headers).length > 0 ? headers : undefined,
    };
    const next = id ? servers.map(s => (s.id === id ? entry : s)) : [...servers, entry];

    saving = true;
    try {
      const stored = await saveLocalMCPServers(next);
      onsaved(stored, id ? stored.find(s => s.id === id) : undefined);
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
      <input bind:value={name} placeholder="laptop" class="col-span-3 border border-dark-border-subtle px-2 py-1 text-xs bg-dark-elevated text-dark-text" />
    </label>
    <label class="contents">
      <span class="text-xs text-dark-text-secondary">URL</span>
      <input bind:value={url} placeholder="http://127.0.0.1:3000/mcp" class="col-span-3 border border-dark-border-subtle px-2 py-1 text-xs font-mono bg-dark-elevated text-dark-text" />
    </label>
    <div class="col-start-2 col-span-3 text-[10px] text-dark-text-muted">
      Full endpoint URL, used exactly as entered. Loopback and private addresses only — a reachable server belongs in an MCP set, where execution policy and tracing apply.
    </div>
  </div>

  <div class="grid grid-cols-4 gap-2 items-start">
    <span class="text-xs text-dark-text-secondary pt-1">Headers</span>
    <div class="col-span-3 space-y-1">
      {#each Object.entries(headers) as [hk, hv]}
        <div class="flex items-center gap-1">
          <span class="text-[10px] font-mono text-dark-text-secondary">{hk}:</span>
          <span class="text-[10px] font-mono text-dark-text-muted truncate">{hv === REDACTED ? 'stored' : hv}</span>
          <button
            onclick={() => removeHeader(hk)}
            class="ml-auto p-0.5 text-dark-text-muted hover:text-red-500"
            aria-label={`Remove header ${hk}`}
          ><X size={10} /></button>
        </div>
      {/each}
      <div class="flex items-center gap-1">
        <input bind:value={headerKey} placeholder="Authorization" class="flex-1 border border-dark-border-subtle px-2 py-1 text-[11px] font-mono bg-dark-elevated text-dark-text" />
        <input bind:value={headerValue} placeholder="value" type="password" class="flex-1 border border-dark-border-subtle px-2 py-1 text-[11px] font-mono bg-dark-elevated text-dark-text" />
        <button onclick={addHeader} class="px-2 py-1 text-[10px] border border-dark-border-subtle text-dark-text-muted">Add</button>
      </div>
      <p class="text-[10px] text-dark-text-muted">Stored encrypted and never shown again.</p>
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
