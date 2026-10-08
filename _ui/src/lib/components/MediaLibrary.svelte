<script lang="ts">
  import { onMount } from 'svelte';
  import { Download, FileAudio, FileText, Image, Loader2, Play, RefreshCw, Trash2, X, File } from 'lucide-svelte';
  import { deleteMedia, listMedia, mediaErrorMessage, mediaImageURL, mediaObjectName, type MediaObject } from '@/lib/api/media';
  import { workspaceTransport } from '@/lib/api/transport';
  import { addToast } from '@/lib/store/toast.svelte';

  const controlClass = 'inline-flex h-7 shrink-0 items-center justify-center gap-1.5 border border-dark-border px-2 text-xs text-dark-text-secondary hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-40 disabled:cursor-not-allowed';
  const iconClass = 'inline-flex size-7 shrink-0 items-center justify-center text-dark-text-secondary hover:bg-dark-elevated hover:text-dark-text focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-40';

  let objects = $state<MediaObject[]>([]);
  let nextBefore = $state('');
  let loading = $state(false);
  let loadingMore = $state(false);
  let error = $state('');
  let selected = $state<MediaObject | null>(null);
  let deleteConfirm = $state<MediaObject | null>(null);
  let deleting = $state(false);
  let version = 0;

  const workspace = $derived(workspaceTransport.selected || undefined);
  const url = (object: MediaObject) => mediaImageURL(object.id, workspace);

  function kind(object: MediaObject): 'image' | 'video' | 'audio' | 'pdf' | 'other' {
    const type = object.content_type || '';
    if (['image/png', 'image/jpeg', 'image/gif', 'image/webp'].includes(type)) return 'image';
    if (type.startsWith('video/')) return 'video';
    if (type.startsWith('audio/')) return 'audio';
    if (type === 'application/pdf') return 'pdf';
    return 'other';
  }

  function icon(object: MediaObject) {
    switch (kind(object)) {
      case 'image': return Image;
      case 'video': return Play;
      case 'audio': return FileAudio;
      case 'pdf': return FileText;
      default: return File;
    }
  }

  function formatSize(bytes: number): string {
    if (!bytes) return '-';
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
  }

  function formatDate(value: string): string {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
  }

  async function load() {
    const request = ++version;
    loading = true;
    error = '';
    selected = null;
    try {
      const page = await listMedia();
      if (request !== version) return;
      objects = page.data;
      nextBefore = page.next_before;
    } catch (e) {
      if (request === version) error = mediaErrorMessage(e, 'Could not load media.');
    } finally {
      if (request === version) loading = false;
    }
  }

  async function loadMore() {
    if (!nextBefore || loadingMore) return;
    const request = version;
    loadingMore = true;
    try {
      const page = await listMedia(nextBefore);
      if (request !== version) return;
      objects = [...objects, ...page.data];
      nextBefore = page.next_before;
    } catch (e) {
      if (request === version) addToast(mediaErrorMessage(e, 'Could not load more media.'), 'alert');
    } finally {
      if (request === version) loadingMore = false;
    }
  }

  async function remove(object: MediaObject) {
    if (deleting) return;
    deleting = true;
    try {
      await deleteMedia(object.id);
      objects = objects.filter((item) => item.id !== object.id);
      if (selected?.id === object.id) selected = null;
      deleteConfirm = null;
      addToast('Deleted', 'info');
    } catch (e) {
      addToast(mediaErrorMessage(e, 'Delete failed'), 'alert');
    } finally {
      deleting = false;
    }
  }

  onMount(() => {
    void load();
    return () => { ++version; };
  });
</script>

<div class="flex h-full min-h-0 min-w-0">
  <div class={[selected ? 'hidden xl:flex' : 'flex', 'flex-1 flex-col min-h-0 min-w-0']}>
    <header class="flex flex-wrap items-center gap-2 px-3 py-2 border-b border-dark-border bg-dark-surface shrink-0">
      <p class="min-w-0 flex-1 text-xs text-dark-text-secondary">
        Images and files you created or attached in Chats, including generated images and skill-run outputs. Only your own media in this workspace is listed.
      </p>
      <button onclick={() => void load()} disabled={loading} class={iconClass} title="Refresh" aria-label="Refresh media"><RefreshCw size={16} /></button>
    </header>
    {#if error}<div role="alert" class="m-3 border border-red-900 p-3 text-sm text-red-300">{error}</div>{/if}

    <div class="flex-1 min-h-0 overflow-y-auto bg-dark-surface" aria-busy={loading}>
      {#if loading}
        <div role="status" class="flex items-center justify-center gap-2 py-12 text-sm text-dark-text-secondary"><Loader2 size={16} class="animate-spin motion-reduce:animate-none" />Loading media…</div>
      {:else if objects.length === 0 && !error}
        <div class="text-center py-12 text-sm text-dark-text-secondary">No media yet</div>
      {:else}
        <ul class="grid grid-cols-[repeat(auto-fill,minmax(9rem,1fr))] gap-2 p-3">
          {#each objects as object (object.id)}
            {@const Icon = icon(object)}
            <li class={['border', selected?.id === object.id ? 'border-accent' : 'border-dark-border hover:border-dark-text-faint']}>
              <button
                onclick={() => { selected = object; }}
                class="flex w-full flex-col text-left focus-visible:outline-2 focus-visible:outline-accent"
                title={mediaObjectName(object)}
              >
                <span class="flex aspect-square w-full items-center justify-center overflow-hidden bg-dark-base">
                  {#if kind(object) === 'image'}
                    <img src={url(object)} alt={mediaObjectName(object)} loading="lazy" class="size-full object-cover" />
                  {:else}
                    <Icon size={28} class="text-dark-text-secondary" />
                  {/if}
                </span>
                <span class="block px-2 py-1 text-xs text-dark-text-secondary">
                  <span class="block truncate">{formatDate(object.created_at)}</span>
                  <span class="block truncate">{formatSize(object.size_bytes)} · {object.content_type}</span>
                </span>
              </button>
            </li>
          {/each}
        </ul>
        {#if nextBefore}
          <div class="flex justify-center pb-4">
            <button onclick={() => void loadMore()} disabled={loadingMore} class={controlClass}>
              {#if loadingMore}<Loader2 size={13} class="animate-spin motion-reduce:animate-none" />{/if}
              {loadingMore ? 'Loading…' : 'Load older media'}
            </button>
          </div>
        {/if}
      {/if}
    </div>
  </div>

  {#if selected}
    <section aria-label="Media preview" class="w-full xl:w-[28rem] min-w-0 min-h-0 xl:border-l border-dark-border bg-dark-surface flex flex-col shrink-0">
      <div class="flex flex-wrap items-center justify-between gap-2 px-3 py-2 border-b border-dark-border shrink-0">
        <div class="min-w-0">
          <h2 class="text-sm font-medium text-dark-text break-all">{mediaObjectName(selected)}</h2>
          <div class="text-xs text-dark-text-secondary">{formatSize(selected.size_bytes)} · {selected.content_type} · {formatDate(selected.created_at)}</div>
        </div>
        <div class="flex items-center gap-1 shrink-0">
          <a href={url(selected)} download={mediaObjectName(selected)} class={iconClass} title="Download" aria-label="Download media"><Download size={14} /></a>
          <button onclick={() => { deleteConfirm = selected; }} class={`${iconClass} hover:!text-red-400`} title="Delete" aria-label="Delete media"><Trash2 size={14} /></button>
          <button onclick={() => { selected = null; }} class={controlClass} aria-label="Close preview"><X size={16} /><span class="xl:hidden">Back to media</span></button>
        </div>
      </div>
      <div class="flex-1 min-h-0 overflow-auto p-3 sm:p-4 bg-dark-base">
        {#if kind(selected) === 'image'}
          <img src={url(selected)} alt={mediaObjectName(selected)} class="mx-auto max-w-full max-h-full object-contain" />
        {:else if kind(selected) === 'video'}
          <!-- svelte-ignore a11y_media_has_caption -->
          <video src={url(selected)} controls preload="metadata" class="w-full max-h-full bg-black"></video>
        {:else if kind(selected) === 'audio'}
          <!-- svelte-ignore a11y_media_has_caption -->
          <audio src={url(selected)} controls preload="metadata" class="w-full mt-8"></audio>
        {:else}
          <div class="space-y-3 py-8 text-center text-sm text-dark-text-secondary">
            <FileText size={32} class="mx-auto" />
            <p>No browser preview for this file type.</p>
            <a class={controlClass} href={url(selected)} download={mediaObjectName(selected)}><Download size={16} />Download file</a>
          </div>
        {/if}
        <dl class="mt-4 grid grid-cols-[5.5rem_minmax(0,1fr)] gap-x-2 gap-y-1 text-xs">
          <dt class="text-dark-text-secondary">ID</dt><dd class="break-all text-dark-text">{selected.id}</dd>
          <dt class="text-dark-text-secondary">Checksum</dt><dd class="break-all text-dark-text">{selected.checksum || '-'}</dd>
          <dt class="text-dark-text-secondary">Backend</dt><dd class="break-all text-dark-text">{selected.backend}</dd>
        </dl>
      </div>
    </section>
  {/if}
</div>

{#if deleteConfirm}
  <section class="fixed inset-x-3 bottom-3 z-50 mx-auto max-w-xl border border-dark-border bg-dark-surface p-4 shadow-lg" aria-label="Confirm media deletion" aria-live="polite">
    <p class="mb-3 break-all text-sm text-dark-text">Delete {mediaObjectName(deleteConfirm)}? Chat messages that show it will no longer display it.</p>
    <div class="flex flex-wrap justify-end gap-2">
      <button class={controlClass} disabled={deleting} onclick={() => { deleteConfirm = null; }}>Cancel</button>
      <button class="inline-flex h-10 items-center bg-red-600 px-4 text-sm font-medium text-white hover:bg-red-700 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-red-600 disabled:opacity-50" disabled={deleting} onclick={() => { if (deleteConfirm) void remove(deleteConfirm); }}>{deleting ? 'Deleting…' : 'Delete'}</button>
    </div>
  </section>
{/if}
