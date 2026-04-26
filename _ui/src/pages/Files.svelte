<script lang="ts">
  import { untrack } from 'svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    Folder, File, ArrowLeft, RefreshCw, Trash2, Play, Image, FileText,
    FileCode, FileAudio, Download, X, ChevronRight, Home, Search,
  } from 'lucide-svelte';
  import axios from 'axios';

  storeNavbar.title = 'Files';

  const api = axios.create({ baseURL: 'api/v1' });

  interface FileEntry {
    name: string;
    path: string;
    is_dir: boolean;
    size: number;
    mod_time: string;
  }

  let currentPath = $state('/tmp');
  let parentPath = $state('/');
  let entries = $state<FileEntry[]>([]);
  let loading = $state(false);
  let deleteConfirm = $state<string | null>(null);

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
  let previewType = $state<'video' | 'image' | 'audio' | 'text' | null>(null);
  let previewText = $state('');

  async function browse(path: string) {
    loading = true;
    try {
      const res = await api.get('/files/browse', { params: { path } });
      currentPath = res.data.path;
      parentPath = res.data.parent;
      entries = res.data.entries || [];
    } catch (e: any) {
      addToast(e?.response?.data || 'Failed to browse', 'alert');
    } finally {
      loading = false;
    }
  }

  async function handleDelete(path: string) {
    try {
      await api.delete('/files', { params: { path } });
      addToast('Deleted', 'info');
      deleteConfirm = null;
      if (previewFile?.path === path) closePreview();
      browse(currentPath);
    } catch (e: any) {
      addToast(e?.response?.data || 'Delete failed', 'alert');
    }
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

  function getFileIconColor(entry: FileEntry): string {
    if (entry.is_dir) return 'text-yellow-500';
    const type = getFileType(entry.name);
    if (type === 'video') return 'text-purple-500';
    if (type === 'image') return 'text-green-500';
    if (type === 'audio') return 'text-blue-500';
    if (type === 'text') return 'text-gray-500';
    return 'text-gray-400';
  }

  function formatSize(bytes: number): string {
    if (bytes === 0) return '-';
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
  }

  function serveUrl(path: string): string {
    return `api/v1/files/serve?path=${encodeURIComponent(path)}`;
  }

  async function openPreview(entry: FileEntry) {
    if (entry.is_dir) {
      browse(entry.path);
      return;
    }

    const type = getFileType(entry.name);
    if (type === 'video' || type === 'image' || type === 'audio') {
      previewFile = entry;
      previewType = type;
      previewText = '';
    } else if (type === 'text') {
      try {
        const res = await fetch(serveUrl(entry.path));
        previewText = await res.text();
        previewFile = entry;
        previewType = 'text';
      } catch {
        addToast('Cannot load file', 'alert');
      }
    }
  }

  function closePreview() {
    previewFile = null;
    previewType = null;
    previewText = '';
  }

  // Breadcrumb parts
  let breadcrumbs = $derived.by(() => {
    const parts = (currentPath || '/').split('/').filter(Boolean);
    const crumbs: { name: string; path: string }[] = [{ name: '/', path: '/' }];
    let acc = '';
    for (const part of parts) {
      acc += '/' + part;
      crumbs.push({ name: part, path: acc });
    }
    return crumbs;
  });

  $effect(() => {
    untrack(() => browse(currentPath));
  });
</script>

<div class="flex h-full">
  <!-- Main content -->
  <div class="flex-1 flex flex-col min-h-0">
    <!-- Header -->
    <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface shrink-0">
      <div class="flex items-center gap-2 min-w-0">
        <button
          onclick={() => browse(parentPath)}
          disabled={currentPath === '/'}
          class="p-1 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text disabled:opacity-30 transition-colors"
          title="Go up"
        >
          <ArrowLeft size={14} />
        </button>

        <!-- Breadcrumbs -->
        <div class="flex items-center gap-0.5 text-xs min-w-0 overflow-hidden">
          {#each breadcrumbs as crumb, i}
            {#if i > 0}
              <ChevronRight size={10} class="text-gray-300 dark:text-dark-text-faint shrink-0" />
            {/if}
            <button
              onclick={() => browse(crumb.path)}
              class="text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary truncate max-w-[120px] transition-colors"
            >
              {#if i === 0}
                <Home size={12} />
              {:else}
                {crumb.name}
              {/if}
            </button>
          {/each}
        </div>
      </div>

      <div class="flex items-center gap-1">
        <!-- Search -->
        <div class="relative">
          <Search size={12} class="absolute left-2 top-1/2 -translate-y-1/2 text-gray-400 dark:text-dark-text-muted" />
          <input
            type="text"
            bind:value={searchQuery}
            placeholder="Filter..."
            class="w-36 pl-7 pr-2 py-1 text-[11px] border border-gray-200 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:text-dark-text dark:placeholder:text-dark-text-muted"
          />
        </div>
        <!-- Quick nav buttons -->
        <button
          onclick={() => browse('/tmp')}
          class="px-2 py-1 text-[10px] text-gray-500 dark:text-dark-text-muted hover:bg-gray-100 dark:hover:bg-dark-elevated transition-colors rounded"
        >/tmp</button>
        <button
          onclick={() => browse('/tmp/at-sandbox')}
          class="px-2 py-1 text-[10px] text-gray-500 dark:text-dark-text-muted hover:bg-gray-100 dark:hover:bg-dark-elevated transition-colors rounded"
        >sandbox</button>
        <!-- Hidden files toggle -->
        <button
          onclick={() => { showHidden = !showHidden; }}
          class="px-2 py-1 text-[10px] transition-colors rounded {showHidden ? 'bg-gray-900 dark:bg-accent text-white' : 'text-gray-400 dark:text-dark-text-muted hover:bg-gray-100 dark:hover:bg-dark-elevated'}"
          title={showHidden ? 'Hide dotfiles' : 'Show dotfiles'}
        >.hidden</button>
        <button
          onclick={() => browse(currentPath)}
          class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary transition-colors"
          title="Refresh"
        >
          <RefreshCw size={13} />
        </button>
      </div>
    </div>

    <!-- File list -->
    <div class="flex-1 overflow-y-auto">
      {#if loading}
        <div class="text-center py-12 text-sm text-gray-400 dark:text-dark-text-muted">Loading...</div>
      {:else if filteredEntries.length === 0}
        <div class="text-center py-12 text-sm text-gray-400 dark:text-dark-text-muted">
          {entries.length > 0 ? 'No matches' : 'Empty directory'}
        </div>
      {:else}
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base text-xs text-gray-500 dark:text-dark-text-muted">
              <th class="text-left px-4 py-2 font-medium">
                <button onclick={() => toggleSort('name')} class="hover:text-gray-700 dark:hover:text-dark-text-secondary">Name{sortIndicator('name')}</button>
              </th>
              <th class="text-right px-4 py-2 font-medium w-24">
                <button onclick={() => toggleSort('size')} class="hover:text-gray-700 dark:hover:text-dark-text-secondary">Size{sortIndicator('size')}</button>
              </th>
              <th class="text-right px-4 py-2 font-medium w-40">
                <button onclick={() => toggleSort('mod_time')} class="hover:text-gray-700 dark:hover:text-dark-text-secondary">Modified{sortIndicator('mod_time')}</button>
              </th>
              <th class="text-right px-4 py-2 font-medium w-20"></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-border">
            {#each filteredEntries as entry}
              {@const FileIcon = getFileIcon(entry)}
              <tr class="hover:bg-gray-50 dark:hover:bg-dark-elevated/50 transition-colors group">
                <td class="px-4 py-2">
                  <button
                    onclick={() => openPreview(entry)}
                    class="flex items-center gap-2 text-left hover:text-blue-600 dark:hover:text-accent-text transition-colors w-full"
                  >
                    <FileIcon size={14} class={getFileIconColor(entry)} />
                    <span class="truncate">{entry.name}</span>
                  </button>
                </td>
                <td class="px-4 py-2 text-right text-xs text-gray-400 dark:text-dark-text-muted font-mono">
                  {entry.is_dir ? '-' : formatSize(entry.size)}
                </td>
                <td class="px-4 py-2 text-right text-xs text-gray-400 dark:text-dark-text-muted">
                  {entry.mod_time}
                </td>
                <td class="px-4 py-2 text-right">
                  <div class="flex items-center justify-end gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity">
                    {#if !entry.is_dir}
                      <a
                        href={serveUrl(entry.path)}
                        download={entry.name}
                        class="p-1 text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text transition-colors"
                        title="Download"
                      >
                        <Download size={12} />
                      </a>
                    {/if}
                    {#if deleteConfirm === entry.path}
                      <button
                        onclick={() => handleDelete(entry.path)}
                        class="px-1.5 py-0.5 text-[10px] bg-red-600 text-white hover:bg-red-700 rounded transition-colors"
                      >Yes</button>
                      <button
                        onclick={() => (deleteConfirm = null)}
                        class="px-1.5 py-0.5 text-[10px] border border-gray-300 dark:border-dark-border-subtle text-gray-500 hover:bg-gray-100 dark:hover:bg-dark-elevated rounded transition-colors"
                      >No</button>
                    {:else}
                      <button
                        onclick={() => (deleteConfirm = entry.path)}
                        class="p-1 text-gray-400 hover:text-red-500 dark:text-dark-text-muted dark:hover:text-red-400 transition-colors"
                        title="Delete"
                      >
                        <Trash2 size={12} />
                      </button>
                    {/if}
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
    <div class="w-[500px] border-l border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface flex flex-col shrink-0">
      <!-- Preview header -->
      <div class="flex items-center justify-between px-4 py-2 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50 shrink-0">
        <div class="min-w-0">
          <div class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary truncate">{previewFile.name}</div>
          <div class="text-[10px] text-gray-400 dark:text-dark-text-muted">{formatSize(previewFile.size)} · {previewFile.mod_time}</div>
        </div>
        <div class="flex items-center gap-1 shrink-0">
          <a
            href={serveUrl(previewFile.path)}
            download={previewFile.name}
            class="p-1 text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text transition-colors"
            title="Download"
          >
            <Download size={14} />
          </a>
          <button
            onclick={() => { deleteConfirm = previewFile?.path ?? null; }}
            class="p-1 text-gray-400 hover:text-red-500 dark:text-dark-text-muted dark:hover:text-red-400 transition-colors"
            title="Delete"
          >
            <Trash2 size={14} />
          </button>
          <button
            onclick={closePreview}
            class="p-1 text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text transition-colors"
          >
            <X size={14} />
          </button>
        </div>
      </div>

      <!-- Preview content -->
      <div class="flex-1 overflow-auto p-4 flex items-start justify-center bg-gray-50 dark:bg-dark-base">
        {#if previewType === 'video'}
          <!-- svelte-ignore a11y_media_has_caption -->
          <video
            src={serveUrl(previewFile.path)}
            controls
            class="max-w-full max-h-full rounded shadow-lg"
            autoplay
          ></video>
        {:else if previewType === 'image'}
          <img
            src={serveUrl(previewFile.path)}
            alt={previewFile.name}
            class="max-w-full max-h-full object-contain rounded shadow-lg"
          />
        {:else if previewType === 'audio'}
          <div class="w-full pt-8">
            <div class="text-center mb-4">
              <FileAudio size={48} class="mx-auto text-blue-400 mb-2" />
              <div class="text-sm text-gray-600 dark:text-dark-text-secondary">{previewFile.name}</div>
            </div>
            <!-- svelte-ignore a11y_media_has_caption -->
            <audio
              src={serveUrl(previewFile.path)}
              controls
              class="w-full"
              autoplay
            ></audio>
          </div>
        {:else if previewType === 'text'}
          <pre class="w-full text-xs font-mono text-gray-700 dark:text-dark-text-secondary whitespace-pre-wrap break-all bg-white dark:bg-dark-surface p-3 border border-gray-200 dark:border-dark-border rounded">{previewText}</pre>
        {/if}
      </div>
    </div>
  {/if}
</div>
