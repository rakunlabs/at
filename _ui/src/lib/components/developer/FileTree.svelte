<script lang="ts">
  import { ChevronRight, File, Folder, FolderOpen, Link2, LoaderCircle } from 'lucide-svelte';
  import type { DeveloperFileEntry } from '@/lib/api/developer-spaces';
  import FileTree from './FileTree.svelte';

  interface Props {
    /** Loaded directory listings keyed by folder path ("" is the space root). */
    listings: Record<string, DeveloperFileEntry[] | undefined>;
    expanded: Record<string, boolean>;
    loading: Record<string, boolean>;
    folder: string;
    depth?: number;
    selected: string;
    /** Paths with unsaved edits, shown with a dot. */
    dirty: Set<string>;
    /** Paths changed in Git, shown tinted. */
    changed: Set<string>;
    ontoggle: (path: string) => void;
    onopen: (entry: DeveloperFileEntry) => void;
    onmenu: (event: MouseEvent, entry: DeveloperFileEntry) => void;
    ondropfiles: (folder: string, files: FileList) => void;
  }
  let { listings, expanded, loading, folder, depth = 0, selected, dirty, changed, ontoggle, onopen, onmenu, ondropfiles }: Props = $props();

  const entries = $derived(listings[folder] ?? []);
  let dropTarget = $state('');

  function drop(event: DragEvent, target: string) {
    event.preventDefault();
    event.stopPropagation();
    dropTarget = '';
    if (event.dataTransfer?.files.length) ondropfiles(target, event.dataTransfer.files);
  }
</script>

<ul role={depth === 0 ? 'tree' : 'group'} class="min-w-0">
  {#each entries as entry (entry.path)}
    {@const isDir = entry.type === 'dir'}
    <li role="treeitem" aria-expanded={isDir ? !!expanded[entry.path] : undefined} aria-selected={selected === entry.path}>
      <button
        type="button"
        class={[
          'flex w-full min-w-0 items-center gap-1 py-[3px] pr-2 text-left text-[13px] focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent',
          selected === entry.path ? 'bg-gray-200 dark:bg-dark-elevated text-gray-900 dark:text-dark-text' : 'text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated/60',
          dropTarget === entry.path ? 'outline outline-1 -outline-offset-1 outline-accent' : '',
        ]}
        style={`padding-left:${depth * 12 + 6}px`}
        title={entry.path}
        onclick={() => (isDir ? ontoggle(entry.path) : onopen(entry))}
        oncontextmenu={event => { event.preventDefault(); onmenu(event, entry); }}
        ondragover={event => { if (isDir) { event.preventDefault(); dropTarget = entry.path; } }}
        ondragleave={() => { if (dropTarget === entry.path) dropTarget = ''; }}
        ondrop={event => { if (isDir) drop(event, entry.path); }}
      >
        {#if isDir}
          <ChevronRight size={13} class={`shrink-0 text-gray-400 ${expanded[entry.path] ? 'rotate-90' : ''}`} />
          {#if loading[entry.path]}
            <LoaderCircle size={14} class="shrink-0 animate-spin text-gray-400 motion-reduce:animate-none" />
          {:else if expanded[entry.path]}
            <FolderOpen size={14} class="shrink-0 text-amber-600 dark:text-amber-400" />
          {:else}
            <Folder size={14} class="shrink-0 text-amber-600 dark:text-amber-400" />
          {/if}
        {:else}
          <span class="w-[13px] shrink-0"></span>
          {#if entry.type === 'symlink'}
            <Link2 size={14} class="shrink-0 text-gray-400" />
          {:else}
            <File size={14} class="shrink-0 text-gray-400" />
          {/if}
        {/if}
        <span class={['min-w-0 flex-1 truncate', changed.has(entry.path) ? 'text-amber-700 dark:text-amber-300' : '']}>{entry.name}</span>
        {#if dirty.has(entry.path)}<span class="size-1.5 shrink-0 rounded-full bg-gray-500 dark:bg-dark-text-secondary" title="Unsaved changes"></span>{/if}
      </button>
      {#if isDir && expanded[entry.path]}
        <FileTree {listings} {expanded} {loading} folder={entry.path} depth={depth + 1} {selected} {dirty} {changed} {ontoggle} {onopen} {onmenu} {ondropfiles} />
      {/if}
    </li>
  {:else}
    {#if depth > 0 && listings[folder]}
      <li class="py-[3px] text-xs italic text-gray-400 dark:text-dark-text-muted" style={`padding-left:${depth * 12 + 25}px`}>Empty</li>
    {/if}
  {/each}
</ul>
