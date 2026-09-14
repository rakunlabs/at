<script lang="ts">
  import { authFetch as fetch } from '@/lib/api/transport';
  import { onMount, untrack } from 'svelte';
  import { querystring } from 'svelte-spa-router';
  import { updateRouteQuery } from '@/lib/helper/route-query';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    Folder, File, ArrowLeft, RefreshCw, Trash2, Play, Image, FileText,
    FileCode, FileAudio, Download, X, ChevronRight, Home, Search, Loader2,
  } from 'lucide-svelte';
  import { browseFiles, deleteFile, fileServeUrl, type FileEntry } from '@/lib/api/files';

  storeNavbar.title = 'Files';

  // All paths are relative to the selected workspace's rooted file API.
  let currentPath = $state('');
  let parentPath = $state('');
  // Relative path within the selected workspace.
  let pathInput = $state('');
  const workspaceRoot = 'tasks';
  let entries = $state<FileEntry[]>([]);
  let loading = $state(false);
  let deleteConfirm = $state<string | null>(null);
  let deleting = $state(false);
  let browseError = $state('');
  let browseVersion = 0;
  let previewController: AbortController | null = null;
  const controlClass = 'inline-flex h-7 shrink-0 items-center justify-center gap-1.5 rounded border border-gray-200 dark:border-dark-border px-2 text-xs text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-40 disabled:cursor-not-allowed';
  const iconClass = 'inline-flex size-7 shrink-0 items-center justify-center rounded text-gray-500 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated hover:text-gray-900 dark:hover:text-dark-text focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-40';
  const inputClass = 'h-7 w-full min-w-0 rounded border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface px-2 text-base sm:text-xs text-gray-900 dark:text-dark-text placeholder:text-gray-500 dark:placeholder:text-dark-text-secondary focus:outline-none focus:ring-1 focus:ring-accent';

  // Search & Sort & Hidden
  let searchQuery = $state('');
  let sortField = $state<'name' | 'size' | 'mod_time'>('name');
  let sortDesc = $state(false);
  let showHidden = $state(false);

  let filteredEntries = $derived.by(() => {
    let result = entries;

    // Filter hidden files
    if (!showHidden) {
      result = result.filter(e => !e.name.startsWith('.'));
    }

    // Filter by search
    if (searchQuery) {
      const q = searchQuery.toLowerCase();
      result = result.filter(e => e.name.toLowerCase().includes(q));
    }

    // Sort
    result = [...result].sort((a, b) => {
      // Directories always first
      if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;

      let cmp = 0;
      if (sortField === 'name') {
        cmp = a.name.localeCompare(b.name);
      } else if (sortField === 'size') {
        cmp = a.size - b.size;
      } else if (sortField === 'mod_time') {
        cmp = a.mod_time.localeCompare(b.mod_time);
      }
      return sortDesc ? -cmp : cmp;
    });

    return result;
  });

  function toggleSort(field: 'name' | 'size' | 'mod_time') {
    if (sortField === field) {
      sortDesc = !sortDesc;
    } else {
      sortField = field;
      sortDesc = false;
    }
  }

  function sortIndicator(field: string): string {
    if (sortField !== field) return '';
    return sortDesc ? ' ↓' : ' ↑';
  }

  // Preview state
  let previewFile = $state<FileEntry | null>(null);
  let previewType = $state<string | null>(null);
  let previewText = $state('');
  let previewLoading = $state(false);
  let previewError = $state('');
  let previewTruncated = $state(false);

  const routePath = $derived(new URLSearchParams($querystring).get('path') || '.');

  $effect(() => {
    const path = routePath;
    untrack(() => { void browse(path, true); });
  });

  async function browse(path: string, fromURL = false) {
    path = path || '.';
    if (!fromURL && path !== routePath) {
      updateRouteQuery({ path: path === '.' ? null : path });
      return;
    }
    const request = ++browseVersion;
    loading = true;
    browseError = '';
    currentPath = path;
    pathInput = path;
    parentPath = path.split('/').slice(0, -1).join('/') || '.';
    entries = [];
    deleteConfirm = null;
    closePreview();
    try {
      const result = await browseFiles(path);
      if (request !== browseVersion) return;
      currentPath = result.path;
      parentPath = result.parent || '.';
      pathInput = result.path;
      entries = result.entries || [];
    } catch (e: any) {
      if (request === browseVersion) browseError = e?.response?.data?.message || (typeof e?.response?.data === 'string' ? e.response.data : 'Could not open this directory. Check your path and workspace access.');
    } finally {
      if (request === browseVersion) loading = false;
    }
  }

  function goToPathInput(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      browse(pathInput.trim() || '.');
    }
  }

  async function handleDelete(path: string) {
    if (deleting) return;
    deleting = true;
    try {
      await deleteFile(path);
      addToast('Deleted', 'info');
      deleteConfirm = null;
      if (previewFile?.path === path) closePreview();
      await browse(currentPath);
    } catch (e: any) {
      addToast(e?.response?.data?.message || (typeof e?.response?.data === 'string' ? e.response.data : 'Delete failed'), 'alert');
    } finally { deleting = false; }
  }

  function getFileType(name: string): string {
    const ext = name.split('.').pop()?.toLowerCase() || '';
    if (['mp4', 'mov', 'webm', 'avi'].includes(ext)) return 'video';
    if (['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp'].includes(ext)) return 'image';
    if (['mp3', 'wav', 'ogg', 'aac', 'm4a', 'flac'].includes(ext)) return 'audio';
    if (['txt', 'log', 'json', 'py', 'sh', 'js', 'ts', 'md', 'csv', 'xml', 'yaml', 'yml', 'toml', 'env', 'cfg'].includes(ext)) return 'text';
    return 'other';
  }

  function getFileIcon(entry: FileEntry) {
    if (entry.is_dir) return Folder;
    const type = getFileType(entry.name);
    if (type === 'video') return Play;
    if (type === 'image') return Image;
    if (type === 'audio') return FileAudio;
    if (type === 'text') return FileCode;
    return File;
  }

  function formatSize(bytes: number): string {
    if (bytes === 0) return '-';
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
  }

  async function openPreview(entry: FileEntry) {
    if (entry.is_dir) {
      browse(entry.path);
      return;
    }

    closePreview();
    previewFile = entry;
    previewType = getFileType(entry.name);
    if (previewType === 'text') {
      const controller = new AbortController();
      previewController = controller;
      previewLoading = true;
      try {
        const res = await fetch(fileServeUrl(entry.path, entry.mod_time), { headers: { Range: 'bytes=0-1048575' }, signal: controller.signal });
        if (!res.ok) throw new Error((await res.text()).slice(0, 300) || 'File unavailable');
        const text = await res.text();
        if (controller.signal.aborted) return;
        previewText = text;
        previewTruncated = entry.size > 1048576;
      } catch (e) {
        if (!controller.signal.aborted) previewError = e instanceof Error ? e.message : 'Could not load this file.';
      } finally { if (!controller.signal.aborted) previewLoading = false; }
    }
  }

  function closePreview() {
    previewController?.abort();
    previewController = null;
    previewFile = null;
    previewType = null;
    previewText = '';
    previewLoading = false;
    previewError = '';
    previewTruncated = false;
  }

  // Breadcrumb parts
  let breadcrumbs = $derived.by(() => {
    const parts = (currentPath || '').split('/').filter(part => part && part !== '.');
    const crumbs: { name: string; path: string }[] = [{ name: 'Workspace', path: '.' }];
    let acc = '';
    for (const part of parts) {
      acc = acc ? acc + '/' + part : part;
      crumbs.push({ name: part, path: acc });
    }
    return crumbs;
  });

  // URL changes drive loading; invalidate pending work when leaving the page.
  onMount(() => {
    return () => { ++browseVersion; previewController?.abort(); };
  });
</script>

<svelte:head><title>AT | Files</title></svelte:head>
<div class="flex h-full min-h-0 min-w-0 bg-gray-50 dark:bg-dark-base text-gray-900 dark:text-dark-text">
  <!-- Main content -->
  <div class={[previewFile ? 'hidden xl:flex' : 'flex', 'flex-1 flex-col min-h-0 min-w-0']}>
    <!-- Header -->
    <header class="flex flex-col gap-1.5 px-3 py-2 border-b border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface shrink-0">
      <div class="flex items-center gap-2 min-w-0">
        <button
          onclick={() => browse(parentPath)}
          disabled={loading || currentPath === '.' || !parentPath || parentPath === currentPath}
          class={iconClass}
          title="Go up"
          aria-label="Go to parent directory"
        >
          <ArrowLeft size={14} />
        </button>

        <!-- Breadcrumbs -->
        <nav aria-label="File path" class="flex items-center gap-1 text-xs min-w-0 overflow-x-auto">
          {#each breadcrumbs as crumb, i}
            {#if i > 0}
              <ChevronRight size={10} class="text-gray-300 dark:text-dark-text-faint shrink-0" />
            {/if}
            <button
              onclick={() => browse(crumb.path)}
              class={[
                'shrink-0 min-h-7 truncate max-w-40 px-1 rounded focus-visible:outline-2 focus-visible:outline-accent transition-colors',
                i === breadcrumbs.length - 1
                  ? 'text-gray-900 dark:text-dark-text font-medium'
                  : 'text-gray-500 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text'
              ]}
              aria-current={i === breadcrumbs.length - 1 ? 'location' : undefined}
              aria-label={i === 0 ? 'Workspace root' : crumb.name}
              title={crumb.path}
            >
              {#if i === 0}
                  <Home size={14} />
              {:else}
                {crumb.name}
              {/if}
            </button>
          {/each}
        </nav>
        <button onclick={() => browse(currentPath)} disabled={loading} class={`${iconClass} ml-auto`} title="Refresh" aria-label="Refresh directory"><RefreshCw size={16} /></button>
      </div>

      <div class="flex flex-wrap items-center gap-1.5 min-w-0">
        <!-- Paths are rooted in the selected workspace and checked by the server. -->
        <input
          type="text"
          bind:value={pathInput}
          onkeydown={goToPathInput}
          placeholder="Workspace path (e.g. assets/uploads)"
          aria-label="Workspace directory path"
          class={`${inputClass} basis-full lg:basis-64 lg:flex-1 font-mono`}
        />
        <!-- Search -->
        <div class="relative min-w-0 flex-1 basis-40">
          <Search size={13} class="absolute left-2 top-1/2 -translate-y-1/2 text-gray-500 dark:text-dark-text-secondary" />
          <input
            type="text"
            bind:value={searchQuery}
            placeholder="Filter files…"
            aria-label="Filter files"
            class={`${inputClass} pl-7`}
          />
        </div>
        <!-- Workspace-relative conventional roots. -->
        <button
          onclick={() => browse(workspaceRoot)}
          class={controlClass}
          title={workspaceRoot}
        >tasks</button>
        <button
          onclick={() => browse('assets')}
          class={controlClass}
        >assets</button>
        <button
          onclick={() => browse('assets/uploads')}
          class={controlClass}
        >uploads</button>
        <button
          onclick={() => browse('runs')}
          class={controlClass}
        >runs</button>
        <button
          onclick={() => browse('.')}
          class={controlClass}
        >workspace</button>
        <!-- Hidden files toggle -->
        <button
          onclick={() => { showHidden = !showHidden; }}
          class={`${controlClass} ${showHidden ? 'bg-gray-900 !text-white dark:bg-gray-100 dark:!text-gray-950' : ''}`}
          title={showHidden ? 'Hide dotfiles' : 'Show dotfiles'}
          aria-pressed={showHidden}
        >.hidden</button>
      </div>
    </header>
    {#if browseError}<div role="alert" class="m-3 rounded-md border border-red-200 dark:border-red-900 p-3 text-sm text-red-700 dark:text-red-300">{browseError}</div>{/if}

    <!-- File list -->
    <div class="flex-1 min-h-0 overflow-y-auto bg-white dark:bg-dark-surface" aria-busy={loading}>
      {#if loading}
        <div role="status" class="flex items-center justify-center gap-2 py-12 text-sm text-gray-500 dark:text-dark-text-secondary"><Loader2 size={16} class="animate-spin motion-reduce:animate-none" />Loading files…</div>
      {:else if filteredEntries.length === 0}
        <div class="text-center py-12 text-sm text-gray-500 dark:text-dark-text-secondary">
          {entries.length > 0 ? 'No matches' : 'Empty directory'}
        </div>
      {:else}
        <table class="w-full table-fixed text-xs">
          <thead>
            <tr class="border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base text-xs text-gray-600 dark:text-dark-text-secondary">
              <th class="text-left px-3 py-1.5 font-medium">
                <button onclick={() => toggleSort('name')} class="hover:text-gray-700 dark:hover:text-dark-text-secondary">Name{sortIndicator('name')}</button>
              </th>
              <th class="hidden md:table-cell text-right px-3 py-1.5 font-medium w-24">
                <button onclick={() => toggleSort('size')} class="hover:text-gray-700 dark:hover:text-dark-text-secondary">Size{sortIndicator('size')}</button>
              </th>
              <th class="hidden 2xl:table-cell text-right px-3 py-1.5 font-medium w-40">
                <button onclick={() => toggleSort('mod_time')} class="hover:text-gray-700 dark:hover:text-dark-text-secondary">Modified{sortIndicator('mod_time')}</button>
              </th>
              <th class="text-right px-2 py-1.5 font-medium w-20"><span class="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-border">
            {#each filteredEntries as entry}
              {@const FileIcon = getFileIcon(entry)}
              <tr class={[
                'transition-colors group',
                previewFile?.path === entry.path
                  ? 'bg-gray-100 dark:bg-dark-elevated'
                  : 'hover:bg-gray-50 dark:hover:bg-dark-elevated'
              ]}>
                <td class="px-3 py-0.5">
                  <button
                    onclick={() => openPreview(entry)}
                    class="flex min-h-7 min-w-0 items-center gap-2 text-left transition-colors w-full rounded focus-visible:outline-2 focus-visible:outline-accent"
                    title={entry.name}
                  >
                    <span class="inline-flex size-4 shrink-0 items-center justify-center text-gray-600 dark:text-dark-text-secondary">
                      <FileIcon size={14} />
                    </span>
                    <span class="min-w-0"><span class="block truncate text-gray-800 dark:text-dark-text">{entry.name}</span><span class="block text-xs text-gray-500 dark:text-dark-text-secondary md:hidden">{entry.is_dir ? 'Folder' : formatSize(entry.size)}</span></span>
                  </button>
                </td>
                <td class="hidden md:table-cell px-3 py-0.5 text-right text-xs text-gray-500 dark:text-dark-text-secondary tabular-nums">
                  {entry.is_dir ? '-' : formatSize(entry.size)}
                </td>
                <td class="hidden 2xl:table-cell px-3 py-0.5 text-right text-xs text-gray-500 dark:text-dark-text-secondary tabular-nums">
                  {entry.mod_time}
                </td>
                <td class="px-2 py-0.5 text-right">
                  <div class="flex items-center justify-end">
                    {#if !entry.is_dir}
                      <a
                        href={fileServeUrl(entry.path, entry.mod_time)}
                        download={entry.name}
                        class={iconClass}
                        title={`Download ${entry.name}`}
                        aria-label={`Download ${entry.name}`}
                      >
                        <Download size={14} />
                      </a>
                    {/if}
                      <button
                        onclick={() => (deleteConfirm = entry.path)}
                        class={`${iconClass} hover:!text-red-600 dark:hover:!text-red-400`}
                        title={`Delete ${entry.name}`}
                        aria-label={`Delete ${entry.name}`}
                      >
                        <Trash2 size={14} />
                      </button>
                  </div>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}
    </div>
  </div>

  <!-- Preview panel -->
  {#if previewFile}
    <section aria-label="File preview" class="w-full xl:w-[28rem] min-w-0 min-h-0 xl:border-l border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface flex flex-col shrink-0">
      <!-- Preview header -->
      <div class="flex flex-wrap items-center justify-between gap-2 px-3 py-2 border-b border-gray-200 dark:border-dark-border shrink-0">
        <div class="min-w-0">
          <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text break-all">{previewFile.name}</h2>
          <div class="text-xs text-gray-500 dark:text-dark-text-secondary">{formatSize(previewFile.size)} · {previewFile.mod_time}</div>
        </div>
        <div class="flex items-center gap-1 shrink-0">
          <a
            href={fileServeUrl(previewFile.path, previewFile.mod_time)}
            download={previewFile.name}
            class={iconClass}
            title="Download"
            aria-label="Download previewed file"
          >
            <Download size={14} />
          </a>
          <button
            onclick={() => { deleteConfirm = previewFile?.path ?? null; }}
            class={`${iconClass} hover:!text-red-600`}
            title="Delete"
            aria-label="Delete previewed file"
          >
            <Trash2 size={14} />
          </button>
          <button
            onclick={closePreview}
            class={controlClass}
            aria-label="Close preview"
          >
            <X size={16} /><span class="xl:hidden">Back to files</span>
          </button>
        </div>
      </div>

      <!-- Preview content -->
      <div class="flex-1 min-h-0 overflow-auto p-3 sm:p-4 bg-gray-50 dark:bg-dark-base">
        {#if previewLoading}<p role="status" class="flex items-center gap-2 text-sm text-gray-500 dark:text-dark-text-secondary"><Loader2 size={16} class="animate-spin motion-reduce:animate-none" />Loading preview…</p>
        {:else if previewError}<p role="alert" class="text-sm text-red-700 dark:text-red-300">{previewError} Download the file to open it on your device.</p>
        {:else if previewType === 'video'}
          <!-- svelte-ignore a11y_media_has_caption -->
          <video
            src={fileServeUrl(previewFile.path, previewFile.mod_time)}
            controls
            preload="metadata"
            class="w-full max-h-full rounded-md bg-black"
            onerror={() => { previewError = 'The video could not be played in this browser.'; }}
          ></video>
        {:else if previewType === 'image'}
          <img
            src={fileServeUrl(previewFile.path, previewFile.mod_time)}
            alt={previewFile.name}
            class="mx-auto max-w-full max-h-full object-contain rounded-md"
            onerror={() => { previewError = 'The image could not be loaded.'; }}
          />
        {:else if previewType === 'audio'}
          <div class="w-full pt-8">
            <div class="text-center mb-4">
              <FileAudio size={40} class="mx-auto text-gray-500 dark:text-dark-text-secondary mb-2" />
              <div class="text-sm text-gray-600 dark:text-dark-text-secondary">{previewFile.name}</div>
            </div>
            <!-- svelte-ignore a11y_media_has_caption -->
            <audio
              src={fileServeUrl(previewFile.path, previewFile.mod_time)}
              controls
              preload="metadata"
              class="w-full"
              onerror={() => { previewError = 'The audio could not be played in this browser.'; }}
            ></audio>
          </div>
        {:else if previewType === 'text'}
          {#if previewTruncated}<p class="mb-3 text-sm text-gray-600 dark:text-dark-text-secondary">Showing the first 1 MB. Download the file for the full content.</p>{/if}
          <pre class="w-full text-sm font-mono text-gray-800 dark:text-dark-text whitespace-pre-wrap break-all bg-white dark:bg-dark-surface p-3 border border-gray-200 dark:border-dark-border rounded-md">{previewText}</pre>
        {:else}
          <div class="space-y-3 py-8 text-center text-sm text-gray-600 dark:text-dark-text-secondary"><FileText size={32} class="mx-auto" /><p>No browser preview for this file type.</p><a class={controlClass} href={fileServeUrl(previewFile.path, previewFile.mod_time)} download={previewFile.name}><Download size={16} />Download file</a></div>
        {/if}
      </div>
    </section>
  {/if}
</div>

{#if deleteConfirm}
  <section class="fixed inset-x-3 bottom-3 z-50 mx-auto max-w-xl rounded-lg border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-surface p-4 shadow-lg" aria-label="Confirm file deletion" aria-live="polite">
    <p id="file-delete-description" class="mb-3 break-all text-sm text-gray-900 dark:text-dark-text">Delete {deleteConfirm}? Only files and empty folders can be deleted.</p>
    <div class="flex flex-wrap justify-end gap-2"><button class={controlClass} disabled={deleting} onclick={() => { deleteConfirm = null; }}>Cancel</button><button class="inline-flex h-10 items-center rounded-md bg-red-600 px-4 text-sm font-medium text-white hover:bg-red-700 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-red-600 disabled:opacity-50" disabled={deleting} onclick={() => { if (deleteConfirm) void handleDelete(deleteConfirm); }}>{deleting ? 'Deleting…' : 'Delete'}</button></div>
  </section>
{/if}
