<script lang="ts">
  import { routeChoice } from '@/lib/helper/route-choice.svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listSkills,
    createSkill,
    updateSkill,
    deleteSkill,
    publishSkill,
    listSkillTemplates,
    installSkillTemplate,
    exportSkill,
    exportSkillMD,
    importSkillFromURL,
    importSkillMD,
    importSkillFiles,
    getOAuthStartURL,
    type Skill,
    type SkillFile,
    type SkillTool,
    type SkillTemplate,
  } from '@/lib/api/skills';
  import {
    listVariables,
    createVariable,
    updateVariable,
    type Variable,
  } from '@/lib/api/secrets';
  import {
    listMarketplaceSources,
    searchMarketplace,
    getTopSkills as getTopMarketplaceSkills,
    previewMarketplaceSkill,
    importMarketplaceSkill,
    updateMarketplaceSource,
    createMarketplaceSource,
    deleteMarketplaceSource,
    type MarketplaceSource,
    type MarketplaceSkill,
  } from '@/lib/api/marketplace';
  import { Plus, Pencil, Trash2, X, Save, RefreshCw, Wand2, Bot, Copy, ClipboardPaste, Download, Upload, Store, Check, ExternalLink, Globe, Settings, Search, Eye, FileText, FolderOpen, Share2, Users } from 'lucide-svelte';
  import SkillBuilderPanel from '@/lib/components/SkillBuilderPanel.svelte';
  import SkillFilesDialog from '@/lib/components/SkillFilesDialog.svelte';
  import { listGitCredentials, type GitCredential } from '@/lib/api/git-credentials';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';
  import { can } from '@/lib/store/workspace.svelte';
  import { isNativeAdmin, storeAuth } from '@/lib/store/auth.svelte';
  import { listAgents, type Agent } from '@/lib/api/agents';

  storeNavbar.title = 'Skills';

  // ─── Tab State ───

  const tabRoute = routeChoice('tab', ['my-skills', 'workspace-skills', 'store', 'community'] as const, 'my-skills');
  let activeTab = $derived(tabRoute.value);
  let mayPublish = $derived(isNativeAdmin() || can('skills.write'));

  // ─── State ───

  let skills = $state<Skill[]>([]);
  let agents = $state<Agent[]>([]);
  let loading = $state(true);
  
  // Pagination
  let offset = $state(0);
  let limit = $state(25);
  let total = $state(0);

  // Search & Sort
  let searchQuery = $state('');
  let sorts = $state<SortEntry[]>([]);

  // Category filter for My Skills tab
  let mySelectedCategory = $state('');
  let scopedSkills = $derived((skills || []).filter((skill) => activeTab === 'workspace-skills' ? !skill.owner_user_id : Boolean(skill.owner_user_id)));
  let myCategories = $derived([...new Set(scopedSkills.map((s) => s.category).filter((c): c is string => Boolean(c)))].sort());
  let filteredSkills = $derived(
    mySelectedCategory
      ? scopedSkills.filter((s) => s.category === mySelectedCategory)
      : scopedSkills
  );
  let pagedSkills = $derived(filteredSkills.slice(offset, offset + limit));

  let showForm = $state(false);
  let editingId = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);
  let showAIPanel = $state(false);
  let folderSkill = $state<Skill | null>(null);
  let createFolderFiles = $state<Array<{ file: globalThis.File; path: string }>>([]);
  let createFolderDragging = $state(false);
  let importingFolder = $state(false);
  let createFolderInput = $state<HTMLInputElement>();

  // Form fields
  let formName = $state('');
  let formDescription = $state('');
  let formCategory = $state('');
  let formTags = $state<string[]>([]);
  let formSystemPrompt = $state('');
  let formTools = $state<SkillTool[]>([]);
  let formContext = $state<'' | 'fork'>('');
  let formAgent = $state('');
  let formBackground = $state(false);
  let saving = $state(false);

  // Copy / Paste via system clipboard (works across browsers/machines)

  async function copySkill(skill: Skill) {
    const exportData = {
      name: skill.name,
      description: skill.description,
      system_prompt: skill.system_prompt,
      tools: skill.tools || [],
      context: skill.context || undefined,
      agent: skill.agent || undefined,
      background: skill.background || undefined,
    };
    try {
      await navigator.clipboard.writeText(JSON.stringify(exportData, null, 2));
      addToast(`Copied "${skill.name}" to clipboard`);
    } catch {
      addToast('Failed to copy to clipboard', 'alert');
    }
  }

  async function pasteSkill() {
    try {
      const text = await navigator.clipboard.readText();
      const src = JSON.parse(text);
      if (!src.name || typeof src.name !== 'string') {
        addToast('Clipboard does not contain a valid skill', 'warn');
        return;
      }
      resetForm();
      formName = src.name + '_copy';
      formDescription = src.description || '';
      formCategory = src.category || '';
      formTags = Array.isArray(src.tags) ? [...src.tags] : [];
      formSystemPrompt = src.system_prompt || '';
      formTools = (src.tools || []).map((t: any) => ({
        name: t.name || '',
        description: t.description || '',
        inputSchema: t.inputSchema || {},
        handler: t.handler || '',
        handler_type: t.handler_type || 'js',
      }));
      formContext = src.context === 'fork' ? 'fork' : '';
      formAgent = formContext === 'fork' ? (src.agent || '') : '';
      formBackground = formContext === 'fork' && Boolean(src.background);
      editingId = null;
      showForm = true;
    } catch {
      addToast('Nothing to paste — copy a skill first or check clipboard permissions', 'warn');
    }
  }

  // ─── Load ───

  async function load() {
    loading = true;
    try {
      const params: any = { _limit: 500 };
      if (searchQuery) params['name[like]'] = `%${searchQuery}%`;
      const sortParam = buildSortParam(sorts);
      if (sortParam) params._sort = sortParam;
      const res = await listSkills(params);
      skills = res.data || [];
      total = (res.data || []).length;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load skills', 'alert');
    } finally {
      loading = false;
    }
  }

  async function loadAgents() {
    try {
      const res = await listAgents({ _limit: 500 } as any);
      agents = res.data || [];
    } catch {
      agents = [];
    }
  }

  function handleSearch(value: string) {
    searchQuery = value;
    offset = 0;
    load();
  }

  function handleSort(field: string, multiSort: boolean) {
    sorts = toggleSort(sorts, field, multiSort);
    offset = 0;
    load();
  }

  load();
  loadAgents();

  // ─── Form ───

  function resetForm() {
    formName = '';
    formDescription = '';
    formCategory = '';
    formTags = [];
    formSystemPrompt = '';
    formTools = [];
    formContext = '';
    formAgent = '';
    formBackground = false;
    createFolderFiles = [];
    editingId = null;
    showForm = false;
  }

  function openCreate() {
    resetForm();
    showForm = true;
  }

  function openEdit(skill: Skill) {
    resetForm();
    editingId = skill.id;
    formName = skill.name;
    formDescription = skill.description;
    formCategory = skill.category || '';
    formTags = skill.tags ? [...skill.tags] : [];
    formSystemPrompt = skill.system_prompt;
    formTools = (skill.tools || []).map((t) => ({ ...t }));
    formContext = skill.context === 'fork' ? 'fork' : '';
    formAgent = skill.agent || '';
    formBackground = Boolean(skill.background);
    showForm = true;
  }

  function openEditWithAI(skill: Skill) {
    openEdit(skill);
    showAIPanel = true;
  }

  async function handleSubmit() {
    if (!formName.trim()) {
      addToast('Skill name is required', 'warn');
      return;
    }
    if (formContext === 'fork' && !formAgent) {
      addToast('Select an agent for the forked skill', 'warn');
      return;
    }

    saving = true;
    try {
      const payload: Partial<Skill> = {
        name: formName.trim(),
        description: formDescription.trim(),
        category: formCategory.trim() || undefined,
        tags: formTags.length > 0 ? formTags : undefined,
        system_prompt: formSystemPrompt,
        tools: formTools.filter((t) => t.name.trim()),
        context: formContext || undefined,
        agent: formContext === 'fork' ? formAgent : undefined,
        background: formContext === 'fork' ? formBackground : undefined,
      };

      if (editingId) {
        await updateSkill(editingId, payload);
        addToast(`Skill "${formName}" updated`);
      } else {
        const created = await createSkill(payload);
        addToast(`Skill "${formName}" created`);
        folderSkill = created;
      }
      resetForm();
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save skill', 'alert');
    } finally {
      saving = false;
    }
  }

  async function decodeFolderFile(file: globalThis.File): Promise<string> {
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

  async function createFilesFromEntry(entry: any, prefix = ''): Promise<Array<{ file: globalThis.File; path: string }>> {
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
    const nested = await Promise.all(entries.map((child) => createFilesFromEntry(child, `${prefix}${entry.name}/`)));
    return nested.flat();
  }

  function visibleSkillFiles(items: Array<{ file: globalThis.File; path: string }>) {
    return items.filter((item) => !item.path.replaceAll('\\', '/').split('/').some((part) => part.startsWith('.')));
  }

  function selectCreateFolder(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    createFolderFiles = visibleSkillFiles(Array.from(input.files || []).map((file) => ({
      file,
      path: file.webkitRelativePath || file.name,
    })));
    input.value = '';
  }

  async function dropCreateFolder(event: DragEvent) {
    event.preventDefault();
    createFolderDragging = false;
    const items = Array.from(event.dataTransfer?.items || []);
    const entries = items.map((item: any) => item.webkitGetAsEntry?.()).filter(Boolean);
    if (entries.length) {
      const nested = await Promise.all(entries.map((entry) => createFilesFromEntry(entry)));
      createFolderFiles = visibleSkillFiles(nested.flat());
      return;
    }
    createFolderFiles = visibleSkillFiles(Array.from(event.dataTransfer?.files || []).map((file) => ({ file, path: file.name })));
  }

  async function handleImportFolder() {
    if (!createFolderFiles.length) {
      addToast('Choose a folder containing SKILL.md', 'warn');
      return;
    }
    importingFolder = true;
    try {
      const files: SkillFile[] = [];
      for (const item of createFolderFiles) {
        files.push({ path: item.path, content: await decodeFolderFile(item.file), media_type: item.file.type || undefined });
      }
      const created = await importSkillFiles(files);
      addToast(`Skill "${created.name}" created from folder`);
      resetForm();
      await load();
      folderSkill = created;
    } catch (e: any) {
      addToast(e?.response?.data?.message || e?.message || 'Failed to import skill folder', 'alert');
    } finally {
      importingFolder = false;
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteSkill(id);
      addToast('Skill deleted');
      deleteConfirm = null;
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete skill', 'alert');
    }
  }

  async function handlePublish(skill: Skill) {
    if (!confirm(`Copy "${skill.name}" to Workspace Skills?`)) return;
    try {
      await publishSkill(skill.id);
      addToast(`Skill "${skill.name}" copied to the workspace`);
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to publish skill', 'alert');
    }
  }

  // ─── Tools Management ───

  function addTool() {
    const tool: SkillTool = { name: '', description: '', inputSchema: {}, handler: '', handler_type: 'js' };
    formTools = [...formTools, tool];
  }

  function removeTool(index: number) {
    formTools = formTools.filter((_, i) => i !== index);
  }

  function updateToolSchema(index: number, value: string) {
    try {
      formTools[index].inputSchema = JSON.parse(value);
    } catch {
      // Keep old value on invalid JSON
    }
  }

  // ─── Skill Store ───

  let templates = $state<SkillTemplate[]>([]);
  let storeLoading = $state(false);
  let selectedCategory = $state('');
  let installedSlugs = $state<Set<string>>(new Set());

  async function loadTemplates() {
    storeLoading = true;
    try {
      const cat = selectedCategory || undefined;
      templates = await listSkillTemplates(cat);
      // Fetch ALL installed skills (not just current page) to check installed status
      const allSkillsRes = await listSkills({ _limit: 500 });
      const allSkillNames = new Set((allSkillsRes.data || []).filter((s: Skill) => Boolean(s.owner_user_id)).map((s: Skill) => s.name));
      installedSlugs = new Set(templates.filter((t) => allSkillNames.has(t.skill.name)).map((t) => t.slug));
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load templates', 'alert');
    } finally {
      storeLoading = false;
    }
  }

  async function handleInstallTemplate(slug: string) {
    try {
      await installSkillTemplate(slug);
      addToast('Skill installed from template');
      installedSlugs = new Set([...installedSlugs, slug]);
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to install template', 'alert');
    }
  }

  function selectCategory(cat: string) {
    selectedCategory = cat === selectedCategory ? '' : cat;
    loadTemplates();
  }

  // ─── OAuth Setup for Templates ───

  let oauthSetupSlug = $state(''); // which template is being set up
  let oauthVarInputs = $state<Record<string, string>>({}); // key -> value for required variables
  let oauthConnected = $state<Record<string, boolean>>({}); // provider -> connected
  let existingVars = $state<Map<string, string>>(new Map()); // key -> id

  async function loadExistingVars() {
    try {
      const res = await listVariables({ _limit: 1000 });
      existingVars = new Map(res.data.map((v: Variable) => [v.key, v.id]));
    } catch { /* ignore */ }
  }

  function startOAuthSetup(slug: string) {
    oauthSetupSlug = oauthSetupSlug === slug ? '' : slug;
    oauthVarInputs = {};
    loadExistingVars();
  }

  async function saveOAuthVars(tmpl: SkillTemplate) {
    try {
      for (const rv of tmpl.required_variables) {
        const val = oauthVarInputs[rv.key];
        if (!val && !existingVars.has(rv.key)) {
          addToast(`Please enter ${rv.key}`, 'alert');
          return;
        }
        if (val) {
          const existingId = existingVars.get(rv.key);
          if (existingId) {
            await updateVariable(existingId, { key: rv.key, value: val, description: rv.description, secret: rv.secret });
          } else {
            const created = await createVariable({ key: rv.key, value: val, description: rv.description, secret: rv.secret });
            existingVars = new Map([...existingVars, [rv.key, created.id]]);
          }
        }
      }
      addToast('Variables saved');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save variables', 'alert');
    }
  }

  async function startOAuthConnect(provider: string) {
    try {
      const url = await getOAuthStartURL(provider);
      const popup = window.open(url, 'oauth', 'width=500,height=600');

      const handler = (event: MessageEvent) => {
        if (event.data?.type === 'oauth-result') {
          window.removeEventListener('message', handler);
          if (event.data.status === 'success') {
            oauthConnected = { ...oauthConnected, [provider]: true };
            existingVars = new Map([...existingVars, [provider + '_refresh_token', 'oauth']]);
            addToast('Account connected successfully');
          } else {
            addToast('OAuth failed', 'alert');
          }
        }
      };
      window.addEventListener('message', handler);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to start OAuth', 'alert');
    }
  }

  function oauthAllVarsReady(tmpl: SkillTemplate): boolean {
    for (const rv of tmpl.required_variables) {
      if (!existingVars.has(rv.key)) return false;
    }
    return true;
  }

  function oauthRefreshTokenReady(provider: string): boolean {
    return existingVars.has(provider + '_refresh_token') || oauthConnected[provider];
  }

  // ─── Export ───

  async function handleExport(skill: Skill) {
    try {
      const mdContent = await exportSkillMD(skill.id);
      const blob = new Blob([mdContent], { type: 'text/markdown' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `${skill.name}.md`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
      addToast(`Exported "${skill.name}" as ${skill.name}.md`);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to export skill', 'alert');
    }
  }

  // ─── Import from URL ───

  let showImportURL = $state(false);
  let importURL = $state('');
  let importRepository = $state(false);
  let importRef = $state('');
  let importPath = $state('');
  let importAll = $state(false);
  let importCredentialID = $state('');
  let gitCredentials = $state<GitCredential[]>([]);
  let gitCredentialsLoaded = $state(false);
  let importingURL = $state(false);

  async function handleImportURL() {
    if (!importURL.trim()) {
      addToast('URL is required', 'warn');
      return;
    }
    importingURL = true;
    try {
      const result = await importSkillFromURL(importURL.trim(), importRepository ? {
        repository: true,
        ref: importRef.trim() || undefined,
        path: importPath.trim() || undefined,
        import_all: importAll,
        credential_id: importCredentialID || undefined,
      } : {});
      const count = 'skills' in result ? result.skills.length : 1;
      addToast(count === 1 ? 'Skill imported' : `${count} skills imported`);
      importURL = '';
      importRef = '';
      importPath = '';
      importAll = false;
      importCredentialID = '';
      showImportURL = false;
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to import skill from URL', 'alert');
    } finally {
      importingURL = false;
    }
  }

  async function loadGitCredentials() {
    if (gitCredentialsLoaded) return;
    try { gitCredentials = await listGitCredentials(); gitCredentialsLoaded = true; }
    catch (e: any) { addToast(e?.response?.data?.message || 'Failed to load Git credentials', 'alert'); }
  }

  // ─── Import Raw SKILL.md ───

  let showImportRaw = $state(false);
  let importRawContent = $state('');
  let importingRaw = $state(false);

  async function handleImportRaw() {
    if (!importRawContent.trim()) {
      addToast('SKILL.md content is required', 'warn');
      return;
    }
    importingRaw = true;
    try {
      await importSkillMD(importRawContent.trim());
      addToast('Skill imported from SKILL.md');
      importRawContent = '';
      showImportRaw = false;
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to import SKILL.md', 'alert');
    } finally {
      importingRaw = false;
    }
  }

  // ─── Community Marketplace ───

  let communitySources = $state<MarketplaceSource[]>([]);
  let communitySkills = $state<MarketplaceSkill[]>([]);
  let communityLoading = $state(false);
  let communitySearchQuery = $state('');
  let communitySourceFilter = $state('');
  let showManageSources = $state(false);
  let searchTimeout: ReturnType<typeof setTimeout> | null = null;

  // Preview modal
  let previewSkill = $state<any>(null);
  let previewLoading = $state(false);
  let previewData = $state<any>(null);

  // Add source form
  let showAddSource = $state(false);
  let newSourceName = $state('');
  let newSourceType = $state('generic');
  let newSourceSearchURL = $state('');
  let newSourceTopURL = $state('');

  async function loadCommunitySources() {
    try {
      communitySources = await listMarketplaceSources();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load marketplace sources', 'alert');
    }
  }

  async function loadCommunitySkills() {
    communityLoading = true;
    try {
      if (communitySearchQuery.trim()) {
        const res = await searchMarketplace(communitySearchQuery, communitySourceFilter || undefined);
        communitySkills = res.skills || [];
      } else {
        const res = await getTopMarketplaceSkills(communitySourceFilter || undefined);
        communitySkills = res.skills || [];
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to search marketplace', 'alert');
    } finally {
      communityLoading = false;
    }
  }

  function handleCommunitySearch(value: string) {
    communitySearchQuery = value;
    if (searchTimeout) clearTimeout(searchTimeout);
    searchTimeout = setTimeout(() => loadCommunitySkills(), 300);
  }

  function handleSourceFilter(sourceId: string) {
    communitySourceFilter = communitySourceFilter === sourceId ? '' : sourceId;
    loadCommunitySkills();
  }

  async function handlePreview(skill: MarketplaceSkill) {
    previewSkill = skill;
    previewLoading = true;
    previewData = null;
    try {
      previewData = await previewMarketplaceSkill(skill.url);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to preview skill', 'alert');
    } finally {
      previewLoading = false;
    }
  }

  async function handleCommunityImport(url: string) {
    try {
      await importMarketplaceSkill(url);
      addToast('Skill imported from marketplace');
      previewSkill = null;
      previewData = null;
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to import skill', 'alert');
    }
  }

  async function toggleSourceEnabled(src: MarketplaceSource) {
    try {
      await updateMarketplaceSource(src.id, { ...src, enabled: !src.enabled });
      await loadCommunitySources();
      addToast(`${src.name} ${!src.enabled ? 'enabled' : 'disabled'}`);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to update source', 'alert');
    }
  }

  async function handleAddSource() {
    if (!newSourceName.trim() || !newSourceSearchURL.trim()) {
      addToast('Name and Search URL are required', 'warn');
      return;
    }
    try {
      await createMarketplaceSource({
        name: newSourceName.trim(),
        type: newSourceType,
        search_url: newSourceSearchURL.trim(),
        top_url: newSourceTopURL.trim(),
        enabled: true,
      });
      addToast('Source added');
      newSourceName = '';
      newSourceType = 'generic';
      newSourceSearchURL = '';
      newSourceTopURL = '';
      showAddSource = false;
      await loadCommunitySources();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to add source', 'alert');
    }
  }

  async function handleDeleteSource(id: string) {
    try {
      await deleteMarketplaceSource(id);
      addToast('Source deleted');
      await loadCommunitySources();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete source', 'alert');
    }
  }

  // Reactive: load templates when switching to store tab
  $effect(() => {
    if (activeTab === 'store') {
      loadTemplates();
    }
  });

  $effect(() => {
    if (activeTab === 'community') {
      loadCommunitySources();
      loadCommunitySkills();
    }
  });
</script>

<svelte:head>
  <title>AT | Skills</title>
</svelte:head>

<div class="flex h-full">
  <!-- Main content -->
  <div class="flex-1 overflow-y-auto">
    <div class="p-6 max-w-6xl mx-auto">
      <!-- Tab Bar -->
      <div class="flex items-center gap-4 mb-4 border-b border-gray-200 dark:border-dark-border">
        <button
          onclick={() => (tabRoute.value = 'my-skills')}
          class="flex items-center gap-1.5 px-1 pb-2 text-sm font-medium border-b-2 {activeTab === 'my-skills' ? 'border-gray-900 dark:border-accent text-gray-900 dark:text-dark-text' : 'border-transparent text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary'}"
        >
          <Wand2 size={14} />
          My Skills
          <span class="text-xs text-gray-400 dark:text-dark-text-muted">({skills.filter((skill) => Boolean(skill.owner_user_id)).length})</span>
        </button>
        <button
          onclick={() => { tabRoute.value = 'workspace-skills'; offset = 0; mySelectedCategory = ''; resetForm(); }}
          class="flex items-center gap-1.5 px-1 pb-2 text-sm font-medium border-b-2 {activeTab === 'workspace-skills' ? 'border-gray-900 dark:border-accent text-gray-900 dark:text-dark-text' : 'border-transparent text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary'}"
        >
          <Users size={14} />
          Workspace Skills
          <span class="text-xs text-gray-400 dark:text-dark-text-muted">({skills.filter((skill) => !skill.owner_user_id).length})</span>
        </button>
        <button
          onclick={() => (tabRoute.value = 'store')}
          class="flex items-center gap-1.5 px-1 pb-2 text-sm font-medium border-b-2 {activeTab === 'store' ? 'border-gray-900 dark:border-accent text-gray-900 dark:text-dark-text' : 'border-transparent text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary'}"
        >
          <Store size={14} />
          Skill Store
        </button>
        <button
          onclick={() => (tabRoute.value = 'community')}
          class="flex items-center gap-1.5 px-1 pb-2 text-sm font-medium border-b-2 {activeTab === 'community' ? 'border-gray-900 dark:border-accent text-gray-900 dark:text-dark-text' : 'border-transparent text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary'}"
        >
          <Globe size={14} />
          Community
        </button>
      </div>

      {#if activeTab === 'my-skills' || activeTab === 'workspace-skills'}
      <!-- Header -->
      <div class="flex items-center justify-between mb-4">
        <div class="flex items-center gap-2">
          <Wand2 size={16} class="text-gray-500 dark:text-dark-text-muted" />
          <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Skills</h2>
        </div>
        <div class="flex items-center gap-2">
          {#if activeTab === 'my-skills'}
          <button
            onclick={() => { showAIPanel = !showAIPanel; }}
            class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium {showAIPanel ? 'bg-accent-muted text-accent dark:text-accent-text border border-accent/30' : 'border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated'}"
            title="Toggle AI Skill Builder"
          >
            <Bot size={12} />
            AI Builder
          </button>
          <button
            onclick={() => { showImportURL = !showImportURL; if (showImportURL) showImportRaw = false; }}
            class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated "
            title="Import skill from URL"
          >
            <ExternalLink size={12} />
            Import URL
          </button>
          <button
            onclick={() => { showImportRaw = !showImportRaw; if (showImportRaw) showImportURL = false; }}
            class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated "
            title="Paste raw SKILL.md content"
          >
            <FileText size={12} />
            Paste SKILL.md
          </button>
          {/if}
          <button
            onclick={load}
            class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary "
            title="Refresh"
          >
            <RefreshCw size={14} />
          </button>
          {#if activeTab === 'my-skills'}
          <button
            onclick={openCreate}
            class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover "
          >
            <Plus size={12} />
            New Skill
          </button>
          {/if}
        </div>
      </div>

      <!-- Import from URL -->
      {#if showImportURL}
        <div class="mb-4 p-3 border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50 space-y-3">
          <div class="flex items-center justify-between gap-3">
            <div>
              <div class="text-xs font-medium text-gray-800 dark:text-dark-text">Import a skill source</div>
              <p class="mt-0.5 text-[11px] text-gray-500 dark:text-dark-text-muted">Use a raw JSON/SKILL.md URL or clone a Git repository package with bundled references.</p>
            </div>
            <button type="button" aria-label="Close import form" onclick={() => { showImportURL = false; }} class="p-1.5 shrink-0 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary">
              <X size={14} />
            </button>
          </div>
          <label class="flex items-center gap-2 text-xs text-gray-700 dark:text-dark-text-secondary">
            <input type="checkbox" bind:checked={importRepository} onchange={() => { if (importRepository) void loadGitCredentials(); }} class="size-3.5" />
            Git repository
          </label>
          <div class="grid grid-cols-1 gap-2 {importRepository ? 'md:grid-cols-[minmax(0,2fr)_minmax(8rem,0.7fr)_minmax(0,1fr)]' : ''}">
            <label class="min-w-0 text-[11px] text-gray-500 dark:text-dark-text-muted">
              {importRepository ? 'Clone URL' : 'File URL'}
              <input type="text" bind:value={importURL} placeholder={importRepository ? 'https://git.example.com/team/skills.git' : 'https://example.com/SKILL.md'} class="mt-1 w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:text-dark-text dark:placeholder:text-dark-text-muted" />
            </label>
            {#if importRepository}
              <label class="min-w-0 text-[11px] text-gray-500 dark:text-dark-text-muted">
                Branch or tag
                <input type="text" bind:value={importRef} placeholder="default branch" class="mt-1 w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:text-dark-text dark:placeholder:text-dark-text-muted" />
              </label>
              <label class="min-w-0 text-[11px] text-gray-500 dark:text-dark-text-muted">
                Path inside repository
                <input type="text" bind:value={importPath} placeholder="skills/my-skill" class="mt-1 w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:text-dark-text dark:placeholder:text-dark-text-muted" />
              </label>
            {/if}
          </div>
          {#if importRepository}
            <div class="grid grid-cols-1 gap-2 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end">
              <label class="min-w-0 text-[11px] text-gray-500 dark:text-dark-text-muted">
                SSH credential
                <select bind:value={importCredentialID} class="mt-1 w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:text-dark-text">
                  <option value="">Server SSH configuration</option>
                  {#each gitCredentials as credential}<option value={credential.id}>{credential.name} — {credential.host}</option>{/each}
                </select>
              </label>
              <a href="#/settings/git-credentials" class="inline-flex items-center justify-center px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated">Manage deploy keys</a>
            </div>
          {/if}
          <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            {#if importRepository}
              <label class="flex items-center gap-2 text-xs text-gray-700 dark:text-dark-text-secondary">
                <input type="checkbox" bind:checked={importAll} class="size-3.5" />
                Import every skill found under this path
              </label>
            {:else}
              <span></span>
            {/if}
            <button onclick={handleImportURL} disabled={importingURL} class="flex items-center justify-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-50">
              <Upload size={12} />
              {importingURL ? (importRepository ? 'Cloning...' : 'Importing...') : 'Import'}
            </button>
          </div>
        </div>
      {/if}

      <!-- Import Raw SKILL.md -->
      {#if showImportRaw}
        <div class="mb-4 p-3 border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50 space-y-2">
          <div class="flex items-center justify-between">
            <span class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Paste raw SKILL.md content</span>
            <button
              onclick={() => { showImportRaw = false; }}
              class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary "
            >
              <X size={14} />
            </button>
          </div>
          <textarea
            bind:value={importRawContent}
            rows={10}
            placeholder={"---\nname: my_skill\ndescription: What this skill does\n---\n\n# Instructions\n\nYour system prompt content here..."}
            class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-2 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 resize-y dark:text-dark-text dark:placeholder:text-dark-text-muted"
          ></textarea>
          <div class="flex justify-end">
            <button
              onclick={handleImportRaw}
              disabled={importingRaw}
              class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-50"
            >
              <Upload size={12} />
              {importingRaw ? 'Importing...' : 'Import SKILL.md'}
            </button>
          </div>
        </div>
      {/if}

      <!-- Category Filter Chips -->
      {#if myCategories.length > 0}
        <div class="flex items-center gap-2 px-4 py-2 flex-wrap">
          <button
            onclick={() => mySelectedCategory = ''}
            class={["px-2 py-0.5 text-xs border ", !mySelectedCategory ? 'bg-gray-900 dark:bg-accent text-white border-gray-900 dark:border-accent' : 'border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated']}
          >All</button>
          {#each myCategories as cat}
            <button
              onclick={() => mySelectedCategory = cat}
              class={["px-2 py-0.5 text-xs border ", mySelectedCategory === cat ? 'bg-gray-900 dark:bg-accent text-white border-gray-900 dark:border-accent' : 'border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated']}
            >{cat}</button>
          {/each}
        </div>
      {/if}

      <!-- Form -->
      {#if showForm}
        <div class="border border-gray-200 dark:border-dark-border mb-6 bg-white dark:bg-dark-surface overflow-hidden">
          <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50">
            <div class="flex items-center gap-2">
              <span class="text-sm font-medium text-gray-900 dark:text-dark-text">
                {editingId ? `Edit: ${formName}` : 'New Skill'}
              </span>
              {#if !editingId}
                <button
                  type="button"
                  onclick={pasteSkill}
                  class="flex items-center gap-1 px-2 py-1 text-xs font-medium border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-muted hover:bg-gray-100 dark:hover:bg-dark-elevated hover:text-gray-900 dark:hover:text-dark-text "
                  title="Paste skill from clipboard"
                >
                  <ClipboardPaste size={12} />
                  Paste
                </button>
              {/if}
            </div>
            <button onclick={resetForm} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary ">
              <X size={14} />
            </button>
          </div>

          <form onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="p-4 space-y-4">
            {#if !editingId}
              <div class="border border-gray-200 bg-gray-50 p-3 dark:border-dark-border dark:bg-dark-base/50">
                <div class="mb-2 flex items-start justify-between gap-3">
                  <div>
                    <div class="flex items-center gap-1.5 text-sm font-medium text-gray-800 dark:text-dark-text">
                      <FolderOpen size={14} />
                      Create from a skill folder
                    </div>
                    <p class="mt-0.5 text-[11px] text-gray-500 dark:text-dark-text-muted">
                      Select or drop one folder containing SKILL.md. All nested text files are imported with their folder structure.
                    </p>
                  </div>
                  {#if createFolderFiles.length > 0}
                    <button type="button" onclick={() => createFolderFiles = []} class="shrink-0 p-1 text-gray-400 hover:bg-gray-200 hover:text-gray-700 dark:text-dark-text-muted dark:hover:bg-dark-elevated"><X size={13} /></button>
                  {/if}
                </div>
                <div
                  role="region"
                  aria-label="Drop a skill folder"
                  class={['flex min-h-24 flex-col items-center justify-center border border-dashed px-4 py-3 text-center', createFolderDragging ? 'border-blue-500 bg-blue-50 text-blue-700 dark:bg-accent-muted dark:text-accent-text' : 'border-gray-300 bg-white text-gray-500 dark:border-dark-border-subtle dark:bg-dark-surface dark:text-dark-text-muted']}
                  ondragover={(event) => { event.preventDefault(); createFolderDragging = true; }}
                  ondragleave={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node)) createFolderDragging = false; }}
                  ondrop={dropCreateFolder}
                >
                  <FolderOpen size={22} class="mb-1" />
                  {#if createFolderFiles.length > 0}
                    <div class="text-xs font-medium text-gray-800 dark:text-dark-text">{createFolderFiles.length} files ready</div>
                    <div class="mt-0.5 max-w-full truncate text-[11px]">{createFolderFiles[0]?.path.split('/')[0]}</div>
                  {:else}
                    <div class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Drop the complete folder here</div>
                    <div class="mt-0.5 text-[11px]">SKILL.md + references, scripts and other text files</div>
                  {/if}
                  <button type="button" onclick={() => createFolderInput?.click()} class="mt-2 border border-gray-300 bg-white px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text-secondary dark:hover:bg-dark-border">
                    Choose folder
                  </button>
                  <input bind:this={createFolderInput} class="hidden" type="file" multiple webkitdirectory={true} onchange={selectCreateFolder} />
                </div>
                {#if createFolderFiles.length > 0}
                  <div class="mt-2 flex items-center justify-between gap-3">
                    <span class="text-[11px] {createFolderFiles.some((item) => item.file.name.toLowerCase() === 'skill.md') ? 'text-green-600 dark:text-green-400' : 'text-amber-600 dark:text-amber-400'}">
                      {createFolderFiles.some((item) => item.file.name.toLowerCase() === 'skill.md') ? 'SKILL.md found' : 'SKILL.md is required'}
                    </span>
                    <button type="button" onclick={handleImportFolder} disabled={importingFolder || !createFolderFiles.some((item) => item.file.name.toLowerCase() === 'skill.md')} class="flex items-center gap-1.5 bg-gray-900 px-3 py-1.5 text-xs font-medium text-white hover:bg-gray-800 disabled:opacity-40 dark:bg-accent dark:hover:bg-accent-hover">
                      <Upload size={12} />
                      {importingFolder ? 'Creating...' : 'Create from folder'}
                    </button>
                  </div>
                {/if}
                <div class="mt-3 flex items-center gap-3 text-[11px] text-gray-400 before:h-px before:flex-1 before:bg-gray-200 after:h-px after:flex-1 after:bg-gray-200 dark:text-dark-text-muted dark:before:bg-dark-border dark:after:bg-dark-border">or define it manually</div>
              </div>
            {/if}

            <!-- Name -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-name" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Name</label>
              <input
                id="form-name"
                type="text"
                bind:value={formName}
                placeholder="e.g., web_search, code_review"
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text dark:placeholder:text-dark-text-muted"
              />
            </div>

            <!-- Description -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-description" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Description</label>
              <input
                id="form-description"
                type="text"
                bind:value={formDescription}
                placeholder="What this skill does"
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text dark:placeholder:text-dark-text-muted"
              />
            </div>

            <!-- Category -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-category" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Category</label>
              <input
                id="form-category"
                type="text"
                bind:value={formCategory}
                placeholder="e.g. OpenMontage, Utilities"
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text dark:placeholder:text-dark-text-muted"
              />
            </div>

            <!-- Tags -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-tags" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Tags</label>
              <input
                id="form-tags"
                type="text"
                value={formTags.join(', ')}
                oninput={(e) => { formTags = (e.target as HTMLInputElement).value.split(',').map(t => t.trim()).filter(Boolean); }}
                placeholder="e.g. video, production"
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text dark:placeholder:text-dark-text-muted"
              />
            </div>

            <!-- System Prompt -->
            <div class="grid grid-cols-4 gap-3 items-start">
              <label for="form-system-prompt" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">System Prompt</label>
              <textarea
                id="form-system-prompt"
                bind:value={formSystemPrompt}
                rows={3}
                placeholder="Instructions for the agent when using this skill"
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle resize-y dark:text-dark-text dark:placeholder:text-dark-text-muted"
              ></textarea>
            </div>

            <!-- Execution Context -->
            <div class="grid grid-cols-4 gap-3 items-start">
              <label for="form-context" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Execution</label>
              <div class="col-span-3 space-y-2">
                <select
                  id="form-context"
                  bind:value={formContext}
                  onchange={() => { if (formContext !== 'fork') { formAgent = ''; formBackground = false; } }}
                  class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:text-dark-text"
                >
                  <option value="">Current agent context</option>
                  <option value="fork">Forked subagent context</option>
                </select>
                {#if formContext === 'fork'}
                  <select
                    bind:value={formAgent}
                    class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:text-dark-text"
                  >
                    <option value="">Select subagent...</option>
                    {#each agents as agent}
                      <option value={agent.id}>{agent.name}</option>
                    {/each}
                  </select>
                  <label class="flex items-center gap-2 text-xs text-gray-600 dark:text-dark-text-secondary">
                    <input type="checkbox" bind:checked={formBackground} class="text-gray-900 dark:text-accent focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:border-dark-border-subtle" />
                    Run in background
                  </label>
                  <p class="text-[11px] text-gray-400 dark:text-dark-text-muted">The calling agent must include this target in its Subagents allowlist.</p>
                {/if}
              </div>
            </div>

            <!-- Tools -->
            <div class="grid grid-cols-4 gap-3 items-start">
              <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Tools</span>
              <div class="col-span-3 space-y-3">
                {#each formTools as tool, i}
                  <div class="border border-gray-200 dark:border-dark-border p-3 bg-gray-50/50 dark:bg-dark-base/30 space-y-2">
                    <div class="flex items-center justify-between">
                      <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted">Tool {i + 1}</span>
                      <button
                        type="button"
                        onclick={() => removeTool(i)}
                        class="p-1 hover:bg-red-50 dark:hover:bg-red-900/20 text-red-500 hover:text-red-700 dark:text-red-400 dark:hover:text-red-300 "
                        title="Remove tool"
                      >
                        <X size={12} />
                      </button>
                    </div>
                    <div class="space-y-2">
                      <input
                        type="text"
                        bind:value={tool.name}
                        placeholder="Tool name (e.g., search_web)"
                        class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2.5 py-1 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text dark:placeholder:text-dark-text-muted"
                      />
                      <input
                        type="text"
                        bind:value={tool.description}
                        placeholder="Tool description"
                        class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2.5 py-1 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text dark:placeholder:text-dark-text-muted"
                      />
                      <div>
                        <div class="text-xs text-gray-500 dark:text-dark-text-muted mb-0.5">Input Schema (JSON)</div>
                        <textarea
                          value={JSON.stringify(tool.inputSchema || {}, null, 2)}
                          oninput={(e) => updateToolSchema(i, (e.target as HTMLTextAreaElement).value)}
                          rows={3}
                          class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2.5 py-1 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle resize-y dark:text-dark-text dark:placeholder:text-dark-text-muted"
                          placeholder={'{\n  "type": "object",\n  "properties": { ... }\n}'}
                        ></textarea>
                      </div>
                      <div>
                        <div class="flex items-center gap-2 mb-0.5">
                          <span class="text-xs text-gray-500 dark:text-dark-text-muted">Handler</span>
                          <select
                            bind:value={tool.handler_type}
                            class="text-xs border border-gray-300 dark:border-dark-border-subtle px-1.5 py-0.5 bg-white dark:bg-dark-elevated focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/30 dark:text-dark-text"
                          >
                            <option value="js">JavaScript</option>
                            <option value="bash">Bash</option>
                          </select>
                        </div>
                        <textarea
                          bind:value={tool.handler}
                          rows={3}
                          class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2.5 py-1 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle resize-y dark:text-dark-text dark:placeholder:text-dark-text-muted"
                          placeholder={tool.handler_type === 'bash'
                            ? '#!/bin/bash\ncurl -s "$ARG_URL" | jq .'
                            : '// Access tool arguments as "args"\nvar result = httpGet(args.url);\nreturn result.body;'}
                        ></textarea>
                      </div>
                    </div>
                  </div>
                {/each}
                <button
                  type="button"
                  onclick={addTool}
                  class="flex items-center gap-1.5 text-sm text-gray-500 hover:text-gray-900 dark:text-dark-text-muted dark:hover:text-dark-text "
                >
                  <Plus size={12} />
                  Add tool
                </button>
              </div>
            </div>

            <!-- Actions -->
            <div class="flex justify-end gap-2 pt-3 border-t border-gray-100 dark:border-dark-border">
              <button
                type="button"
                onclick={resetForm}
                class="px-3 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary "
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={saving}
                class="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-50"
              >
                <Save size={14} />
                {#if saving}
                  Saving...
                {:else}
                  {editingId ? 'Update' : 'Create'}
                {/if}
              </button>
            </div>
          </form>
        </div>
      {/if}

      <!-- Skill list -->
      {#if loading || skills.length > 0 || !showForm}
        <DataTable
          items={pagedSkills}
          {loading}
          total={filteredSkills.length}
          bind:limit
          bind:offset
          onchange={load}
          onsearch={handleSearch}
          searchPlaceholder="Search by name..."
          emptyIcon={Wand2}
          emptyTitle="No skills configured"
          emptyDescription="Skills define reusable tool sets for agent workflows"
        >
          {#snippet header()}
            <SortableHeader field="name" label="Name" {sorts} onsort={handleSort} />
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Description</th>
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Tools</th>
            <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-32"></th>
          {/snippet}

          {#snippet row(skill)}
            <tr class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 ">
              <td class="px-4 py-2.5 font-mono font-medium text-gray-900 dark:text-dark-text">{skill.name}</td>
              <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted max-w-64 truncate" title={skill.description}>
                {skill.description || '-'}
              </td>
              <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">
                {#if skill.tools && skill.tools.length > 0}
                  <span class="px-2 py-0.5 bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary font-mono">
                    {skill.tools.length} tool{skill.tools.length !== 1 ? 's' : ''}
                  </span>
                  <span class="ml-1.5 text-gray-400 dark:text-dark-text-muted">
                    {skill.tools.map((t) => t.name).join(', ')}
                  </span>
                {:else}
                  <span class="text-gray-400 dark:text-dark-text-muted">none</span>
                {/if}
              </td>
              <td class="px-4 py-2.5 text-right">
                <div class="flex justify-end gap-1">
                  {#if skill.owner_user_id === storeAuth.identity?.subject && mayPublish}
                    <button
                      onclick={() => handlePublish(skill)}
                      class="p-1.5 hover:bg-blue-50 dark:hover:bg-accent-muted text-blue-500 hover:text-blue-700 dark:text-accent-text"
                      title="Copy to Workspace Skills"
                    >
                      <Share2 size={14} />
                    </button>
                  {/if}
                  <button
                    onclick={() => folderSkill = skill}
                    class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text "
                    title="Open skill folder"
                  >
                    <FolderOpen size={14} />
                  </button>
                  <button
                    onclick={() => handleExport(skill)}
                    class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text "
                    title="Export skill as JSON"
                  >
                    <Download size={14} />
                  </button>
                  <button
                    onclick={() => copySkill(skill)}
                    class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text "
                    title="Copy skill"
                  >
                    <Copy size={14} />
                  </button>
                  {#if skill.owner_user_id || mayPublish}
                  <button
                    onclick={() => openEditWithAI(skill)}
                    class="p-1.5 hover:bg-blue-50 dark:hover:bg-accent-muted text-blue-500 hover:text-blue-700 dark:text-accent-text dark:hover:text-accent-text "
                    title="Edit with AI"
                  >
                    <Bot size={14} />
                  </button>
                  <button
                    onclick={() => openEdit(skill)}
                    class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text "
                    title="Edit"
                  >
                    <Pencil size={14} />
                  </button>
                  {#if deleteConfirm === skill.id}
                    <button
                      onclick={() => handleDelete(skill.id)}
                      class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 "
                    >
                      Confirm
                    </button>
                    <button
                      onclick={() => (deleteConfirm = null)}
                      class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary "
                    >
                      Cancel
                    </button>
                  {:else}
                    <button
                      onclick={() => (deleteConfirm = skill.id)}
                      class="p-1.5 hover:bg-red-50 dark:hover:bg-red-900/20 text-red-500 hover:text-red-700 dark:text-red-400 dark:hover:text-red-300 "
                      title="Delete"
                    >
                      <Trash2 size={14} />
                    </button>
                  {/if}
                  {/if}
                </div>
              </td>
            </tr>
          {/snippet}
        </DataTable>
      {/if}
      {/if}

      <!-- Skill Store Tab -->
      {#if activeTab === 'store'}
        <!-- Category Filters -->
        {#if templates.length > 0}
          {@const categories = [...new Set(templates.map((t) => t.category))]}
          <div class="flex items-center gap-2 mb-4 flex-wrap">
            <span class="text-xs text-gray-500 dark:text-dark-text-muted">Filter:</span>
            {#each categories as cat}
              <button
                onclick={() => selectCategory(cat)}
                class="px-2.5 py-1 text-xs font-medium {selectedCategory === cat ? 'bg-gray-900 dark:bg-accent text-white' : 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary hover:bg-gray-200 dark:hover:bg-dark-border'}"
              >
                {cat}
              </button>
            {/each}
            {#if selectedCategory}
              <button
                onclick={() => selectCategory('')}
                class="px-2 py-1 text-xs text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary "
              >
                Clear
              </button>
            {/if}
          </div>
        {/if}

        {#if storeLoading}
          <div class="flex items-center justify-center py-12 text-gray-400 dark:text-dark-text-muted">
            <RefreshCw size={16} class="animate-spin mr-2" />
            Loading templates...
          </div>
        {:else if templates.length === 0}
          <div class="flex flex-col items-center justify-center py-12 text-gray-400 dark:text-dark-text-muted">
            <Store size={24} class="mb-2" />
            <p class="text-sm">No templates available</p>
          </div>
        {:else}
          <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {#each templates as tmpl}
              <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-4 flex flex-col">
                <div class="flex items-start justify-between mb-2">
                  <div>
                    <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text">{tmpl.name}</h3>
                    <span class="inline-block mt-1 px-2 py-0.5 text-[10px] font-medium bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted">
                      {tmpl.category}
                    </span>
                  </div>
                  {#if installedSlugs.has(tmpl.slug)}
                    <span class="flex items-center gap-1 px-2 py-1 text-xs font-medium text-green-600 dark:text-green-400 bg-green-50 dark:bg-green-900/20">
                      <Check size={12} />
                      Installed
                    </span>
                  {/if}
                </div>
                <p class="text-xs text-gray-500 dark:text-dark-text-muted mb-3 flex-1">{tmpl.description}</p>

                <!-- Tags -->
                {#if tmpl.tags && tmpl.tags.length > 0}
                  <div class="flex flex-wrap gap-1 mb-3">
                    {#each tmpl.tags as tag}
                      <span class="px-1.5 py-0.5 text-[10px] bg-gray-50 dark:bg-dark-base text-gray-400 dark:text-dark-text-muted">{tag}</span>
                    {/each}
                  </div>
                {/if}

                <!-- Required vars -->
                {#if tmpl.required_variables && tmpl.required_variables.length > 0 && !tmpl.oauth}
                  <div class="text-[10px] text-gray-400 dark:text-dark-text-muted mb-3">
                    Requires: {tmpl.required_variables.map((v) => v.key).join(', ')}
                  </div>
                {/if}

                <!-- Tools preview -->
                <div class="text-xs text-gray-500 dark:text-dark-text-muted mb-3">
                  <span class="font-mono">{tmpl.skill.tools.length} tool{tmpl.skill.tools.length !== 1 ? 's' : ''}</span>:
                  {tmpl.skill.tools.map((t) => t.name).join(', ')}
                </div>

                <!-- OAuth setup flow -->
                {#if tmpl.oauth && !installedSlugs.has(tmpl.slug)}
                  {#if oauthSetupSlug === tmpl.slug}
                    <div class="border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50 p-3 mb-3 space-y-2">
                      <!-- Step 1: Required variables -->
                      {#each tmpl.required_variables as rv}
                        <label class="block">
                          <span class="block text-[10px] text-gray-500 dark:text-dark-text-muted mb-0.5">{rv.key}</span>
                          {#if existingVars.has(rv.key)}
                            <div class="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
                              <Check size={10} />
                              Already set
                            </div>
                          {:else}
                            <input
                              type={rv.secret ? 'password' : 'text'}
                              placeholder={rv.description}
                              value={oauthVarInputs[rv.key] || ''}
                              oninput={(e) => { oauthVarInputs = { ...oauthVarInputs, [rv.key]: (e.target as HTMLInputElement).value }; }}
                              class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2 py-1 text-xs dark:text-dark-text dark:placeholder:text-dark-text-muted"
                            />
                          {/if}
                        </label>
                      {/each}

                      {#if !oauthAllVarsReady(tmpl)}
                        <button
                          onclick={() => saveOAuthVars(tmpl)}
                          class="w-full flex items-center justify-center gap-1 px-2 py-1.5 text-xs font-medium bg-gray-800 dark:bg-dark-elevated text-white hover:bg-gray-700 dark:hover:bg-dark-border "
                        >
                          <Save size={10} />
                          Save Credentials
                        </button>
                      {/if}

                      <!-- Step 2: OAuth connect -->
                      {#if oauthAllVarsReady(tmpl)}
                        {#if oauthRefreshTokenReady(tmpl.oauth)}
                          <div class="flex items-center gap-1 text-xs text-green-600 dark:text-green-400 py-1">
                            <Check size={12} />
                            Account connected
                          </div>
                        {:else}
                          <button
                            onclick={() => startOAuthConnect(tmpl.oauth!)}
                            class="w-full flex items-center justify-center gap-1.5 px-2 py-1.5 text-xs font-medium bg-blue-600 text-white hover:bg-blue-700 "
                          >
                            <ExternalLink size={10} />
                            Connect {tmpl.oauth.charAt(0).toUpperCase() + tmpl.oauth.slice(1)} Account (Optional)
                          </button>
                          <p class="text-[10px] text-gray-400 dark:text-dark-text-muted leading-tight">Users can connect their own accounts via /login in chat</p>
                        {/if}
                      {/if}

                      <!-- Step 3: Install -->
                      {#if oauthAllVarsReady(tmpl)}
                        <button
                          onclick={() => handleInstallTemplate(tmpl.slug)}
                          class="w-full flex items-center justify-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover "
                        >
                          <Download size={12} />
                          Install
                        </button>
                      {/if}
                    </div>

                    <button
                      onclick={() => { oauthSetupSlug = ''; }}
                      class="w-full text-xs text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary py-1"
                    >
                      Cancel
                    </button>
                  {:else}
                    <button
                      onclick={() => startOAuthSetup(tmpl.slug)}
                      class="w-full flex items-center justify-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover "
                    >
                      <Settings size={12} />
                      Setup & Install
                    </button>
                  {/if}

                <!-- Regular install button (no OAuth) -->
                {:else if !installedSlugs.has(tmpl.slug)}
                  <button
                    onclick={() => handleInstallTemplate(tmpl.slug)}
                    class="w-full flex items-center justify-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover "
                  >
                    <Download size={12} />
                    Install
                  </button>
                {/if}
              </div>
            {/each}
          </div>
        {/if}
      {/if}

      <!-- Community Tab -->
      {#if activeTab === 'community'}
        <!-- Source Filter Chips + Manage Button -->
        <div class="flex items-center gap-2 mb-4 flex-wrap">
          <span class="text-xs text-gray-500 dark:text-dark-text-muted">Sources:</span>
          <button
            onclick={() => handleSourceFilter('')}
            class="px-2.5 py-1 text-xs font-medium {communitySourceFilter === '' ? 'bg-gray-900 dark:bg-accent text-white' : 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary hover:bg-gray-200 dark:hover:bg-dark-border'}"
          >
            All
          </button>
          {#each communitySources.filter((s) => s.enabled) as src}
            <button
              onclick={() => handleSourceFilter(src.id)}
              class="px-2.5 py-1 text-xs font-medium {communitySourceFilter === src.id ? 'bg-gray-900 dark:bg-accent text-white' : 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary hover:bg-gray-200 dark:hover:bg-dark-border'}"
            >
              {src.name}
            </button>
          {/each}
          <button
            onclick={() => { showManageSources = !showManageSources; }}
            class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary ml-auto"
            title="Manage sources"
          >
            <Settings size={14} />
          </button>
        </div>

        <!-- Search Bar -->
        <div class="flex items-center gap-2 mb-4">
          <div class="relative flex-1">
            <Search size={14} class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 dark:text-dark-text-muted" />
            <input
              type="text"
              value={communitySearchQuery}
              oninput={(e) => handleCommunitySearch((e.target as HTMLInputElement).value)}
              placeholder="Search community skills..."
              class="w-full pl-9 pr-3 py-2 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:text-dark-text dark:placeholder:text-dark-text-muted"
            />
          </div>
          <button
            onclick={loadCommunitySkills}
            class="p-2 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary "
            title="Refresh"
          >
            <RefreshCw size={14} />
          </button>
        </div>

        <!-- Manage Sources Panel -->
        {#if showManageSources}
          <div class="mb-4 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-4">
            <div class="flex items-center justify-between mb-3">
              <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text">Marketplace Sources</h3>
              <button
                onclick={() => { showAddSource = !showAddSource; }}
                class="flex items-center gap-1 px-2.5 py-1 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover "
              >
                <Plus size={12} />
                Add Source
              </button>
            </div>

            {#if showAddSource}
              <div class="mb-3 p-3 bg-gray-50 dark:bg-dark-base/50 border border-gray-200 dark:border-dark-border space-y-2">
                <input type="text" bind:value={newSourceName} placeholder="Source name" class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm dark:text-dark-text dark:placeholder:text-dark-text-muted" />
                <select bind:value={newSourceType} class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm dark:text-dark-text">
                  <option value="generic">Generic</option>
                </select>
                <input type="text" bind:value={newSourceSearchURL} placeholder="Search API URL" class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm dark:text-dark-text dark:placeholder:text-dark-text-muted" />
                <input type="text" bind:value={newSourceTopURL} placeholder="Top/Trending API URL (optional)" class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm dark:text-dark-text dark:placeholder:text-dark-text-muted" />
                <div class="flex justify-end gap-2">
                  <button onclick={() => { showAddSource = false; }} class="px-3 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated ">Cancel</button>
                  <button onclick={handleAddSource} class="px-3 py-1.5 text-xs bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover ">Add</button>
                </div>
              </div>
            {/if}

            <div class="space-y-2">
              {#each communitySources as src}
                <div class="flex items-center justify-between py-2 px-3 bg-gray-50 dark:bg-dark-base/30 border border-gray-100 dark:border-dark-border">
                  <div class="flex items-center gap-3">
                    <button
                      onclick={() => toggleSourceEnabled(src)}
                      aria-label="Toggle {src.name}"
                      class="w-8 h-5 rounded-full {src.enabled ? 'bg-green-500' : 'bg-gray-300 dark:bg-dark-border'} relative"
                    >
                      <span class="absolute top-0.5 {src.enabled ? 'right-0.5' : 'left-0.5'} w-4 h-4 rounded-full bg-white shadow "></span>
                    </button>
                    <div>
                      <span class="text-sm font-medium text-gray-900 dark:text-dark-text">{src.name}</span>
                      <span class="ml-2 px-1.5 py-0.5 text-[10px] bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted">{src.type}</span>
                    </div>
                  </div>
                  {#if !src.id.startsWith('default-')}
                    <button
                      onclick={() => handleDeleteSource(src.id)}
                      class="p-1 hover:bg-red-50 dark:hover:bg-red-900/20 text-red-500 hover:text-red-700 dark:text-red-400 dark:hover:text-red-300 "
                      title="Delete source"
                    >
                      <Trash2 size={14} />
                    </button>
                  {/if}
                </div>
              {/each}
            </div>
          </div>
        {/if}

        <!-- Results -->
        {#if communityLoading}
          <div class="flex items-center justify-center py-12 text-gray-400 dark:text-dark-text-muted">
            <RefreshCw size={16} class="animate-spin mr-2" />
            Searching marketplaces...
          </div>
        {:else if communitySkills.length === 0}
          <div class="flex flex-col items-center justify-center py-12 text-gray-400 dark:text-dark-text-muted">
            <Globe size={24} class="mb-2" />
            <p class="text-sm">{communitySearchQuery ? 'No skills found' : 'Browse community skills'}</p>
            <p class="text-xs mt-1">Search or browse trending skills from configured marketplaces</p>
          </div>
        {:else}
          <div class="text-xs text-gray-400 dark:text-dark-text-muted mb-3">
            {communitySkills.length} skill{communitySkills.length !== 1 ? 's' : ''} found
          </div>
          <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {#each communitySkills as skill}
              <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-4 flex flex-col">
                <div class="flex items-start justify-between mb-2">
                  <div class="flex-1 min-w-0">
                    <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text truncate">{skill.name}</h3>
                    {#if skill.author}
                      <span class="text-[10px] text-gray-400 dark:text-dark-text-muted">by {skill.author}</span>
                    {/if}
                  </div>
                  <span class="ml-2 shrink-0 px-1.5 py-0.5 text-[10px] font-medium bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted">
                    {skill.source}
                  </span>
                </div>
                <p class="text-xs text-gray-500 dark:text-dark-text-muted mb-3 flex-1 line-clamp-3">{skill.description || 'No description'}</p>

                {#if skill.tags && skill.tags.length > 0}
                  <div class="flex flex-wrap gap-1 mb-3">
                    {#each skill.tags.slice(0, 5) as tag}
                      <span class="px-1.5 py-0.5 text-[10px] bg-gray-50 dark:bg-dark-base text-gray-400 dark:text-dark-text-muted">{tag}</span>
                    {/each}
                  </div>
                {/if}

                <div class="flex items-center justify-between text-[10px] text-gray-400 dark:text-dark-text-muted mb-3">
                  {#if skill.downloads > 0}
                    <span>{skill.downloads.toLocaleString()} downloads</span>
                  {:else}
                    <span></span>
                  {/if}
                  {#if skill.license}
                    <span>{skill.license}</span>
                  {/if}
                </div>

                <button
                  onclick={() => handlePreview(skill)}
                  class="w-full flex items-center justify-center gap-1.5 px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated "
                >
                  <Eye size={12} />
                  Preview
                </button>
              </div>
            {/each}
          </div>
        {/if}

        <!-- Preview Modal -->
        {#if previewSkill}
          <!-- svelte-ignore a11y_no_static_element_interactions -->
          <!-- svelte-ignore a11y_click_events_have_key_events -->
          <div class="fixed inset-0 bg-black/50 z-50 flex items-center justify-center p-4" onclick={() => { previewSkill = null; previewData = null; }}>
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <div class="bg-white dark:bg-dark-surface border border-gray-200 dark:border-dark-border w-full max-w-2xl max-h-[80vh] flex flex-col" onclick={(e) => e.stopPropagation()}>
              <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50">
                <div>
                  <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text">{previewSkill.name}</h3>
                  <span class="text-[10px] text-gray-400 dark:text-dark-text-muted">from {previewSkill.source}</span>
                </div>
                <button onclick={() => { previewSkill = null; previewData = null; }} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 ">
                  <X size={14} />
                </button>
              </div>

              <div class="flex-1 overflow-y-auto p-4">
                {#if previewLoading}
                  <div class="flex items-center justify-center py-8 text-gray-400 dark:text-dark-text-muted">
                    <RefreshCw size={16} class="animate-spin mr-2" />
                    Loading preview...
                  </div>
                {:else if previewData}
                  <div class="space-y-4">
                    <div>
                      <span class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted">Name</span>
                      <p class="text-sm text-gray-900 dark:text-dark-text font-mono">{previewData.name || '-'}</p>
                    </div>
                    {#if previewData.description}
                      <div>
                        <span class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted">Description</span>
                        <p class="text-sm text-gray-700 dark:text-dark-text-secondary">{previewData.description}</p>
                      </div>
                    {/if}
                    {#if previewData.system_prompt}
                      <div>
                        <span class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted">System Prompt / Instructions</span>
                        <pre class="mt-1 p-3 bg-gray-50 dark:bg-dark-base/50 border border-gray-200 dark:border-dark-border text-xs text-gray-700 dark:text-dark-text-secondary whitespace-pre-wrap max-h-64 overflow-y-auto">{previewData.system_prompt}</pre>
                      </div>
                    {/if}
                    {#if previewData.tools && previewData.tools.length > 0}
                      <div>
                        <span class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted">Tools ({previewData.tools.length})</span>
                        <div class="mt-1 space-y-1">
                          {#each previewData.tools as tool}
                            <div class="px-2 py-1 bg-gray-50 dark:bg-dark-base/50 border border-gray-100 dark:border-dark-border">
                              <span class="text-xs font-mono text-gray-900 dark:text-dark-text">{tool.name}</span>
                              {#if tool.description}
                                <span class="text-[10px] text-gray-400 dark:text-dark-text-muted ml-2">{tool.description}</span>
                              {/if}
                            </div>
                          {/each}
                        </div>
                      </div>
                    {/if}
                  </div>
                {:else}
                  <p class="text-sm text-gray-400 dark:text-dark-text-muted">Failed to load preview</p>
                {/if}
              </div>

              <div class="flex justify-end gap-2 px-4 py-3 border-t border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50">
                <button
                  onclick={() => { previewSkill = null; previewData = null; }}
                  class="px-3 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated "
                >
                  Cancel
                </button>
                {#if previewData}
                  <button
                    onclick={() => handleCommunityImport(previewSkill.url)}
                    class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover "
                  >
                    <Download size={12} />
                    Import Skill
                  </button>
                {/if}
              </div>
            </div>
          </div>
        {/if}
      {/if}
    </div>
  </div>

  <!-- AI Panel (slides in from right) -->
  {#if showAIPanel}
    <SkillBuilderPanel
      onclose={() => { showAIPanel = false; }}
      bind:formName
      bind:formDescription
      bind:formSystemPrompt
      bind:formTools
      bind:editingId
      bind:showForm
      onSaved={load}
    />
  {/if}
</div>

{#if folderSkill}
  <SkillFilesDialog skill={folderSkill} onclose={() => folderSkill = null} onchanged={load} />
{/if}
