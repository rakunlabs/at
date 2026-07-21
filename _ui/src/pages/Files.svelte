<script lang="ts">
  import { untrack } from 'svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    Folder, File, ArrowLeft, RefreshCw, Trash2, Play, Image, FileText,
    FileCode, FileAudio, Download, X, ChevronRight, Home, Search,
  } from 'lucide-svelte';
  import axios from 'axios';
  import { getInfo } from '@/lib/api/gateway';

  storeNavbar.title = 'Files';

  const api = axios.create({ baseURL: 'api/v1' });

  interface FileEntry {
    name: string;
    path: string;
    is_dir: boolean;
    size: number;
    mod_time: string;
  }

  // The file API operates on the daemon's full filesystem (no allow-list).
  // The backend defaults `GET /files/browse` (no path) to the configured
  // task-workspace root (loopgov.WorkspaceRoot), so we start with empty
  // strings and let the server tell us where we landed. The actual path
  // is filled in by the first `browse('')` call below.
  let currentPath = $state('');
  let parentPath = $state('');
  // Free-text path input so the user can jump anywhere on the host.
  let pathInput = $state('');
  // Effective workspace root reported by the server (GET /api/v1/info).
  // Used for the "tasks" quick-nav button. Falls back to /tmp/at-tasks
  // until the info request returns.
  let workspaceRoot = $state('/tmp/at-tasks');
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
      pathInput = res.data.path;
      entries = res.data.entries || [];
    } catch (e: any) {
      addToast(e?.response?.data || 'Failed to browse', 'alert');
    } finally {
      loading = false;
    }
  }

  function goToPathInput(e: KeyboardEvent) {
    if (e.key === 'Enter' && pathInput.trim()) {
      browse(pathInput.trim());
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
    if (entry.is_dir) return 'text-amber-600 bg-amber-100 ring-amber-200/70 dark:text-amber-300 dark:bg-amber-500/10 dark:ring-amber-500/20';
    const type = getFileType(entry.name);
    if (type === 'video') return 'text-violet-600 bg-violet-100 ring-violet-200/70 dark:text-violet-300 dark:bg-violet-500/10 dark:ring-violet-500/20';
    if (type === 'image') return 'text-emerald-600 bg-emerald-100 ring-emerald-200/70 dark:text-emerald-300 dark:bg-emerald-500/10 dark:ring-emerald-500/20';
    if (type === 'audio') return 'text-sky-600 bg-sky-100 ring-sky-200/70 dark:text-sky-300 dark:bg-sky-500/10 dark:ring-sky-500/20';
    if (type === 'text') return 'text-cyan-700 bg-cyan-100 ring-cyan-200/70 dark:text-cyan-300 dark:bg-cyan-500/10 dark:ring-cyan-500/20';
    return 'text-slate-500 bg-slate-100 ring-slate-200/70 dark:text-dark-text-muted dark:bg-white/5 dark:ring-white/10';
  }

  function formatSize(bytes: number): string {
    if (bytes === 0) return '-';
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
  }

  function serveUrl(path: string, modTime?: string): string {
    // Append the file's mod_time as a cache-buster so re-running a task that
    // overwrites the same path (e.g. a regenerated video at /tmp/at-tasks/<id>/final.mp4)
    // forces the browser to re-fetch instead of showing the stale cached
    // response. The server now sets Last-Modified via http.ServeContent, but
    // browsers still aggressively cache same-URL media; bumping the query
    // string is the simplest way to invalidate.
    const base = `api/v1/files/serve?path=${encodeURIComponent(path)}`;
    if (modTime) return `${base}&t=${encodeURIComponent(modTime)}`;
    return base;
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
        const res = await fetch(serveUrl(entry.path, entry.mod_time));
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

  // On mount: fetch the effective workspace root from the server, then
  // browse it. We only run once — `untrack` keeps this from re-firing on
  // every state change.
  $effect(() => {
    untrack(async () => {
      try {
        const info = await getInfo();
        if (info.workspace_root) {
          workspaceRoot = info.workspace_root;
        }
      } catch {
        // Non-fatal: keep the /tmp/at-tasks fallback.
      }
      // browse('') asks the backend for its configured default; the
      // server will resolve and return the actual path in `res.data.path`.
      browse(currentPath);
    });
  });
</script>

<div class="flex h-full bg-[#edf3f6] dark:bg-[#111719]">
  <!-- Main content -->
  <div class="flex-1 flex flex-col min-h-0">
    <!-- Header -->
    <div class="flex items-center justify-between px-4 py-3 border-b border-sky-200/80 dark:border-cyan-950 bg-[#f9fcfd] dark:bg-[#182023] shrink-0 shadow-sm">
      <div class="flex items-center gap-2 min-w-0">
        <button
          onclick={() => browse(parentPath)}
          disabled={currentPath === '/'}
          class="p-1 hover:bg-sky-100 dark:hover:bg-cyan-950/50 text-slate-400 hover:text-sky-700 dark:text-dark-text-muted dark:hover:text-cyan-300 disabled:opacity-30 transition-colors"
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
              class={[
                'truncate max-w-[120px] transition-colors',
                i === breadcrumbs.length - 1
                  ? 'text-sky-700 dark:text-cyan-300 font-medium'
                  : 'text-slate-500 dark:text-dark-text-muted hover:text-sky-700 dark:hover:text-cyan-300'
              ]}
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
        <!-- Free-text path input. No allow-list — anywhere the daemon can read. -->
        <input
          type="text"
          bind:value={pathInput}
          onkeydown={goToPathInput}
          placeholder="/path/to/dir"
          class="w-56 px-2 py-1 text-[11px] font-mono border border-sky-200 dark:border-cyan-950 bg-white dark:bg-[#111719] rounded focus:outline-none focus:ring-1 focus:ring-sky-400 dark:focus:ring-cyan-700 dark:text-dark-text dark:placeholder:text-dark-text-muted"
        />
        <!-- Search -->
        <div class="relative">
          <Search size={12} class="absolute left-2 top-1/2 -translate-y-1/2 text-gray-400 dark:text-dark-text-muted" />
          <input
            type="text"
            bind:value={searchQuery}
            placeholder="Filter..."
            class="w-36 pl-7 pr-2 py-1 text-[11px] border border-sky-200 dark:border-cyan-950 bg-white dark:bg-[#111719] rounded focus:outline-none focus:ring-1 focus:ring-sky-400 dark:focus:ring-cyan-700 dark:text-dark-text dark:placeholder:text-dark-text-muted"
          />
        </div>
        <!-- Quick nav shortcuts to common agent workspace roots.
             "tasks" jumps to the configured loopgov.WorkspaceRoot
             (defaults to /tmp/at-tasks). -->
        <button
          onclick={() => browse(workspaceRoot)}
          class="px-2 py-1 text-[10px] border border-sky-200/80 dark:border-cyan-900/60 bg-sky-50 dark:bg-cyan-950/20 text-sky-700 dark:text-cyan-300 hover:bg-sky-100 dark:hover:bg-cyan-950/50 transition-colors rounded"
          title={workspaceRoot}
        >tasks</button>
        <button
          onclick={() => browse('/tmp/at-sandbox')}
          class="px-2 py-1 text-[10px] border border-sky-200/80 dark:border-cyan-900/60 bg-sky-50 dark:bg-cyan-950/20 text-sky-700 dark:text-cyan-300 hover:bg-sky-100 dark:hover:bg-cyan-950/50 transition-colors rounded"
        >sandbox</button>
        <button
          onclick={() => browse('/tmp/at-audio')}
          class="px-2 py-1 text-[10px] border border-sky-200/80 dark:border-cyan-900/60 bg-sky-50 dark:bg-cyan-950/20 text-sky-700 dark:text-cyan-300 hover:bg-sky-100 dark:hover:bg-cyan-950/50 transition-colors rounded"
        >audio</button>
        <button
          onclick={() => browse('/tmp/at-git-cache')}
          class="px-2 py-1 text-[10px] border border-sky-200/80 dark:border-cyan-900/60 bg-sky-50 dark:bg-cyan-950/20 text-sky-700 dark:text-cyan-300 hover:bg-sky-100 dark:hover:bg-cyan-950/50 transition-colors rounded"
        >git-cache</button>
        <button
          onclick={() => browse('/')}
          class="px-2 py-1 text-[10px] border border-sky-200/80 dark:border-cyan-900/60 bg-sky-50 dark:bg-cyan-950/20 text-sky-700 dark:text-cyan-300 hover:bg-sky-100 dark:hover:bg-cyan-950/50 transition-colors rounded"
        >/</button>
        <!-- Hidden files toggle -->
        <button
          onclick={() => { showHidden = !showHidden; }}
          class="px-2 py-1 text-[10px] border transition-colors rounded {showHidden ? 'border-cyan-700 bg-cyan-700 dark:border-accent dark:bg-accent text-white' : 'border-sky-200/80 dark:border-cyan-900/60 text-slate-400 dark:text-dark-text-muted hover:bg-sky-100 dark:hover:bg-cyan-950/50'}"
          title={showHidden ? 'Hide dotfiles' : 'Show dotfiles'}
        >.hidden</button>
        <button
          onclick={() => browse(currentPath)}
          class="p-1.5 hover:bg-sky-100 dark:hover:bg-cyan-950/50 text-slate-400 hover:text-sky-700 dark:text-dark-text-muted dark:hover:text-cyan-300 transition-colors"
          title="Refresh"
        >
          <RefreshCw size={13} />
        </button>
      </div>
    </div>

    <!-- File list -->
    <div class="flex-1 overflow-y-auto bg-[#edf3f6] dark:bg-[#111719]">
      {#if loading}
        <div class="text-center py-12 text-sm text-gray-400 dark:text-dark-text-muted">Loading...</div>
      {:else if filteredEntries.length === 0}
        <div class="text-center py-12 text-sm text-gray-400 dark:text-dark-text-muted">
          {entries.length > 0 ? 'No matches' : 'Empty directory'}
        </div>
      {:else}
        <div class="m-3 overflow-hidden border border-sky-200/80 dark:border-cyan-950 bg-white dark:bg-[#182023] shadow-sm">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-sky-200/80 dark:border-cyan-950 bg-sky-50/80 dark:bg-cyan-950/20 text-xs text-slate-500 dark:text-dark-text-muted">
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
          <tbody class="divide-y divide-sky-100 dark:divide-cyan-950/70">
            {#each filteredEntries as entry}
              {@const FileIcon = getFileIcon(entry)}
              <tr class={[
                'transition-colors group',
                previewFile?.path === entry.path
                  ? 'bg-sky-100/80 dark:bg-cyan-950/40'
                  : entry.is_dir
                    ? 'bg-amber-50/25 hover:bg-amber-50/70 dark:bg-amber-500/[0.02] dark:hover:bg-amber-500/[0.07]'
                    : 'hover:bg-sky-50/70 dark:hover:bg-cyan-950/25'
              ]}>
                <td class="px-4 py-2">
                  <button
                    onclick={() => openPreview(entry)}
                    class="flex items-center gap-2 text-left hover:text-blue-600 dark:hover:text-accent-text transition-colors w-full"
                  >
                    <span class={["inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md ring-1", getFileIconColor(entry)]}>
                      <FileIcon size={14} />
                    </span>
                    <span class="truncate text-slate-700 dark:text-dark-text-secondary">{entry.name}</span>
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
                        href={serveUrl(entry.path, entry.mod_time)}
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
        </div>
      {/if}
    </div>
  </div>

  <!-- Preview panel -->
  {#if previewFile}
    <div class="w-[500px] border-l border-sky-200 dark:border-cyan-950 bg-white dark:bg-[#182023] flex flex-col shrink-0 shadow-[-8px_0_24px_rgba(14,116,144,0.06)]">
      <!-- Preview header -->
      <div class="flex items-center justify-between px-4 py-2 border-b border-sky-200/80 dark:border-cyan-950 bg-sky-50/80 dark:bg-cyan-950/20 shrink-0">
        <div class="min-w-0">
          <div class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary truncate">{previewFile.name}</div>
          <div class="text-[10px] text-gray-400 dark:text-dark-text-muted">{formatSize(previewFile.size)} · {previewFile.mod_time}</div>
        </div>
        <div class="flex items-center gap-1 shrink-0">
          <a
            href={serveUrl(previewFile.path, previewFile.mod_time)}
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
      <div class="flex-1 overflow-auto p-4 flex items-start justify-center bg-[#edf3f6] dark:bg-[#111719]">
        {#if previewType === 'video'}
          <!-- svelte-ignore a11y_media_has_caption -->
          <video
            src={serveUrl(previewFile.path, previewFile.mod_time)}
            controls
            preload="metadata"
            class="max-w-full max-h-full rounded shadow-lg"
            autoplay
          ></video>
        {:else if previewType === 'image'}
          <img
            src={serveUrl(previewFile.path, previewFile.mod_time)}
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
              src={serveUrl(previewFile.path, previewFile.mod_time)}
              controls
              preload="metadata"
              class="w-full"
              autoplay
            ></audio>
          </div>
        {:else if previewType === 'text'}
          <pre class="w-full text-xs font-mono text-slate-700 dark:text-dark-text-secondary whitespace-pre-wrap break-all bg-white dark:bg-[#182023] p-3 border border-sky-200 dark:border-cyan-950 rounded shadow-sm">{previewText}</pre>
        {/if}
      </div>
    </div>
  {/if}
</div>
