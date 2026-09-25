<script lang="ts">
  import { untrack } from 'svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    deleteSkillFile,
    listSkillFiles,
    putSkillFiles,
    type Skill,
    type SkillFile,
  } from '@/lib/api/skills';
  import { isMarkdownPath } from '@/lib/helper/skill-files';
  import { Code, Eye, File, FilePlus, Folder, FolderOpen, Save, Trash2, Upload, X } from 'lucide-svelte';
  import SkillFilePreview from './SkillFilePreview.svelte';

  interface Props {
    skill: Skill;
    onclose: () => void;
    onchanged?: () => void;
    /** File to select on open; falls back to SKILL.md when absent. */
    initialPath?: string;
  }

  let { skill, onclose, onchanged = () => {}, initialPath }: Props = $props();
  let files = $state<SkillFile[]>([]);
  let selectedPath = $state(untrack(() => initialPath) || 'SKILL.md');
  let editorContent = $state('');
  let savedContent = $state('');
  let loading = $state(true);
  let saving = $state(false);
  let dragging = $state(false);
  let newPath = $state('');
  let showNewFile = $state(false);
  // Markdown opens rendered, everything else as editable source. Preview
  // always reflects the editor, including unsaved changes.
  let view = $state<'raw' | 'preview'>('preview');
  let fileInput: HTMLInputElement;
  let folderInput: HTMLInputElement;

  let selectedFile = $derived(files.find((file) => file.path === selectedPath));
  let dirty = $derived(editorContent !== savedContent);
  let treeRows = $derived(buildTreeRows(files));

  interface TreeRow {
    kind: 'directory' | 'file';
    path: string;
    name: string;
    depth: number;
  }

  function buildTreeRows(items: SkillFile[]): TreeRow[] {
    const directories = new Set<string>();
    for (const item of items) {
      const parts = item.path.split('/');
      for (let i = 1; i < parts.length; i++) directories.add(parts.slice(0, i).join('/'));
    }
    const rows: TreeRow[] = [];
    for (const directory of directories) {
      const parts = directory.split('/');
      rows.push({ kind: 'directory', path: directory, name: parts.at(-1) || directory, depth: parts.length - 1 });
    }
    for (const item of items) {
      const parts = item.path.split('/');
      rows.push({ kind: 'file', path: item.path, name: parts.at(-1) || item.path, depth: parts.length - 1 });
    }
    return rows.sort((a, b) => {
      if (a.path === 'SKILL.md') return -1;
      if (b.path === 'SKILL.md') return 1;
      const aPrefix = a.kind === 'directory' ? `${a.path}/` : a.path;
      const bPrefix = b.kind === 'directory' ? `${b.path}/` : b.path;
      return aPrefix.localeCompare(bPrefix);
    });
  }

  async function load() {
    loading = true;
    try {
      files = await listSkillFiles(skill.id);
      selectFile(files.find((file) => file.path === selectedPath) || files[0]);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load skill files', 'alert');
    } finally {
      loading = false;
    }
  }

  function selectFile(file?: SkillFile) {
    if (!file) return;
    if (dirty && !confirm('Discard unsaved changes?')) return;
    if (file.path !== selectedPath || !editorContent) view = isMarkdownPath(file.path) && file.content.trim() ? 'preview' : 'raw';
    selectedPath = file.path;
    editorContent = file.content;
    savedContent = file.content;
  }

  async function saveCurrent() {
    if (!selectedFile) return;
    saving = true;
    try {
      await putSkillFiles(skill.id, [{ ...selectedFile, content: editorContent }]);
      savedContent = editorContent;
      files = files.map((file) => file.path === selectedPath ? { ...file, content: editorContent } : file);
      addToast(`${selectedPath} saved`);
      onchanged();
      if (selectedPath === 'SKILL.md') await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save file', 'alert');
    } finally {
      saving = false;
    }
  }

  async function createFile() {
    const path = newPath.trim().replaceAll('\\', '/').replace(/^\/+/, '');
    if (!path) return;
    if (files.some((file) => file.path === path) && !confirm(`${path} already exists. Replace it?`)) return;
    try {
      await putSkillFiles(skill.id, [{ path, content: '' }]);
      showNewFile = false;
      newPath = '';
      await load();
      selectFile(files.find((file) => file.path === path));
      addToast(`${path} created`);
      onchanged();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to create file', 'alert');
    }
  }

  async function removeCurrent() {
    if (!selectedFile || selectedPath === 'SKILL.md') return;
    if (!confirm(`Delete ${selectedPath}?`)) return;
    try {
      await deleteSkillFile(skill.id, selectedPath);
      addToast(`${selectedPath} deleted`);
      selectedPath = 'SKILL.md';
      await load();
      onchanged();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete file', 'alert');
    }
  }

  async function decodeText(file: globalThis.File): Promise<string> {
    const bytes = await file.arrayBuffer();
    if (bytes.byteLength > 256 * 1024 && file.name.toLowerCase() !== 'skill.md') {
      throw new Error(`${file.name} is larger than 256 KiB`);
    }
    try {
      const content = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
      if (content.includes('\0')) throw new Error('binary');
      return content;
    } catch {
      throw new Error(`${file.name} is not a UTF-8 text file`);
    }
  }

  async function uploadFiles(uploaded: Array<{ file: globalThis.File; path: string }>) {
    if (!uploaded.length) return;
    const replacements = uploaded.filter((item) => files.some((file) => file.path === item.path));
    if (replacements.length && !confirm(`${replacements.length} existing file(s) will be replaced. Continue?`)) return;
    saving = true;
    try {
      const payload: SkillFile[] = [];
      for (const item of uploaded) {
        payload.push({ path: item.path, content: await decodeText(item.file), media_type: item.file.type || undefined });
      }
      await putSkillFiles(skill.id, payload);
      addToast(`${payload.length} file${payload.length === 1 ? '' : 's'} uploaded`);
      await load();
      onchanged();
    } catch (e: any) {
      addToast(e?.response?.data?.message || e?.message || 'Failed to upload files', 'alert');
    } finally {
      saving = false;
    }
  }

  function fromInput(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const selected = Array.from(input.files || []).map((file) => ({
      file,
      path: file.webkitRelativePath || file.name,
    }));
    void uploadFiles(selected);
    input.value = '';
  }

  async function filesFromEntry(entry: any, prefix = ''): Promise<Array<{ file: globalThis.File; path: string }>> {
    if (entry.isFile) {
      const file = await new Promise<globalThis.File>((resolve, reject) => entry.file(resolve, reject));
      return [{ file, path: `${prefix}${file.name}` }];
    }
    if (!entry.isDirectory) return [];
    const entries: any[] = [];
    const reader = entry.createReader();
    while (true) {
      const batch = await new Promise<any[]>((resolve, reject) => reader.readEntries(resolve, reject));
      if (!batch.length) break;
      entries.push(...batch);
    }
    const nested = await Promise.all(entries.map((child) => filesFromEntry(child, `${prefix}${entry.name}/`)));
    return nested.flat();
  }

  async function handleDrop(event: DragEvent) {
    event.preventDefault();
    dragging = false;
    const items = Array.from(event.dataTransfer?.items || []);
    const entries = items.map((item: any) => item.webkitGetAsEntry?.()).filter(Boolean);
    if (entries.length) {
      const nested = await Promise.all(entries.map((entry) => filesFromEntry(entry)));
      await uploadFiles(nested.flat());
      return;
    }
    await uploadFiles(Array.from(event.dataTransfer?.files || []).map((file) => ({ file, path: file.name })));
  }

  function close() {
    if (dirty && !confirm('Discard unsaved changes?')) return;
    onclose();
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') close();
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 's') {
      event.preventDefault();
      void saveCurrent();
    }
  }

  load();
</script>

<svelte:window onkeydown={handleKeydown} />

<div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="presentation" onclick={(event) => { if (event.currentTarget === event.target) close(); }}>
  <div class="flex h-[min(46rem,calc(100dvh-2rem))] w-full max-w-6xl flex-col border border-gray-200 bg-white shadow-xl dark:border-dark-border dark:bg-dark-surface" role="dialog" aria-modal="true" aria-label={`${skill.name} files`}>
    <div class="flex items-center justify-between border-b border-gray-200 bg-gray-50 px-4 py-3 dark:border-dark-border dark:bg-dark-base">
      <div class="flex min-w-0 items-center gap-2">
        <FolderOpen size={16} class="shrink-0 text-gray-500 dark:text-dark-text-muted" />
        <div class="truncate text-sm font-medium text-gray-900 dark:text-dark-text">{skill.name}</div>
        <span class="text-xs text-gray-400 dark:text-dark-text-muted">skill folder</span>
      </div>
      <button type="button" onclick={close} class="p-1.5 text-gray-400 hover:bg-gray-200 hover:text-gray-700 dark:text-dark-text-muted dark:hover:bg-dark-elevated dark:hover:text-dark-text"><X size={15} /></button>
    </div>

    <div class="grid min-h-0 flex-1 grid-cols-1 md:grid-cols-[17rem_minmax(0,1fr)]">
      <aside class="flex min-h-0 flex-col border-b border-gray-200 dark:border-dark-border md:border-b-0 md:border-r">
        <div class="flex items-center gap-1 border-b border-gray-200 px-2 py-2 dark:border-dark-border">
          <button type="button" onclick={() => fileInput.click()} class="flex items-center gap-1 px-2 py-1 text-xs text-gray-600 hover:bg-gray-100 dark:text-dark-text-secondary dark:hover:bg-dark-elevated" title="Upload files"><Upload size={12} /> Files</button>
          <button type="button" onclick={() => folderInput.click()} class="flex items-center gap-1 px-2 py-1 text-xs text-gray-600 hover:bg-gray-100 dark:text-dark-text-secondary dark:hover:bg-dark-elevated" title="Upload a folder"><Folder size={12} /> Folder</button>
          <button type="button" onclick={() => showNewFile = !showNewFile} class="ml-auto p-1.5 text-gray-500 hover:bg-gray-100 dark:text-dark-text-muted dark:hover:bg-dark-elevated" title="New file"><FilePlus size={13} /></button>
          <input bind:this={fileInput} class="hidden" type="file" multiple onchange={fromInput} />
          <input bind:this={folderInput} class="hidden" type="file" multiple webkitdirectory={true} onchange={fromInput} />
        </div>
        {#if showNewFile}
          <form class="flex gap-1 border-b border-gray-200 p-2 dark:border-dark-border" onsubmit={(event) => { event.preventDefault(); void createFile(); }}>
            <input bind:value={newPath} class="min-w-0 flex-1 border border-gray-300 bg-white px-2 py-1 text-xs dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text" placeholder="references/notes.md" />
            <button class="bg-gray-900 px-2 py-1 text-xs text-white dark:bg-accent" type="submit">Add</button>
          </form>
        {/if}
        <div
          class={['min-h-0 flex-1 overflow-y-auto p-2', dragging ? 'bg-blue-50 dark:bg-accent-muted' : '']}
          role="region"
          aria-label="Skill files and upload drop zone"
          ondragover={(event) => { event.preventDefault(); dragging = true; }}
          ondragleave={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node)) dragging = false; }}
          ondrop={handleDrop}
        >
          {#if loading}
            <div class="p-3 text-xs text-gray-400">Loading...</div>
          {:else}
            {#each treeRows as row}
              {#if row.kind === 'directory'}
                <div class="flex items-center gap-1.5 py-1 text-xs font-medium text-gray-500 dark:text-dark-text-muted" style={`padding-left:${row.depth * 14 + 6}px`}><Folder size={13} /> {row.name}</div>
              {:else}
                <button type="button" onclick={() => selectFile(files.find((file) => file.path === row.path))} class={['flex w-full items-center gap-1.5 py-1 text-left text-xs', selectedPath === row.path ? 'bg-gray-100 text-gray-900 dark:bg-dark-elevated dark:text-dark-text' : 'text-gray-600 hover:bg-gray-50 dark:text-dark-text-secondary dark:hover:bg-dark-base']} style={`padding-left:${row.depth * 14 + 6}px`} title={row.path}>
                  <File size={13} class="shrink-0" /><span class="truncate">{row.name}</span>
                </button>
              {/if}
            {/each}
            <div class={['mt-3 border border-dashed p-3 text-center text-[11px]', dragging ? 'border-blue-400 text-blue-600' : 'border-gray-300 text-gray-400 dark:border-dark-border-subtle dark:text-dark-text-muted']}>
              Drop UTF-8 files or folders here<br />Same paths are replaced
            </div>
          {/if}
        </div>
      </aside>

      <section class="flex min-h-0 flex-col">
        <div class="flex items-center justify-between border-b border-gray-200 px-3 py-2 dark:border-dark-border">
          <div class="min-w-0 font-mono text-xs text-gray-600 dark:text-dark-text-secondary">{selectedPath}{dirty ? ' • modified' : ''}</div>
          <div class="flex items-center gap-1">
            {#if selectedFile}
              <div class="mr-1 flex border border-gray-300 dark:border-dark-border-subtle" role="group" aria-label="File view">
                <button type="button" onclick={() => view = 'raw'} aria-pressed={view === 'raw'} title="Edit the source" class={['flex items-center gap-1 px-2 py-1 text-xs', view === 'raw' ? 'bg-gray-900 text-white dark:bg-accent dark:text-gray-950' : 'text-gray-600 hover:bg-gray-100 dark:text-dark-text-secondary dark:hover:bg-dark-elevated']}><Code size={12} /> Raw</button>
                <button type="button" onclick={() => view = 'preview'} aria-pressed={view === 'preview'} title={isMarkdownPath(selectedPath) ? 'Rendered markdown' : 'Read-only with syntax highlighting'} class={['flex items-center gap-1 border-l border-gray-300 px-2 py-1 text-xs dark:border-dark-border-subtle', view === 'preview' ? 'bg-gray-900 text-white dark:bg-accent dark:text-gray-950' : 'text-gray-600 hover:bg-gray-100 dark:text-dark-text-secondary dark:hover:bg-dark-elevated']}><Eye size={12} /> Preview</button>
              </div>
            {/if}
            {#if selectedPath !== 'SKILL.md'}
              <button type="button" onclick={removeCurrent} class="p-1.5 text-red-500 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20" title="Delete file"><Trash2 size={14} /></button>
            {/if}
            <button type="button" onclick={saveCurrent} disabled={!dirty || saving} class="flex items-center gap-1 bg-gray-900 px-2.5 py-1.5 text-xs font-medium text-white disabled:opacity-40 dark:bg-accent"><Save size={12} />{saving ? 'Saving...' : 'Save'}</button>
          </div>
        </div>
        {#if selectedFile && view === 'preview'}
          <SkillFilePreview
            path={selectedPath}
            content={editorContent}
            paths={files.map((file) => file.path)}
            onopen={(path) => selectFile(files.find((file) => file.path === path))}
          />
        {:else if selectedFile}
          <textarea bind:value={editorContent} spellcheck="false" class="min-h-0 flex-1 resize-none bg-white p-4 font-mono text-xs leading-5 text-gray-900 outline-none dark:bg-dark-surface dark:text-dark-text"></textarea>
        {:else}
          <div class="flex flex-1 items-center justify-center text-sm text-gray-400">Select a file</div>
        {/if}
      </section>
    </div>
  </div>
</div>
