<script lang="ts">
  import { ArrowLeft, Copy, ImageOff, Loader2, LockKeyhole, MessageSquareText, Wrench } from 'lucide-svelte';
  import { onMount } from 'svelte';
  import { push } from 'svelte-spa-router';

  import { getInfo } from '@/lib/api/gateway';
  import { chatShareMediaURL, getChatShare, importChatShare, type ChatShare } from '@/lib/api/playground';
  import { workspaceTransport } from '@/lib/api/transport';
  import Markdown from '@/lib/components/Markdown.svelte';
  import { getTextContent, type ContentPart } from '@/lib/helper/chat';
  import { addToast } from '@/lib/store/toast.svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';

  let { params = {} }: { params?: { id?: string } } = $props();
  let share = $state<ChatShare | null>(null);
  let loading = $state(true);
  let copying = $state(false);
  let error = $state('');
  let models = $state<string[]>([]);
  let selectedModel = $state('');
  const sharedWorkspace = new URLSearchParams(location.search).get('workspace_id') || workspaceTransport.selected;

  storeNavbar.title = 'Shared chat';

  const contentParts = (value: unknown): ContentPart[] => Array.isArray(value) ? value as ContentPart[] : [];

  onMount(async () => {
    try {
      const [loaded, info] = await Promise.all([getChatShare(params.id ?? '', sharedWorkspace), getInfo(sharedWorkspace)]);
      share = loaded;
      models = info.providers.flatMap(provider => provider.models.map(model => `${provider.reference || provider.key}/${model}`));
      const sourceModel = loaded.payload.provider_key && loaded.payload.model ? `${loaded.payload.provider_key}/${loaded.payload.model}` : '';
      selectedModel = models.includes(sourceModel) ? sourceModel : '';
    } catch (e: any) {
      error = e?.response?.data?.message || 'This shared chat is unavailable.';
    } finally {
      loading = false;
    }
  });

  async function copyToChats() {
    if (!share || copying) return;
    copying = true;
    try {
      const slash = selectedModel.indexOf('/');
      const input = slash > 0 ? { provider_key: selectedModel.slice(0, slash), model: selectedModel.slice(slash + 1) } : {};
      const conversation = await importChatShare(share.id, share.version, input, sharedWorkspace);
      addToast('Copied to your chats. The snapshot is now independent.', 'info');
      push(`/chats/${encodeURIComponent(conversation.id)}`);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to copy the shared chat', 'alert');
    } finally {
      copying = false;
    }
  }
</script>

<div class="h-full overflow-y-auto bg-gray-50 dark:bg-dark-base">
  <div class="mx-auto w-full max-w-4xl px-4 py-5 sm:px-6 sm:py-8">
    <a href="#/chats" class="mb-4 inline-flex items-center gap-1.5 text-xs text-gray-500 hover:text-gray-900 dark:text-dark-text-muted dark:hover:text-dark-text focus-visible:outline-2 focus-visible:outline-accent">
      <ArrowLeft size={14} /> Back to Chats
    </a>

    {#if loading}
      <div class="flex min-h-64 items-center justify-center gap-2 border border-gray-200 bg-white text-sm text-gray-500 dark:border-dark-border dark:bg-dark-surface dark:text-dark-text-muted">
        <Loader2 size={16} class="animate-spin" /> Loading shared snapshot
      </div>
    {:else if error || !share}
      <div class="border border-red-200 bg-white p-5 dark:border-red-900/60 dark:bg-dark-surface">
        <h1 class="text-base font-semibold text-gray-900 dark:text-dark-text">Shared chat unavailable</h1>
        <p class="mt-2 text-sm text-gray-600 dark:text-dark-text-secondary">{error || 'The link may have been revoked, or you no longer have access to its workspace.'}</p>
      </div>
    {:else}
      <header class="border border-gray-200 bg-white dark:border-dark-border dark:bg-dark-surface">
        <div class="flex flex-col gap-3 border-b border-gray-200 bg-gray-50 px-4 py-3 dark:border-dark-border dark:bg-dark-base sm:flex-row sm:items-start sm:justify-between">
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <MessageSquareText size={16} class="text-purple-600 dark:text-purple-400" />
              <h1 class="truncate text-base font-semibold text-gray-900 dark:text-dark-text">{share.payload.title || 'Shared conversation'}</h1>
            </div>
            <p class="mt-1 text-xs text-gray-500 dark:text-dark-text-muted">Snapshot version {share.version} · through message {share.through_sequence}</p>
          </div>
          <div class="flex items-center gap-1.5 text-[11px] text-gray-500 dark:text-dark-text-muted">
            <LockKeyhole size={12} /> Workspace members only
          </div>
        </div>
        <div class="grid gap-3 p-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end">
          <label class="block min-w-0">
            <span class="mb-1 block text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Model for your copy</span>
            <select bind:value={selectedModel} class="h-10 w-full border border-gray-300 bg-white px-2.5 text-xs text-gray-700 focus-visible:outline-2 focus-visible:outline-accent dark:border-dark-border-subtle dark:bg-dark-surface dark:text-dark-text-secondary">
              <option value="">Choose later in Chats</option>
              {#each models as model}<option value={model}>{model}</option>{/each}
            </select>
            {#if share.payload.provider_key && !selectedModel}
              <span class="mt-1 block text-[11px] text-amber-700 dark:text-amber-400">The author's model is not available to you. No credential or provider access is transferred.</span>
            {/if}
          </label>
          <button onclick={copyToChats} disabled={copying} class="h-10 inline-flex items-center justify-center gap-2 bg-purple-600 px-4 text-xs font-medium text-white hover:bg-purple-700 disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent">
            {#if copying}<Loader2 size={14} class="animate-spin" />{:else}<Copy size={14} />{/if}
            Copy to my chats
          </button>
        </div>
      </header>

      {#if share.payload.system_prompt}
        <section class="mt-4 border border-gray-200 bg-white px-4 py-3 dark:border-dark-border dark:bg-dark-surface">
          <div class="mb-1 flex items-center gap-1.5 text-xs font-medium text-gray-700 dark:text-dark-text-secondary"><Wrench size={12} /> Included system prompt</div>
          <p class="whitespace-pre-wrap text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary">{share.payload.system_prompt}</p>
        </section>
      {/if}

      <main class="mt-4 space-y-3" aria-label="Shared conversation transcript">
        {#each share.payload.messages as message}
          <article class={['flex', message.role === 'user' ? 'justify-end' : 'justify-start']}>
            <div class={['max-w-[85%] border px-3 py-2.5', message.role === 'user' ? 'border-purple-200 bg-purple-50 dark:border-purple-900/60 dark:bg-purple-900/15' : message.role === 'tool' ? 'border-gray-300 bg-gray-100 dark:border-dark-border-subtle dark:bg-dark-elevated' : 'border-gray-200 bg-white dark:border-dark-border dark:bg-dark-surface']}>
              <div class="mb-1 text-[10px] font-semibold uppercase tracking-wide text-gray-400 dark:text-dark-text-muted">{message.role}</div>
              {#if contentParts(message.data.content).length > 0}
                {#each contentParts(message.data.content) as part}
                  {#if part.type === 'image' && part.media_id}
                    <img src={chatShareMediaURL(share.id, part.media_id, workspaceTransport.selected)} alt={part.name || 'Shared attachment'} class="mb-2 max-h-80 max-w-full border border-gray-200 object-contain dark:border-dark-border" />
                  {:else if part.type === 'text'}
                    <Markdown source={part.text || ''} />
                  {:else if part.type === 'image'}
                    <div class="mb-2 flex items-center gap-1.5 border border-dashed border-gray-300 px-2 py-1 text-[11px] text-gray-500 dark:border-dark-border-subtle dark:text-dark-text-muted"><ImageOff size={12} /> Attachment unavailable</div>
                  {/if}
                {/each}
              {:else}
                <Markdown source={getTextContent(message.data.content as any)} />
              {/if}
              {#if message.role === 'tool'}
                <div class="mt-1 text-[10px] text-gray-400 dark:text-dark-text-muted">Historical result only — it will not run when copied.</div>
              {/if}
            </div>
          </article>
        {/each}
      </main>
    {/if}
  </div>
</div>
