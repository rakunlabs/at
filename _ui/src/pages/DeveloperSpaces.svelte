<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import {
    ChevronDown, CircleAlert, Download, FilePlus, FolderGit2, FolderPlus, GitBranch, LoaderCircle, MessageSquare,
    PanelBottom, PanelLeft, PanelRight, Plus, Power, RefreshCw, Search, Settings, Trash2, Upload, X, FileText, GitCompare,
  } from 'lucide-svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { getInfo } from '@/lib/api/gateway';
  import {
    cloneDeveloperProject, createDeveloperFile, createDeveloperSession, deleteDeveloperFile, deleteDeveloperSession,
    fetchDeveloperFileBlob, getDeveloperGitDiff, getDeveloperGitStatus, getDeveloperSpace, listDeveloperFiles, listDeveloperSessions,
    readDeveloperFile, renameDeveloperFile, resetDeveloperSpace, searchDeveloperFiles, startDeveloperSpace, stopDeveloperSpace,
    updateDeveloperSpace, uploadDeveloperFile, writeDeveloperFile, DEFAULT_DEVELOPER_IMAGE,
    type DeveloperFileEntry, type DeveloperSearchMatch, type DeveloperSession, type DeveloperSpace,
  } from '@/lib/api/developer-spaces';
  import FileTree from '@/lib/components/developer/FileTree.svelte';
  import CodeEditor from '@/lib/components/developer/CodeEditor.svelte';
  import SessionChat from '@/lib/components/developer/SessionChat.svelte';
  import GitPanel from '@/lib/components/developer/GitPanel.svelte';
  import TerminalPanel from '@/lib/components/developer/TerminalPanel.svelte';
  import Markdown from '@/lib/components/Markdown.svelte';
  import {
    baseName, isImagePath, isMarkdownFile, isWithin, joinPath, parentPath, renamedPath, validEntryName,
    STATUS_LABELS, type DeveloperTerminalTab,
  } from '@/lib/helper/developer-space';

  storeNavbar.title = 'Developer Space';

  // ─── Tabs in the main area ───
  type ChatTab = { kind: 'chat'; key: string; sessionId: string };
  type FileTab = { kind: 'file'; key: string; path: string; content: string; saved: string; version: string; loading: boolean; binary: boolean; tooLarge: boolean; preview: boolean; imageURL: string; error: string; stale: boolean };
  type DiffTab = { kind: 'diff'; key: string; project: string; file: string; staged: boolean; untracked: boolean; head: boolean; diff: string; loading: boolean };
  type Tab = ChatTab | FileTab | DiffTab;

  const LAYOUT_KEY = 'at.developer-space.layout';

  let space = $state<DeveloperSpace | null>(null);
  let loadError = $state('');
  let sessions = $state<DeveloperSession[]>([]);
  let project = $state('');
  let projects = $state<DeveloperFileEntry[]>([]);
  let listings = $state<Record<string, DeveloperFileEntry[] | undefined>>({});
  let expanded = $state<Record<string, boolean>>({});
  let folderLoading = $state<Record<string, boolean>>({});
  let tabs = $state<Tab[]>([]);
  let activeKey = $state('');
  let modelGroups = $state<Array<{ label: string; models: string[] }>>([]);
  let defaultModel = $state('');
  let gitChanged = $state<Set<string>>(new Set());
  let gitRevision = $state(0);
  let sidebarView = $state<'files' | 'search'>('files');
  let rightView = $state<'git' | 'none'>('git');
  let showLeft = $state(true);
  let showTerminal = $state(true);
  let leftWidth = $state(260);
  let rightWidth = $state(300);
  let terminalHeight = $state(240);
  let terminalMinimized = $state(false);
  let terminals = $state<DeveloperTerminalTab[]>([]);
  let activeTerminal = $state('');
  let projectMenu = $state(false);
  let newProjectMode = $state<'' | 'folder' | 'clone'>('');
  let newProjectName = $state('');
  let cloneRemote = $state('');
  let cloneBranch = $state('');
  let creatingProject = $state(false);
  let menu = $state<{ x: number; y: number; entry: DeveloperFileEntry | null; folder: string } | null>(null);
  let searchQuery = $state('');
  let searchResults = $state<DeveloperSearchMatch[]>([]);
  let searching = $state(false);
  let searchTruncated = $state(false);
  let starting = $state(false);
  let settingsOpen = $state(false);
  let settingsSaving = $state(false);
  let settingsForm = $state({ image: '', cpu: '', memory: '', diskGiB: '' });
  let uploadInput: HTMLInputElement;
  let uploadFolder = '';

  const activeTab = $derived(tabs.find(t => t.key === activeKey));
  const dirty = $derived(new Set(tabs.filter((t): t is FileTab => t.kind === 'file' && t.content !== t.saved).map(t => t.path)));
  const projectSessions = $derived(sessions.filter(s => s.project_path === project || (project === '' && !s.project_path)));
  const otherSessions = $derived(sessions.filter(s => !projectSessions.includes(s)));

  // ─── Load ───

  onMount(() => {
    try {
      const saved = JSON.parse(localStorage.getItem(LAYOUT_KEY) || '{}');
      if (typeof saved.leftWidth === 'number') leftWidth = saved.leftWidth;
      if (typeof saved.rightWidth === 'number') rightWidth = saved.rightWidth;
      if (typeof saved.terminalHeight === 'number') terminalHeight = saved.terminalHeight;
      if (typeof saved.showTerminal === 'boolean') showTerminal = saved.showTerminal;
      if (typeof saved.terminalMinimized === 'boolean') terminalMinimized = saved.terminalMinimized;
      if (typeof saved.showLeft === 'boolean') showLeft = saved.showLeft;
      if (saved.rightView === 'git' || saved.rightView === 'none') rightView = saved.rightView;
      if (typeof saved.project === 'string') project = saved.project;
    } catch { /* defaults */ }
    if (window.matchMedia('(max-width: 767px)').matches) { showLeft = false; rightView = 'none'; showTerminal = false; }
    void boot();
    const beforeUnload = (event: BeforeUnloadEvent) => { if (dirty.size) event.preventDefault(); };
    window.addEventListener('beforeunload', beforeUnload);
    return () => window.removeEventListener('beforeunload', beforeUnload);
  });

  $effect(() => {
    const layout = { leftWidth, rightWidth, terminalHeight, terminalMinimized, showTerminal, showLeft, rightView, project };
    localStorage.setItem(LAYOUT_KEY, JSON.stringify(layout));
  });

  async function boot() {
    loadError = '';
    try {
      space = await getDeveloperSpace();
    } catch (e: any) {
      loadError = e?.response?.data?.message || 'Could not open your developer space';
      return;
    }
    void loadModels();
    await Promise.all([loadSessions(), start()]);
  }

  async function start() {
    starting = true;
    try {
      space = await startDeveloperSpace();
      await loadRoot();
      if (project && !projects.some(p => p.path === project)) project = '';
      if (project) await openProject(project, false);
      if (!terminals.length) newTerminal();
    } catch (e: any) {
      space = space ? { ...space, status: 'error', error: e?.response?.data?.message || 'The space could not start' } : space;
    } finally {
      starting = false;
    }
  }

  async function stop() {
    if (dirty.size && !confirm('You have unsaved files. Stop the space anyway? Unsaved edits stay in the editor.')) return;
    try {
      space = await stopDeveloperSpace();
      terminals = [];
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not stop the space', 'alert');
    }
  }

  function openSettings() {
    if (!space) return;
    settingsForm = {
      image: space.image || '',
      cpu: space.cpu_limit || '',
      memory: space.memory_limit || '',
      diskGiB: space.disk_limit_bytes ? String(+(space.disk_limit_bytes / 2 ** 30).toFixed(2)) : '',
    };
    settingsOpen = true;
  }

  async function saveSettings() {
    if (!space) return;
    const disk = settingsForm.diskGiB.trim();
    const diskBytes = disk ? Math.round(Number(disk) * 2 ** 30) : 0;
    if (disk && (!Number.isFinite(diskBytes) || diskBytes <= 0)) {
      addToast('Disk limit must be a positive number of GiB', 'alert');
      return;
    }
    const running = space.status === 'ready';
    if (running && dirty.size && !confirm('Applying these settings restarts the container. You have unsaved files; continue? Unsaved edits stay in the editor.')) return;
    settingsSaving = true;
    try {
      // PUT replaces the record, so the stored profiles are sent back unchanged.
      space = await updateDeveloperSpace({
        image: settingsForm.image.trim(), cpu_limit: settingsForm.cpu.trim(), memory_limit: settingsForm.memory.trim(),
        disk_limit_bytes: diskBytes, config: space.config,
      });
      settingsOpen = false;
      if (running) {
        addToast('Settings saved. Restarting the container; packages installed in the old one are not carried over.', 'info');
        terminals = [];
        await start();
      } else {
        addToast('Settings saved. They apply the next time the space starts.', 'info');
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not save the settings', 'alert');
    } finally {
      settingsSaving = false;
    }
  }

  async function reset() {
    const answer = prompt('This permanently deletes every file, project and session in your developer space. Type "delete" to confirm.');
    if (answer !== 'delete') return;
    try {
      const result = await resetDeveloperSpace();
      if (result.cleanup_warning) addToast(result.cleanup_warning, 'alert');
      tabs = []; activeKey = ''; terminals = []; listings = {}; expanded = {}; project = ''; sessions = [];
      await boot();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not reset the space', 'alert');
    }
  }

  async function loadModels() {
    try {
      const info = await getInfo();
      const groups: Array<{ label: string; models: string[] }> = [];
      for (const p of info.providers ?? []) {
        const reference = p.reference || p.key;
        const models = (p.models?.length ? p.models : p.default_model ? [p.default_model] : []).map(m => `${reference}/${m}`);
        if (models.length) groups.push({ label: p.key, models });
      }
      modelGroups = groups.sort((a, b) => a.label.localeCompare(b.label));
      defaultModel = localStorage.getItem('at.developer-space.model') || modelGroups[0]?.models[0] || '';
    } catch { modelGroups = []; }
  }

  async function loadSessions() {
    try { sessions = await listDeveloperSessions(); }
    catch (e: any) { addToast(e?.response?.data?.message || 'Could not load sessions', 'alert'); }
  }

  // ─── Files ───

  async function loadFolder(folder: string) {
    folderLoading = { ...folderLoading, [folder]: true };
    try {
      const result = await listDeveloperFiles(folder);
      listings = { ...listings, [folder]: result.entries };
      if (folder === '') projects = result.entries.filter(e => e.type === 'dir');
    } catch (e: any) {
      if (e?.response?.status === 404) {
        const { [folder]: _, ...rest } = listings; listings = rest;
        expanded = { ...expanded, [folder]: false };
      } else addToast(e?.response?.data?.message || `Could not list ${folder || 'the space'}`, 'alert');
    } finally {
      const { [folder]: _, ...rest } = folderLoading; folderLoading = rest;
    }
  }

  const loadRoot = () => loadFolder('');

  async function refreshTree() {
    const open = ['', ...Object.keys(expanded).filter(k => expanded[k])];
    await Promise.all(open.map(loadFolder));
    await refreshGitMarks();
  }

  async function toggleFolder(path: string) {
    const next = !expanded[path];
    expanded = { ...expanded, [path]: next };
    if (next && !listings[path]) await loadFolder(path);
  }

  async function openProject(path: string, focusChat = true) {
    project = path;
    projectMenu = false;
    expanded = { ...expanded, [path]: true };
    if (!listings[path]) await loadFolder(path);
    await refreshGitMarks();
    if (focusChat) {
      const latest = sessions.find(s => s.project_path === path);
      if (latest) openSession(latest.id);
    }
  }

  async function refreshGitMarks() {
    if (!project) { gitChanged = new Set(); return; }
    try {
      const status = await getDeveloperGitStatus(project);
      const marks = new Set<string>();
      for (const file of [...status.staged.map(f => f.path), ...status.unstaged.map(f => f.path), ...status.untracked, ...status.conflicted]) {
        const full = joinPath(project, file);
        marks.add(full);
        let folder = parentPath(full);
        while (folder && folder !== project) { marks.add(folder); folder = parentPath(folder); }
      }
      gitChanged = marks;
    } catch { gitChanged = new Set(); }
  }

  async function openFile(path: string) {
    const key = `file:${path}`;
    const existing = tabs.find(t => t.key === key);
    activeKey = key;
    if (existing) return;
    const tab: FileTab = { kind: 'file', key, path, content: '', saved: '', version: '', loading: true, binary: false, tooLarge: false, preview: isMarkdownFile(path), imageURL: '', error: '', stale: false };
    tabs = [...tabs, tab];
    await loadFileTab(key);
  }

  function fileTab(key: string): FileTab | undefined {
    const tab = tabs.find(t => t.key === key);
    return tab?.kind === 'file' ? tab : undefined;
  }

  function patchTab(key: string, patch: Partial<Tab>) {
    tabs = tabs.map(t => (t.key === key ? ({ ...t, ...patch } as Tab) : t));
  }

  async function loadFileTab(key: string) {
    const tab = fileTab(key);
    if (!tab) return;
    patchTab(key, { loading: true, error: '' });
    try {
      if (isImagePath(tab.path)) {
        const blob = await fetchDeveloperFileBlob(tab.path);
        if (tab.imageURL) URL.revokeObjectURL(tab.imageURL);
        patchTab(key, { imageURL: URL.createObjectURL(blob), loading: false, binary: true, stale: false });
        return;
      }
      const file = await readDeveloperFile(tab.path);
      patchTab(key, {
        content: file.content ?? '', saved: file.content ?? '', version: file.version ?? '',
        binary: !!file.binary, tooLarge: !!file.too_large, loading: false, stale: false,
      });
    } catch (e: any) {
      patchTab(key, { loading: false, error: e?.response?.data?.message || e?.message || 'Could not open the file' });
    }
  }

  async function saveTab(key: string) {
    const tab = fileTab(key);
    if (!tab || tab.binary || tab.tooLarge || tab.content === tab.saved) return;
    const content = tab.content;
    try {
      const result = await writeDeveloperFile(tab.path, content, tab.version);
      patchTab(key, { saved: content, version: result.version, stale: false });
      gitRevision++;
      void refreshGitMarks();
    } catch (e: any) {
      if (e?.response?.status === 409) {
        patchTab(key, { stale: true });
        addToast('This file changed on disk since you opened it. Reload it or overwrite from the banner.', 'alert');
      } else addToast(e?.response?.data?.message || 'Could not save the file', 'alert');
    }
  }

  async function overwrite(key: string) {
    const tab = fileTab(key);
    if (!tab) return;
    try {
      const result = await writeDeveloperFile(tab.path, tab.content, '');
      patchTab(key, { saved: tab.content, version: result.version, stale: false });
      gitRevision++;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not save the file', 'alert');
    }
  }

  // Files the agent wrote: clean tabs reload silently, edited ones get a banner.
  async function filesChanged(paths: string[], tree: boolean) {
    for (const path of paths) {
      const tab = fileTab(`file:${path}`);
      if (!tab) continue;
      if (tab.content === tab.saved) await loadFileTab(tab.key);
      else patchTab(tab.key, { stale: true });
    }
    const folders = new Set(paths.map(parentPath));
    if (tree) await refreshTree();
    else await Promise.all([...folders].filter(f => f === '' || listings[f]).map(loadFolder));
    gitRevision++;
    void refreshGitMarks();
  }

  function closeTab(key: string) {
    const tab = tabs.find(t => t.key === key);
    if (tab?.kind === 'file' && tab.content !== tab.saved && !confirm(`Discard unsaved changes to ${baseName(tab.path)}?`)) return;
    if (tab?.kind === 'file' && tab.imageURL) URL.revokeObjectURL(tab.imageURL);
    const index = tabs.findIndex(t => t.key === key);
    tabs = tabs.filter(t => t.key !== key);
    if (activeKey === key) activeKey = tabs[Math.min(index, tabs.length - 1)]?.key ?? '';
  }

  async function download(path: string) {
    try {
      const blob = await fetchDeveloperFileBlob(path);
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url; a.download = baseName(path); a.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (e: any) {
      addToast(e?.message || 'Download failed', 'alert');
    }
  }

  // ─── Tree actions ───

  function showMenu(event: MouseEvent, entry: DeveloperFileEntry | null, folder = '') {
    menu = { x: Math.min(event.clientX, window.innerWidth - 200), y: Math.min(event.clientY, window.innerHeight - 260), entry, folder: entry ? (entry.type === 'dir' ? entry.path : parentPath(entry.path)) : folder };
  }

  async function createEntry(folder: string, type: 'file' | 'dir') {
    menu = null;
    const name = prompt(type === 'dir' ? 'New folder name' : 'New file name');
    if (name === null) return;
    const problem = validEntryName(name);
    if (problem) { addToast(problem, 'alert'); return; }
    const path = joinPath(folder, name.trim());
    try {
      await createDeveloperFile(path, type);
      if (folder) expanded = { ...expanded, [folder]: true };
      await loadFolder(folder);
      if (type === 'file') await openFile(path);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not create it', 'alert');
    }
  }

  async function renameEntry(entry: DeveloperFileEntry) {
    menu = null;
    const name = prompt('New name', entry.name);
    if (name === null || name.trim() === entry.name) return;
    const problem = validEntryName(name);
    if (problem) { addToast(problem, 'alert'); return; }
    const to = joinPath(parentPath(entry.path), name.trim());
    try {
      await renameDeveloperFile(entry.path, to);
      tabs = tabs.map(t => (t.kind === 'file' && isWithin(t.path, entry.path) ? { ...t, path: renamedPath(t.path, entry.path, to), key: `file:${renamedPath(t.path, entry.path, to)}` } : t));
      if (activeKey.startsWith('file:') && isWithin(activeKey.slice(5), entry.path)) activeKey = `file:${renamedPath(activeKey.slice(5), entry.path, to)}`;
      if (project === entry.path) project = to;
      await loadFolder(parentPath(entry.path));
      void refreshGitMarks();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not rename it', 'alert');
    }
  }

  async function deleteEntry(entry: DeveloperFileEntry) {
    menu = null;
    const what = entry.type === 'dir' ? `the folder "${entry.path}" and everything in it` : `"${entry.path}"`;
    if (!confirm(`Delete ${what}? This cannot be undone.`)) return;
    try {
      await deleteDeveloperFile(entry.path);
      tabs = tabs.filter(t => !(t.kind === 'file' && isWithin(t.path, entry.path)));
      if (activeKey && !tabs.some(t => t.key === activeKey)) activeKey = tabs[0]?.key ?? '';
      if (project === entry.path) project = '';
      await loadFolder(parentPath(entry.path));
      void refreshGitMarks();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not delete it', 'alert');
    }
  }

  function pickUpload(folder: string) {
    menu = null;
    uploadFolder = folder;
    uploadInput.click();
  }

  async function uploadFiles(folder: string, files: FileList | File[]) {
    const list = [...files];
    if (!list.length) return;
    let failed = 0;
    for (const file of list) {
      try { await uploadDeveloperFile(folder, file); }
      catch (e: any) { failed++; addToast(`${file.name}: ${e?.response?.data?.message || 'upload failed'}`, 'alert'); }
    }
    if (folder) expanded = { ...expanded, [folder]: true };
    await loadFolder(folder);
    if (list.length - failed > 0) addToast(`Uploaded ${list.length - failed} file${list.length - failed === 1 ? '' : 's'}`);
    void refreshGitMarks();
  }

  // ─── Projects ───

  async function createProject() {
    creatingProject = true;
    try {
      if (newProjectMode === 'folder') {
        const problem = validEntryName(newProjectName);
        if (problem) { addToast(problem, 'alert'); return; }
        await createDeveloperFile(newProjectName.trim(), 'dir');
        await loadRoot();
        await openProject(newProjectName.trim());
      } else {
        const result = await cloneDeveloperProject(cloneRemote.trim(), newProjectName.trim(), cloneBranch.trim());
        await loadRoot();
        await openProject(result.path);
        addToast(`Cloned into ${result.path}`);
      }
      newProjectMode = ''; newProjectName = ''; cloneRemote = ''; cloneBranch = '';
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not create the project', 'alert');
    } finally {
      creatingProject = false;
    }
  }

  // ─── Sessions ───

  function openSession(id: string) {
    const key = `chat:${id}`;
    if (!tabs.some(t => t.key === key)) tabs = [{ kind: 'chat', key, sessionId: id }, ...tabs.filter(t => t.kind !== 'chat' || t.key !== key)];
    activeKey = key;
  }

  async function newSession() {
    const model = defaultModel;
    const slash = model.indexOf('/');
    try {
      const session = await createDeveloperSession({
        project_path: project, mode: 'build',
        provider: slash > 0 ? model.slice(0, slash) : '', model: slash > 0 ? model.slice(slash + 1) : '',
      });
      sessions = [session, ...sessions];
      openSession(session.id);
    } catch (e: any) {
      addToast(e?.response?.data?.message || (modelGroups.length ? 'Could not start a session' : 'Add a model provider before starting a session'), 'alert');
    }
  }

  async function removeSession(session: DeveloperSession) {
    if (!confirm(`Delete the session "${session.title || 'Untitled'}" and its history?`)) return;
    try {
      await deleteDeveloperSession(session.id);
      sessions = sessions.filter(s => s.id !== session.id);
      closeTab(`chat:${session.id}`);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not delete the session', 'alert');
    }
  }

  function sessionUpdated(updated: DeveloperSession) {
    sessions = sessions.map(s => (s.id === updated.id ? updated : s)).sort((a, b) => b.updated_at.localeCompare(a.updated_at));
    if (updated.provider) localStorage.setItem('at.developer-space.model', `${updated.provider}/${updated.model ?? ''}`);
  }

  // ─── Diff tabs ───

  async function openDiff(file: string, opts: { staged?: boolean; untracked?: boolean; head?: boolean }) {
    const key = `diff:${project}:${opts.head ? 'head' : opts.staged ? 'staged' : 'work'}:${file}`;
    if (!tabs.some(t => t.key === key)) tabs = [...tabs, { kind: 'diff', key, project, file, staged: !!opts.staged, untracked: !!opts.untracked, head: !!opts.head, diff: '', loading: true }];
    activeKey = key;
    await loadDiff(key);
  }

  // Chat sessions are bound to their own project, which may differ from the
  // one selected in the explorer.
  async function openDiffIn(folder: string, file: string, opts: { untracked?: boolean; head?: boolean }) {
    if (project !== folder) await openProject(folder, false);
    await openDiff(file, opts);
  }

  async function loadDiff(key: string) {
    const tab = tabs.find(t => t.key === key);
    if (tab?.kind !== 'diff') return;
    patchTab(key, { loading: true });
    try {
      const result = await getDeveloperGitDiff(tab.project, tab.file, { staged: tab.staged, untracked: tab.untracked, head: tab.head });
      patchTab(key, { diff: result.diff, loading: false });
    } catch (e: any) {
      patchTab(key, { diff: e?.response?.data?.message || 'Could not load the diff', loading: false });
    }
  }

  function diffLineClass(line: string) {
    if (line.startsWith('+++') || line.startsWith('---')) return 'text-gray-500';
    if (line.startsWith('+')) return 'bg-green-50 text-green-800 dark:bg-green-950/40 dark:text-green-300';
    if (line.startsWith('-')) return 'bg-red-50 text-red-800 dark:bg-red-950/40 dark:text-red-300';
    if (line.startsWith('@@')) return 'text-blue-700 dark:text-blue-300';
    return 'text-gray-700 dark:text-dark-text-secondary';
  }

  // ─── Search ───

  async function runSearch() {
    if (!searchQuery.trim()) return;
    searching = true;
    try {
      const result = await searchDeveloperFiles(project, searchQuery);
      searchResults = result.matches;
      searchTruncated = result.truncated;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Search failed', 'alert');
    } finally {
      searching = false;
    }
  }

  // ─── Terminals ───

  let terminalSeq = 0;
  function newTerminal() {
    const id = `t${Date.now()}-${terminalSeq++}`;
    terminals = [...terminals, { id, cwd: project, generation: 0 }];
    activeTerminal = id;
    showTerminal = true;
    terminalMinimized = false;
  }

  function closeTerminal(id: string) {
    const index = terminals.findIndex(t => t.id === id);
    terminals = terminals.filter(t => t.id !== id);
    if (activeTerminal === id) activeTerminal = terminals[Math.min(index, terminals.length - 1)]?.id ?? '';
  }

  // ─── Resizing ───

  function drag(event: PointerEvent, apply: (dx: number, dy: number) => void) {
    event.preventDefault();
    const startX = event.clientX, startY = event.clientY;
    const target = event.currentTarget as HTMLElement;
    target.setPointerCapture(event.pointerId);
    const move = (e: PointerEvent) => apply(e.clientX - startX, e.clientY - startY);
    const up = () => { target.removeEventListener('pointermove', move); target.removeEventListener('pointerup', up); };
    target.addEventListener('pointermove', move);
    target.addEventListener('pointerup', up);
  }
  const clamp = (value: number, min: number, max: number) => Math.max(min, Math.min(max, value));

  function resizeLeft(event: PointerEvent) { const start = leftWidth; drag(event, dx => { leftWidth = clamp(start + dx, 180, 520); }); }
  function resizeRight(event: PointerEvent) { const start = rightWidth; drag(event, dx => { rightWidth = clamp(start - dx, 220, 560); }); }
  function resizeTerminal(event: PointerEvent) { const start = terminalHeight; drag(event, (_dx, dy) => { terminalHeight = clamp(start - dy, 100, Math.round(window.innerHeight * 0.75)); }); }

  function keydown(event: KeyboardEvent) {
    if ((event.ctrlKey || event.metaKey) && event.key === 's' && activeTab?.kind === 'file') {
      event.preventDefault();
      void saveTab(activeTab.key);
    }
    if ((event.ctrlKey || event.metaKey) && event.key === '`') {
      event.preventDefault();
      showTerminal = !showTerminal;
      if (showTerminal && !terminals.length) newTerminal();
    }
    if (event.key === 'Escape') { menu = null; projectMenu = false; }
  }

  function sessionTitle(s: DeveloperSession) { return s.title || 'New session'; }
  function tabLabel(tab: Tab) {
    if (tab.kind === 'chat') return sessionTitle(sessions.find(s => s.id === tab.sessionId) ?? ({ title: 'Session' } as DeveloperSession));
    if (tab.kind === 'file') return baseName(tab.path);
    return `${baseName(tab.file)} (${tab.staged ? 'staged' : 'changes'})`;
  }
  function statusDot(status: string) {
    if (status === 'running') return 'bg-blue-500 animate-pulse motion-reduce:animate-none';
    if (status.startsWith('waiting')) return 'bg-amber-500';
    if (status === 'failed') return 'bg-red-500';
    if (status === 'completed') return 'bg-green-500';
    return 'bg-gray-300 dark:bg-dark-border';
  }

  // Keep the default model for new sessions valid once the catalogue loads.
  $effect(() => {
    const groups = modelGroups;
    untrack(() => {
      if (defaultModel && !groups.some(g => g.models.includes(defaultModel))) defaultModel = groups[0]?.models[0] ?? '';
    });
  });
</script>

<svelte:head><title>AT | Developer Space</title></svelte:head>
<svelte:window onkeydown={keydown} onclick={() => { menu = null; }} />

<input bind:this={uploadInput} type="file" multiple class="hidden" onchange={event => { const files = event.currentTarget.files; if (files) void uploadFiles(uploadFolder, files); event.currentTarget.value = ''; }} />

{#if loadError}
  <div class="p-6"><p role="alert" class="settings-error">{loadError}</p><button class="settings-button mt-3" onclick={boot}>Try again</button></div>
{:else}
<div class="flex h-full min-h-0 flex-col bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text">
  <!-- Top bar -->
  <div class="flex h-10 shrink-0 items-center gap-2 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-surface px-2">
    <button type="button" onclick={() => (showLeft = !showLeft)} class={['p-1.5 hover:bg-gray-200 dark:hover:bg-dark-elevated', showLeft ? 'text-gray-900 dark:text-dark-text' : 'text-gray-400']} title="Toggle side bar" aria-label="Toggle side bar" aria-pressed={showLeft}><PanelLeft size={15} /></button>

    <div class="relative">
      <button type="button" onclick={event => { event.stopPropagation(); projectMenu = !projectMenu; }} class="inline-flex max-w-72 items-center gap-1.5 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-base px-2.5 py-1 text-xs hover:bg-gray-50 dark:hover:bg-dark-elevated" aria-haspopup="menu" aria-expanded={projectMenu}>
        <FolderGit2 size={13} class="shrink-0 text-gray-500" />
        <span class="truncate font-medium">{project || 'All files'}</span>
        <ChevronDown size={12} class="shrink-0 text-gray-400" />
      </button>
      {#if projectMenu}
        <div role="menu" tabindex="-1" class="absolute left-0 top-full z-30 mt-1 w-72 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface py-1 text-xs shadow-lg" onclick={event => event.stopPropagation()} onkeydown={() => {}}>
          <button type="button" role="menuitem" onclick={() => { project = ''; projectMenu = false; void refreshGitMarks(); }} class={['block w-full px-3 py-1.5 text-left hover:bg-gray-100 dark:hover:bg-dark-elevated', project === '' ? 'font-semibold' : '']}>All files <span class="text-gray-400">/workspace</span></button>
          {#each projects as p (p.path)}
            <button type="button" role="menuitem" onclick={() => openProject(p.path)} class={['flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-gray-100 dark:hover:bg-dark-elevated', project === p.path ? 'font-semibold' : '']}>
              <FolderGit2 size={13} class="text-gray-400" /> <span class="truncate">{p.name}</span>
            </button>
          {/each}
          <div class="my-1 border-t border-gray-200 dark:border-dark-border"></div>
          <button type="button" role="menuitem" onclick={() => { newProjectMode = 'folder'; projectMenu = false; }} class="flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-gray-100 dark:hover:bg-dark-elevated"><FolderPlus size={13} /> New empty project</button>
          <button type="button" role="menuitem" onclick={() => { newProjectMode = 'clone'; projectMenu = false; }} class="flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-gray-100 dark:hover:bg-dark-elevated"><GitBranch size={13} /> Clone a Git repository</button>
        </div>
      {/if}
    </div>

    <span class="flex-1"></span>

    {#if space}
      <span class="hidden items-center gap-1.5 text-xs text-gray-500 dark:text-dark-text-muted sm:inline-flex" title={space.error || ''}>
        <span class={['size-2 rounded-full', starting ? 'bg-blue-500 animate-pulse motion-reduce:animate-none' : space.status === 'ready' ? 'bg-green-500' : space.status === 'error' ? 'bg-red-500' : 'bg-gray-400']}></span>
        {starting ? 'Starting…' : space.status === 'ready' ? 'Running' : space.status === 'error' ? 'Error' : space.status === 'stopped' ? 'Stopped' : 'Not started'}
      </span>
      {#if space.status === 'ready' && !starting}
        <button type="button" onclick={stop} class="p-1.5 text-gray-500 hover:bg-gray-200 hover:text-gray-900 dark:hover:bg-dark-elevated" title="Stop the container (files are kept)" aria-label="Stop the space"><Power size={14} /></button>
      {:else if !starting}
        <button type="button" onclick={start} class="inline-flex items-center gap-1 bg-gray-900 px-2.5 py-1 text-xs text-white hover:bg-gray-800 dark:bg-accent" title="Start the container">Start</button>
      {/if}
      <button type="button" onclick={() => (settingsOpen ? (settingsOpen = false) : openSettings())} class={['p-1.5 hover:bg-gray-200 hover:text-gray-900 dark:hover:bg-dark-elevated', settingsOpen ? 'text-gray-900 dark:text-dark-text' : 'text-gray-500']} title="Space settings (image and resource limits)" aria-label="Space settings" aria-expanded={settingsOpen}><Settings size={14} /></button>
      <button type="button" onclick={reset} class="p-1.5 text-gray-400 hover:bg-gray-200 hover:text-red-700 dark:hover:bg-dark-elevated" title="Reset the space (deletes everything)" aria-label="Reset the space"><Trash2 size={14} /></button>
    {/if}
    <button type="button" onclick={() => { showTerminal = !showTerminal; if (showTerminal && !terminals.length) newTerminal(); }} class={['p-1.5 hover:bg-gray-200 dark:hover:bg-dark-elevated', showTerminal ? 'text-gray-900 dark:text-dark-text' : 'text-gray-400']} title="Toggle terminal (Ctrl+`)" aria-label="Toggle terminal" aria-pressed={showTerminal}><PanelBottom size={15} /></button>
    <button type="button" onclick={() => (rightView = rightView === 'git' ? 'none' : 'git')} class={['p-1.5 hover:bg-gray-200 dark:hover:bg-dark-elevated', rightView !== 'none' ? 'text-gray-900 dark:text-dark-text' : 'text-gray-400']} title="Toggle source control" aria-label="Toggle source control" aria-pressed={rightView !== 'none'}><PanelRight size={15} /></button>
  </div>

  {#if space?.status === 'error' && !starting}
    <p role="alert" class="flex items-start gap-2 border-b border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-3 py-2 text-xs text-red-800 dark:text-red-300"><CircleAlert size={14} class="mt-0.5 shrink-0" /> {space.error || 'The space could not start.'} Files in your space are safe; try Start again.</p>
  {/if}

  {#if settingsOpen && space}
    <form class="border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-surface px-3 py-2 text-xs" onsubmit={event => { event.preventDefault(); void saveSettings(); }}>
      <div class="flex flex-wrap items-end gap-2">
        <label class="min-w-64 flex-[2]">Base image
          <input bind:value={settingsForm.image} placeholder={DEFAULT_DEVELOPER_IMAGE} spellcheck="false" autocomplete="off" class="mt-1 block w-full border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-base px-2 py-1.5 font-mono text-sm dark:text-dark-text" />
        </label>
        <label class="w-24">CPU cores<input bind:value={settingsForm.cpu} placeholder="2" inputmode="decimal" class="mt-1 block w-full border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-base px-2 py-1.5 font-mono text-sm dark:text-dark-text" /></label>
        <label class="w-24">Memory<input bind:value={settingsForm.memory} placeholder="4g" class="mt-1 block w-full border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-base px-2 py-1.5 font-mono text-sm dark:text-dark-text" /></label>
        <label class="w-24">Disk (GiB)<input bind:value={settingsForm.diskGiB} placeholder="20" inputmode="decimal" class="mt-1 block w-full border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-base px-2 py-1.5 font-mono text-sm dark:text-dark-text" /></label>
        <button type="submit" disabled={settingsSaving || starting} class="inline-flex items-center gap-1.5 bg-gray-900 px-3 py-1.5 font-medium text-white disabled:opacity-50 dark:bg-accent">{#if settingsSaving}<LoaderCircle size={13} class="animate-spin motion-reduce:animate-none" />{/if}{space.status === 'ready' ? 'Save & restart' : 'Save'}</button>
        <button type="button" onclick={() => (settingsOpen = false)} class="px-2 py-1.5 text-gray-600 hover:bg-gray-200 dark:text-dark-text-secondary dark:hover:bg-dark-elevated">Cancel</button>
      </div>
      <p class="mt-1.5 text-gray-500 dark:text-dark-text-muted">
        Any Docker image works; leave empty for <code class="font-mono">{DEFAULT_DEVELOPER_IMAGE}</code>. AT installs nothing: add whatever you need from the terminal.
        Installed packages stay while the container is stopped and are lost when the image or limits change; files in <code class="font-mono">/workspace</code> are always kept.
      </p>
    </form>
  {/if}

  {#if newProjectMode}
    <form class="flex flex-wrap items-end gap-2 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-surface px-3 py-2 text-xs" onsubmit={event => { event.preventDefault(); void createProject(); }}>
      {#if newProjectMode === 'clone'}
        <label class="min-w-64 flex-[2]">Repository URL<input bind:value={cloneRemote} required placeholder="https://github.com/org/repo.git" class="mt-1 block w-full border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-base px-2 py-1.5 font-mono text-sm dark:text-dark-text" /></label>
        <label class="w-32">Branch<input bind:value={cloneBranch} placeholder="default" class="mt-1 block w-full border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-base px-2 py-1.5 font-mono text-sm dark:text-dark-text" /></label>
      {/if}
      <label class="min-w-40 flex-1">Folder name{newProjectMode === 'clone' ? ' (optional)' : ''}<input bind:value={newProjectName} required={newProjectMode === 'folder'} placeholder={newProjectMode === 'clone' ? 'from the URL' : 'my-project'} class="mt-1 block w-full border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-base px-2 py-1.5 text-sm dark:text-dark-text" /></label>
      <button type="submit" disabled={creatingProject} class="inline-flex items-center gap-1.5 bg-gray-900 px-3 py-1.5 font-medium text-white disabled:opacity-50 dark:bg-accent">{#if creatingProject}<LoaderCircle size={13} class="animate-spin motion-reduce:animate-none" />{/if}{newProjectMode === 'clone' ? 'Clone' : 'Create'}</button>
      <button type="button" onclick={() => (newProjectMode = '')} class="px-2 py-1.5 text-gray-600 hover:bg-gray-200 dark:text-dark-text-secondary dark:hover:bg-dark-elevated">Cancel</button>
    </form>
  {/if}

  <div class="flex min-h-0 flex-1">
    <!-- Left: sessions + explorer -->
    {#if showLeft}
      <aside class="flex min-h-0 shrink-0 flex-col border-r border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-surface max-md:absolute max-md:inset-y-10 max-md:left-0 max-md:z-20 max-md:w-[85vw] max-md:shadow-xl" style={`width:${leftWidth}px`}>
        <section class="flex max-h-[40%] min-h-0 flex-col border-b border-gray-200 dark:border-dark-border">
          <div class="flex items-center gap-1 px-2 py-1.5">
            <h3 class="flex-1 text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-dark-text-muted">Sessions</h3>
            <button type="button" onclick={newSession} class="p-1 text-gray-500 hover:bg-gray-200 hover:text-gray-900 dark:hover:bg-dark-elevated dark:hover:text-dark-text" title={`New session in ${project || 'the space'}`} aria-label="New session"><Plus size={14} /></button>
          </div>
          <ul class="min-h-0 overflow-y-auto pb-1">
            {#each projectSessions as s (s.id)}
              <li class="group flex items-center">
                <button type="button" onclick={() => openSession(s.id)} class={['flex min-w-0 flex-1 items-center gap-2 px-3 py-1.5 text-left text-[13px] hover:bg-gray-100 dark:hover:bg-dark-elevated', activeKey === `chat:${s.id}` ? 'bg-gray-200 dark:bg-dark-elevated' : '']} title={STATUS_LABELS[s.status]}>
                  <span class={['size-1.5 shrink-0 rounded-full', statusDot(s.status)]}></span>
                  <span class="min-w-0 flex-1 truncate">{sessionTitle(s)}</span>
                </button>
                <button type="button" onclick={() => removeSession(s)} class="mr-1 p-1 text-gray-400 opacity-0 hover:text-red-700 group-hover:opacity-100 focus:opacity-100 [@media(pointer:coarse)]:opacity-100" aria-label={`Delete ${sessionTitle(s)}`}><Trash2 size={12} /></button>
              </li>
            {:else}
              <li class="px-3 py-1.5 text-xs text-gray-500 dark:text-dark-text-muted">
                <button type="button" onclick={newSession} class="inline-flex items-center gap-1.5 hover:text-gray-900 dark:hover:text-dark-text"><MessageSquare size={12} /> Start a session in {project || 'the space'}</button>
              </li>
            {/each}
            {#if otherSessions.length}
              <li class="px-3 pt-2 pb-0.5 text-[10px] uppercase tracking-wide text-gray-400">Other projects</li>
              {#each otherSessions as s (s.id)}
                <li>
                  <button type="button" onclick={() => { if (s.project_path !== project) void openProject(s.project_path, false); openSession(s.id); }} class={['flex w-full min-w-0 items-center gap-2 px-3 py-1 text-left text-xs text-gray-600 hover:bg-gray-100 dark:text-dark-text-secondary dark:hover:bg-dark-elevated', activeKey === `chat:${s.id}` ? 'bg-gray-200 dark:bg-dark-elevated' : '']}>
                    <span class={['size-1.5 shrink-0 rounded-full', statusDot(s.status)]}></span>
                    <span class="min-w-0 flex-1 truncate">{sessionTitle(s)}</span>
                    <span class="shrink-0 truncate text-[10px] text-gray-400">{s.project_path || '/'}</span>
                  </button>
                </li>
              {/each}
            {/if}
          </ul>
        </section>

        <div class="flex items-center gap-0.5 border-b border-gray-200 dark:border-dark-border px-1 py-1">
          <button type="button" onclick={() => (sidebarView = 'files')} class={['px-2 py-0.5 text-[11px] font-semibold uppercase tracking-wide', sidebarView === 'files' ? 'text-gray-900 dark:text-dark-text' : 'text-gray-400 hover:text-gray-700']}>Files</button>
          <button type="button" onclick={() => (sidebarView = 'search')} class={['px-2 py-0.5 text-[11px] font-semibold uppercase tracking-wide', sidebarView === 'search' ? 'text-gray-900 dark:text-dark-text' : 'text-gray-400 hover:text-gray-700']}>Search</button>
          <span class="flex-1"></span>
          {#if sidebarView === 'files'}
            <button type="button" onclick={() => createEntry(project, 'file')} class="p-1 text-gray-500 hover:bg-gray-200 hover:text-gray-900 dark:hover:bg-dark-elevated" title="New file" aria-label="New file"><FilePlus size={13} /></button>
            <button type="button" onclick={() => createEntry(project, 'dir')} class="p-1 text-gray-500 hover:bg-gray-200 hover:text-gray-900 dark:hover:bg-dark-elevated" title="New folder" aria-label="New folder"><FolderPlus size={13} /></button>
            <button type="button" onclick={() => pickUpload(project)} class="p-1 text-gray-500 hover:bg-gray-200 hover:text-gray-900 dark:hover:bg-dark-elevated" title="Upload files" aria-label="Upload files"><Upload size={13} /></button>
            <button type="button" onclick={refreshTree} class="p-1 text-gray-500 hover:bg-gray-200 hover:text-gray-900 dark:hover:bg-dark-elevated" title="Refresh" aria-label="Refresh files"><RefreshCw size={13} /></button>
          {/if}
        </div>

        {#if sidebarView === 'files'}
          <div
            role="region"
            aria-label="Files"
            class="min-h-0 flex-1 overflow-auto py-1"
            oncontextmenu={event => { if (event.target === event.currentTarget) { event.preventDefault(); showMenu(event, null, project); } }}
            ondragover={event => event.preventDefault()}
            ondrop={event => { event.preventDefault(); if (event.dataTransfer?.files.length) void uploadFiles(project, event.dataTransfer.files); }}
          >
            {#if space?.status !== 'ready' && !Object.keys(listings).length}
              <p class="px-3 py-2 text-xs text-gray-500 dark:text-dark-text-muted">{starting ? 'Starting your space…' : 'Start the space to browse files.'}</p>
            {:else if project}
              <FileTree {listings} {expanded} loading={folderLoading} folder={project} selected={activeTab?.kind === 'file' ? activeTab.path : ''} {dirty} changed={gitChanged} ontoggle={toggleFolder} onopen={entry => openFile(entry.path)} onmenu={(event, entry) => showMenu(event, entry)} ondropfiles={(folder, files) => uploadFiles(folder, files)} />
            {:else}
              <FileTree {listings} {expanded} loading={folderLoading} folder="" selected={activeTab?.kind === 'file' ? activeTab.path : ''} {dirty} changed={gitChanged} ontoggle={toggleFolder} onopen={entry => openFile(entry.path)} onmenu={(event, entry) => showMenu(event, entry)} ondropfiles={(folder, files) => uploadFiles(folder, files)} />
              {#if listings['']?.length === 0}
                <div class="px-3 py-3 text-xs text-gray-500 dark:text-dark-text-muted">
                  <p>Your space is empty.</p>
                  <button type="button" onclick={() => (newProjectMode = 'clone')} class="mt-2 inline-flex items-center gap-1.5 text-gray-800 underline-offset-2 hover:underline dark:text-dark-text"><GitBranch size={12} /> Clone a repository</button><br />
                  <button type="button" onclick={() => (newProjectMode = 'folder')} class="mt-1 inline-flex items-center gap-1.5 text-gray-800 underline-offset-2 hover:underline dark:text-dark-text"><FolderPlus size={12} /> Create an empty project</button>
                </div>
              {/if}
            {/if}
          </div>
        {:else}
          <div class="flex min-h-0 flex-1 flex-col">
            <form class="p-2" onsubmit={event => { event.preventDefault(); void runSearch(); }}>
              <div class="flex border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-base">
                <input bind:value={searchQuery} placeholder={`Search ${project || 'all files'} (regex)`} aria-label="Search text" class="min-w-0 flex-1 bg-transparent px-2 py-1 text-xs dark:text-dark-text focus:outline-none" />
                <button type="submit" class="px-2 text-gray-500" aria-label="Search">{#if searching}<LoaderCircle size={13} class="animate-spin motion-reduce:animate-none" />{:else}<Search size={13} />{/if}</button>
              </div>
            </form>
            <ul class="min-h-0 flex-1 overflow-y-auto text-xs">
              {#each searchResults as match, i (i)}
                <li><button type="button" onclick={() => openFile(joinPath(project, match.path))} class="block w-full px-3 py-1 text-left hover:bg-gray-100 dark:hover:bg-dark-elevated">
                  <span class="block truncate font-mono text-gray-800 dark:text-dark-text">{match.path}<span class="text-gray-400">:{match.line}</span></span>
                  <span class="block truncate font-mono text-gray-500 dark:text-dark-text-muted">{match.text.trim()}</span>
                </button></li>
              {/each}
              {#if searchTruncated}<li class="px-3 py-1 text-gray-500">Showing the first 2000 matches.</li>{/if}
            </ul>
          </div>
        {/if}
      </aside>
      <div role="separator" aria-orientation="vertical" aria-label="Resize side bar" class="w-1 shrink-0 cursor-col-resize bg-transparent hover:bg-accent/40 max-md:hidden" onpointerdown={resizeLeft}></div>
    {/if}

    <!-- Center: tabs + terminal -->
    <div class="flex min-h-0 min-w-0 flex-1 flex-col">
      <div class="flex h-9 shrink-0 items-end overflow-x-auto border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-surface">
        {#each tabs as tab (tab.key)}
          <div class={['group flex h-9 shrink-0 items-center gap-1.5 border-r border-gray-200 dark:border-dark-border pl-3 pr-1.5 text-xs', tab.key === activeKey ? 'bg-white text-gray-900 dark:bg-dark-base dark:text-dark-text' : 'text-gray-500 hover:bg-gray-100 dark:text-dark-text-muted dark:hover:bg-dark-elevated']}>
            <button type="button" onclick={() => (activeKey = tab.key)} class="inline-flex max-w-52 items-center gap-1.5" title={tab.kind === 'file' ? tab.path : tab.kind === 'diff' ? tab.file : ''}>
              {#if tab.kind === 'chat'}
                {@const s = sessions.find(x => x.id === tab.sessionId)}
                <MessageSquare size={13} class="shrink-0" />
                {#if s}<span class={['size-1.5 shrink-0 rounded-full', statusDot(s.status)]}></span>{/if}
              {:else if tab.kind === 'diff'}
                <GitCompare size={13} class="shrink-0" />
              {:else}
                <FileText size={13} class="shrink-0" />
              {/if}
              <span class="truncate">{tabLabel(tab)}</span>
            </button>
            <button type="button" onclick={() => closeTab(tab.key)} class="flex size-5 items-center justify-center hover:bg-gray-200 dark:hover:bg-dark-elevated" aria-label={`Close ${tabLabel(tab)}`}>
              {#if tab.kind === 'file' && tab.content !== tab.saved}
                <span class="size-2 rounded-full bg-gray-600 group-hover:hidden dark:bg-dark-text-secondary"></span>
                <X size={12} class="hidden group-hover:block" />
              {:else}
                <X size={12} class="opacity-50 group-hover:opacity-100" />
              {/if}
            </button>
          </div>
        {/each}
        <button type="button" onclick={newSession} class="flex h-9 shrink-0 items-center gap-1 px-3 text-xs text-gray-500 hover:bg-gray-100 hover:text-gray-900 dark:hover:bg-dark-elevated dark:hover:text-dark-text" title="New chat session"><Plus size={13} /> Chat</button>
      </div>

      <div class="relative min-h-0 flex-1">
        {#each tabs as tab (tab.key)}
          <div class={['absolute inset-0 flex flex-col', tab.key === activeKey ? '' : 'invisible']}>
            {#if tab.kind === 'chat'}
              {@const s = sessions.find(x => x.id === tab.sessionId)}
              {#if s}
                <SessionChat session={s} {modelGroups} revision={gitRevision} onsession={sessionUpdated} onfileschanged={filesChanged} onopenfile={openFile} ondiff={(file, opts) => openDiffIn(s.project_path, file, opts)} />
              {:else}
                <p class="p-4 text-sm text-gray-500">This session no longer exists.</p>
              {/if}
            {:else if tab.kind === 'file'}
              <div class="flex h-8 shrink-0 items-center gap-2 border-b border-gray-200 dark:border-dark-border px-3 text-xs text-gray-500 dark:text-dark-text-muted">
                <span class="min-w-0 flex-1 truncate font-mono">{tab.path}</span>
                {#if isMarkdownFile(tab.path) && !tab.binary}
                  <button type="button" onclick={() => patchTab(tab.key, { preview: !tab.preview })} class="px-1.5 py-0.5 hover:bg-gray-100 dark:hover:bg-dark-elevated">{tab.preview ? 'Edit' : 'Preview'}</button>
                {/if}
                {#if tab.content !== tab.saved && !tab.binary}
                  <button type="button" onclick={() => saveTab(tab.key)} class="bg-gray-900 px-2 py-0.5 text-white dark:bg-accent" title="Save (Ctrl+S)">Save</button>
                {/if}
                <button type="button" onclick={() => download(tab.path)} class="p-1 hover:bg-gray-100 dark:hover:bg-dark-elevated" title="Download" aria-label="Download file"><Download size={13} /></button>
                <button type="button" onclick={() => loadFileTab(tab.key)} class="p-1 hover:bg-gray-100 dark:hover:bg-dark-elevated" title="Reload from disk" aria-label="Reload file"><RefreshCw size={13} /></button>
              </div>
              {#if tab.stale}
                <div class="flex flex-wrap items-center gap-2 border-b border-amber-200 dark:border-amber-800 bg-amber-50 dark:bg-amber-950/40 px-3 py-1.5 text-xs text-amber-900 dark:text-amber-200">
                  <span class="flex-1">This file changed on disk{tab.content !== tab.saved ? ' while you were editing it' : ''}.</span>
                  <button type="button" onclick={() => loadFileTab(tab.key)} class="border border-amber-400 px-2 py-0.5 hover:bg-amber-100 dark:hover:bg-amber-900">Load disk version</button>
                  {#if tab.content !== tab.saved}<button type="button" onclick={() => overwrite(tab.key)} class="border border-amber-400 px-2 py-0.5 hover:bg-amber-100 dark:hover:bg-amber-900">Keep mine and overwrite</button>{/if}
                </div>
              {/if}
              <div class="min-h-0 flex-1">
                {#if tab.loading}
                  <p class="p-4 text-sm text-gray-500">Loading…</p>
                {:else if tab.error}
                  <p role="alert" class="p-4 text-sm text-red-700 dark:text-red-400">{tab.error}</p>
                {:else if tab.imageURL}
                  <div class="flex h-full items-center justify-center overflow-auto bg-[conic-gradient(#eee_25%,#fff_0_50%,#eee_0_75%,#fff_0)] bg-[length:16px_16px] p-4 dark:bg-none"><img src={tab.imageURL} alt={tab.path} class="max-h-full max-w-full" /></div>
                {:else if tab.binary || tab.tooLarge}
                  <div class="p-4 text-sm text-gray-600 dark:text-dark-text-secondary">
                    <p>{tab.tooLarge ? 'This file is larger than the 2 MiB editor limit.' : 'This is a binary file and cannot be edited here.'}</p>
                    <button type="button" onclick={() => download(tab.path)} class="mt-2 inline-flex items-center gap-1.5 border border-gray-300 px-3 py-1.5 text-xs hover:bg-gray-50 dark:border-dark-border dark:hover:bg-dark-elevated"><Download size={13} /> Download</button>
                  </div>
                {:else if tab.preview}
                  <div class="h-full overflow-y-auto p-6"><div class="mx-auto max-w-3xl"><Markdown source={tab.content} enhance /></div></div>
                {:else}
                  <CodeEditor path={tab.path} value={tab.saved === tab.content ? tab.saved : tab.content} onchange={value => patchTab(tab.key, { content: value })} onsave={() => saveTab(tab.key)} />
                {/if}
              </div>
            {:else}
              <div class="flex h-8 shrink-0 items-center gap-2 border-b border-gray-200 dark:border-dark-border px-3 text-xs text-gray-500 dark:text-dark-text-muted">
                <span class="min-w-0 flex-1 truncate font-mono">{tab.project}/{tab.file} · {tab.head ? 'all changes' : tab.staged ? 'staged' : tab.untracked ? 'new file' : 'working tree'}</span>
                <button type="button" onclick={() => openFile(joinPath(tab.project, tab.file))} class="px-1.5 py-0.5 hover:bg-gray-100 dark:hover:bg-dark-elevated">Open file</button>
                <button type="button" onclick={() => loadDiff(tab.key)} class="p-1 hover:bg-gray-100 dark:hover:bg-dark-elevated" aria-label="Reload diff"><RefreshCw size={13} /></button>
              </div>
              <div class="min-h-0 flex-1 overflow-auto bg-white dark:bg-dark-base">
                {#if tab.loading}
                  <p class="p-4 text-sm text-gray-500">Loading…</p>
                {:else if !tab.diff.trim()}
                  <p class="p-4 text-sm text-gray-500">No differences.</p>
                {:else}
                  <pre class="min-w-max py-2 font-mono text-xs leading-5">{#each tab.diff.split('\n') as line}<div class={['px-4', diffLineClass(line)]}>{line || ' '}</div>{/each}</pre>
                {/if}
              </div>
            {/if}
          </div>
        {:else}
          <div class="flex h-full flex-col items-center justify-center gap-3 p-6 text-center text-sm text-gray-500 dark:text-dark-text-muted">
            <FolderGit2 size={32} class="text-gray-300 dark:text-dark-border" />
            <p class="max-w-md">Pick a project from the top bar, open a file from the side bar, or start a chat with the coding agent.</p>
            <div class="flex flex-wrap justify-center gap-2">
              <button type="button" onclick={newSession} class="inline-flex items-center gap-1.5 bg-gray-900 px-3 py-1.5 text-xs font-medium text-white hover:bg-gray-800 dark:bg-accent"><MessageSquare size={13} /> New chat{project ? ` in ${project}` : ''}</button>
              <button type="button" onclick={() => (newProjectMode = 'clone')} class="inline-flex items-center gap-1.5 border border-gray-300 dark:border-dark-border px-3 py-1.5 text-xs hover:bg-gray-50 dark:hover:bg-dark-elevated"><GitBranch size={13} /> Clone a repository</button>
            </div>
          </div>
        {/each}
      </div>

      {#if showTerminal}
        {#if !terminalMinimized || space?.status !== 'ready'}
          <div role="separator" aria-orientation="horizontal" aria-label="Resize terminal" class="h-1 shrink-0 cursor-row-resize bg-gray-200 hover:bg-accent/50 dark:bg-dark-border" onpointerdown={resizeTerminal}></div>
        {/if}
        {#if space?.status === 'ready'}
          <!-- Minimizing clips the panel to its tab strip instead of unmounting
               it, so the shells stay connected and xterm keeps its size. -->
          <div class="shrink-0 overflow-hidden" style={`height:${terminalMinimized ? 32 : terminalHeight}px`}>
            <div style={`height:${terminalHeight}px`}>
              <TerminalPanel tabs={terminals} active={activeTerminal} minimized={terminalMinimized} ontoggleminimize={() => (terminalMinimized = !terminalMinimized)} onselect={id => (activeTerminal = id)} onclose={closeTerminal} onnew={newTerminal} onreconnect={id => { terminals = terminals.map(t => (t.id === id ? { ...t, generation: t.generation + 1 } : t)); }} />
            </div>
          </div>
        {:else}
          <div class="flex shrink-0 items-center justify-center bg-[#1e1e1e] text-xs text-gray-400" style={`height:${terminalHeight}px`}>{starting ? 'Starting your space…' : 'Start the space to open a terminal.'}</div>
        {/if}
      {/if}
    </div>

    <!-- Right: source control -->
    {#if rightView === 'git'}
      <div role="separator" aria-orientation="vertical" aria-label="Resize source control" class="w-1 shrink-0 cursor-col-resize hover:bg-accent/40 max-md:hidden" onpointerdown={resizeRight}></div>
      <aside class="flex min-h-0 shrink-0 flex-col border-l border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-surface max-md:absolute max-md:inset-y-10 max-md:right-0 max-md:z-20 max-md:w-[85vw] max-md:shadow-xl" style={`width:${rightWidth}px`}>
        <div class="flex h-9 shrink-0 items-center border-b border-gray-200 dark:border-dark-border px-3">
          <h3 class="flex-1 text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-dark-text-muted">Source control{project ? ` · ${project}` : ''}</h3>
        </div>
        <div class="min-h-0 flex-1">
          {#if space?.status === 'ready'}
            <GitPanel project={project} revision={gitRevision} ondiff={openDiff} onchanged={() => { void refreshTree(); for (const t of tabs) if (t.kind === 'file' && t.content === t.saved) void loadFileTab(t.key); }} />
          {:else}
            <p class="p-3 text-xs text-gray-500">Start the space to see changes.</p>
          {/if}
        </div>
      </aside>
    {/if}
  </div>
</div>
{/if}

{#if menu}
  <div role="menu" tabindex="-1" class="fixed z-50 w-48 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface py-1 text-xs shadow-lg" style={`left:${menu.x}px;top:${menu.y}px`} onclick={event => event.stopPropagation()} onkeydown={() => {}}>
    {#if menu.entry && menu.entry.type !== 'dir'}
      <button type="button" role="menuitem" onclick={() => { const p = menu!.entry!.path; menu = null; void openFile(p); }} class="block w-full px-3 py-1.5 text-left hover:bg-gray-100 dark:hover:bg-dark-elevated">Open</button>
      <button type="button" role="menuitem" onclick={() => { const p = menu!.entry!.path; menu = null; void download(p); }} class="block w-full px-3 py-1.5 text-left hover:bg-gray-100 dark:hover:bg-dark-elevated">Download</button>
    {/if}
    <button type="button" role="menuitem" onclick={() => createEntry(menu!.folder, 'file')} class="block w-full px-3 py-1.5 text-left hover:bg-gray-100 dark:hover:bg-dark-elevated">New file</button>
    <button type="button" role="menuitem" onclick={() => createEntry(menu!.folder, 'dir')} class="block w-full px-3 py-1.5 text-left hover:bg-gray-100 dark:hover:bg-dark-elevated">New folder</button>
    <button type="button" role="menuitem" onclick={() => pickUpload(menu!.folder)} class="block w-full px-3 py-1.5 text-left hover:bg-gray-100 dark:hover:bg-dark-elevated">Upload files…</button>
    {#if menu.entry?.type === 'dir'}
      <div class="my-1 border-t border-gray-200 dark:border-dark-border"></div>
      {#if !menu.entry.path.includes('/')}
        <button type="button" role="menuitem" onclick={() => { const p = menu!.entry!.path; menu = null; void openProject(p); }} class="block w-full px-3 py-1.5 text-left hover:bg-gray-100 dark:hover:bg-dark-elevated">Open as project</button>
      {/if}
      <button type="button" role="menuitem" onclick={() => { const cwd = menu!.entry!.path; menu = null; terminals = [...terminals, { id: `t${Date.now()}`, cwd, generation: 0 }]; activeTerminal = terminals[terminals.length - 1].id; showTerminal = true; terminalMinimized = false; }} class="block w-full px-3 py-1.5 text-left hover:bg-gray-100 dark:hover:bg-dark-elevated">Open terminal here</button>
    {/if}
    {#if menu.entry}
      <div class="my-1 border-t border-gray-200 dark:border-dark-border"></div>
      <button type="button" role="menuitem" onclick={() => renameEntry(menu!.entry!)} class="block w-full px-3 py-1.5 text-left hover:bg-gray-100 dark:hover:bg-dark-elevated">Rename…</button>
      <button type="button" role="menuitem" onclick={() => deleteEntry(menu!.entry!)} class="block w-full px-3 py-1.5 text-left text-red-700 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/40">Delete</button>
    {/if}
  </div>
{/if}
