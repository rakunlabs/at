<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listSkillServers,
    createSkillServer,
    updateSkillServer,
    deleteSkillServer,
    type SkillServer,
    type SkillServerMode,
  } from '@/lib/api/skill-servers';
  import { listSkills, type Skill } from '@/lib/api/skills';
  import {
    Server,
    Package,
    Wrench,
    Boxes,
    Plus,
    Pencil,
    Trash2,
    X,
    Save,
    RefreshCw,
    Copy,
    Check,
    Search,
    Download,
  } from 'lucide-svelte';

  storeNavbar.title = 'Skill Servers';

  const modes: Array<{ value: SkillServerMode; label: string; description: string }> = [
    {
      value: 'package',
      label: 'Package',
      description: 'Expose export/list tools so other agents can discover and install selected skills.',
    },
    {
      value: 'tools',
      label: 'Tools',
      description: 'Expose each selected skill tool directly through MCP.',
    },
    {
      value: 'both',
      label: 'Both',
      description: 'Expose package export tools and direct skill tools together.',
    },
  ];

  let servers = $state<SkillServer[]>([]);
  let skills = $state<Skill[]>([]);
  let loading = $state(true);
  let skillsLoading = $state(true);
  let saving = $state(false);
  let showForm = $state(false);
  let editingId = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);
  let copiedName = $state<string | null>(null);
  let copiedMarketplace = $state<string | null>(null);

  let searchQuery = $state('');
  let skillQuery = $state('');

  let formName = $state('');
  let formDescription = $state('');
  let formPublic = $state(false);
  let formMode = $state<SkillServerMode>('package');
  let formSkills = $state<string[]>([]);

  let filteredServers = $derived(filterServers(servers, searchQuery));
  let filteredSkills = $derived(filterSkills(skills, skillQuery));
  let publicServers = $derived(servers.filter((server) => server.public));
  let missingSkillRefs = $derived(formSkills.filter((ref) => !findSkill(ref)));

  async function loadServers() {
    loading = true;
    try {
      const res = await listSkillServers({ _limit: 500, _sort: 'name' });
      servers = res.data || [];
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load skill servers', 'alert');
    } finally {
      loading = false;
    }
  }

  async function loadSkills() {
    skillsLoading = true;
    try {
      const res = await listSkills({ _limit: 500, _sort: 'name' });
      skills = res.data || [];
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load skills', 'alert');
    } finally {
      skillsLoading = false;
    }
  }

  loadServers();
  loadSkills();

  function filterServers(items: SkillServer[], query: string): SkillServer[] {
    const q = query.trim().toLowerCase();
    if (!q) return items;
    return items.filter((server) => {
      return (
        server.name.toLowerCase().includes(q) ||
        (server.description || '').toLowerCase().includes(q) ||
        (server.skills || []).some((skill) => skill.toLowerCase().includes(q))
      );
    });
  }

  function filterSkills(items: Skill[], query: string): Skill[] {
    const q = query.trim().toLowerCase();
    if (!q) return items;
    return items.filter((skill) => {
      return (
        skill.name.toLowerCase().includes(q) ||
        (skill.description || '').toLowerCase().includes(q) ||
        (skill.category || '').toLowerCase().includes(q)
      );
    });
  }

  function resetForm() {
    formName = '';
    formDescription = '';
    formPublic = false;
    formMode = 'package';
    formSkills = [];
    skillQuery = '';
    editingId = null;
    showForm = false;
  }

  function openCreate() {
    resetForm();
    showForm = true;
  }

  function openEdit(server: SkillServer) {
    resetForm();
    editingId = server.id;
    formName = server.name;
    formDescription = server.description || '';
    formPublic = Boolean(server.public);
    formMode = server.mode || 'package';
    formSkills = [...(server.skills || [])];
    showForm = true;
  }

  async function handleSubmit() {
    const name = formName.trim();
    if (!name) {
      addToast('Skill server name is required', 'warn');
      return;
    }
    if (name.includes('/')) {
      addToast('Skill server name cannot contain / because it is used in the gateway URL', 'warn');
      return;
    }

    saving = true;
    try {
      const payload: Partial<SkillServer> = {
        name,
        description: formDescription.trim(),
        public: formPublic,
        mode: formMode,
        skills: formSkills,
      };

      if (editingId) {
        await updateSkillServer(editingId, payload);
        addToast(`Skill server "${name}" updated`);
      } else {
        await createSkillServer(payload);
        addToast(`Skill server "${name}" created`);
      }
      resetForm();
      await loadServers();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save skill server', 'alert');
    } finally {
      saving = false;
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteSkillServer(id);
      addToast('Skill server deleted');
      deleteConfirm = null;
      await loadServers();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete skill server', 'alert');
    }
  }

  function skillRef(skill: Skill): string {
    return skill.name || skill.id;
  }

  function findSkill(ref: string): Skill | undefined {
    return skills.find((skill) => skill.id === ref || skill.name === ref);
  }

  function isSkillSelected(skill: Skill): boolean {
    return formSkills.includes(skill.id) || formSkills.includes(skill.name);
  }

  function toggleSkill(skill: Skill) {
    const refs = [skill.id, skill.name].filter(Boolean);
    if (refs.some((ref) => formSkills.includes(ref))) {
      formSkills = formSkills.filter((ref) => !refs.includes(ref));
      return;
    }
    formSkills = [...formSkills, skillRef(skill)];
  }

  function removeSkillRef(ref: string) {
    formSkills = formSkills.filter((item) => item !== ref);
  }

  function skillLabel(ref: string): string {
    return findSkill(ref)?.name || ref;
  }

  function modeLabel(mode: string): string {
    return modes.find((item) => item.value === mode)?.label || mode || 'Package';
  }

  function modeDescription(mode: string): string {
    return modes.find((item) => item.value === mode)?.description || modes[0].description;
  }

  function endpointFor(name: string): string {
    return `${window.location.origin}/gateway/v1/skill-servers/${encodeURIComponent(name)}/mcp`;
  }

  function marketplaceJSONURL(): string {
    return `${window.location.origin}/gateway/v1/claude-code/marketplace.json`;
  }

  function marketplaceZipURL(): string {
    return `${window.location.origin}/gateway/v1/claude-code/marketplace.zip`;
  }

  function pluginZipFor(name: string): string {
    return `${window.location.origin}/gateway/v1/claude-code/plugins/${encodeURIComponent(name)}/plugin.zip`;
  }

  async function copyEndpoint(server: SkillServer) {
    try {
      await navigator.clipboard.writeText(endpointFor(server.name));
      copiedName = server.name;
      addToast(`Copied endpoint for "${server.name}"`);
      setTimeout(() => {
        if (copiedName === server.name) copiedName = null;
      }, 2000);
    } catch {
      addToast('Failed to copy endpoint', 'alert');
    }
  }

  async function copyMarketplace(kind: 'json' | 'zip') {
    const url = kind === 'json' ? marketplaceJSONURL() : marketplaceZipURL();
    try {
      await navigator.clipboard.writeText(url);
      copiedMarketplace = kind;
      addToast(`Copied Claude marketplace ${kind.toUpperCase()} URL`);
      setTimeout(() => {
        if (copiedMarketplace === kind) copiedMarketplace = null;
      }, 2000);
    } catch {
      addToast('Failed to copy marketplace URL', 'alert');
    }
  }
</script>

<svelte:head>
  <title>AT | Skill Servers</title>
</svelte:head>

<div class="flex h-full">
  <div class="flex-1 overflow-y-auto">
    <div class="p-6 max-w-6xl mx-auto space-y-6">
      <div class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <div class="flex items-center gap-2">
            <Server size={18} class="text-gray-500 dark:text-dark-text-muted" />
            <h1 class="text-lg font-semibold text-gray-900 dark:text-dark-text">Skill Servers</h1>
          </div>
          <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1 max-w-2xl">
            Publish a curated set of skills through an MCP-compatible gateway endpoint. Endpoints require Bearer token auth unless Public mode is enabled.
          </p>
        </div>
        <div class="flex items-center gap-2">
          <button
            onclick={() => { loadServers(); loadSkills(); }}
            class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
            title="Refresh"
          >
            <RefreshCw size={14} />
          </button>
          <button
            onclick={openCreate}
            class="flex items-center gap-1.5 px-3 py-1.5 text-xs whitespace-nowrap font-medium bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors"
          >
            <Plus size={12} />
            New Skill Server
          </button>
        </div>
      </div>

      {#if showForm}
        <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface overflow-hidden">
          <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
            <span class="text-sm font-medium text-gray-900 dark:text-dark-text">
              {editingId ? `Edit: ${formName}` : 'New Skill Server'}
            </span>
            <button onclick={resetForm} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors">
              <X size={14} />
            </button>
          </div>

          <form novalidate onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="p-4 space-y-5">
            <div class="grid grid-cols-1 gap-4 md:grid-cols-4 md:items-center">
              <label for="skill-server-name" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Name</label>
              <input
                id="skill-server-name"
                type="text"
                bind:value={formName}
                placeholder="e.g., writing-tools"
                class="md:col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
              />
            </div>

            <div class="grid grid-cols-1 gap-4 md:grid-cols-4 md:items-center">
              <label for="skill-server-description" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Description</label>
              <input
                id="skill-server-description"
                type="text"
                bind:value={formDescription}
                placeholder="What this server publishes"
                class="md:col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
              />
            </div>

            <div class="grid grid-cols-1 gap-4 md:grid-cols-4 md:items-start">
              <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1">Access</span>
              <label class="md:col-span-3 flex items-start gap-2 cursor-pointer border border-gray-200 dark:border-dark-border bg-gray-50/50 dark:bg-dark-base/30 p-3">
                <input
                  type="checkbox"
                  bind:checked={formPublic}
                  class="mt-0.5 w-3.5 h-3.5 dark:bg-dark-elevated dark:border-dark-border-subtle dark:accent-accent"
                />
                <span class="text-xs text-gray-600 dark:text-dark-text-secondary leading-relaxed">
                  <span class="font-medium text-gray-800 dark:text-dark-text">Public endpoint</span>
                  <span class="block text-gray-400 dark:text-dark-text-muted mt-0.5">Allow unauthenticated agents to discover, export, and call this skill server. Only publish skills that are safe for public use.</span>
                </span>
              </label>
            </div>

            <div class="grid grid-cols-1 gap-4 md:grid-cols-4 md:items-start">
              <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1">Mode</span>
              <div class="md:col-span-3 grid gap-2 sm:grid-cols-3">
                {#each modes as mode}
                  <button
                    type="button"
                    onclick={() => (formMode = mode.value)}
                    class={[
                      'text-left border p-3 transition-colors',
                      formMode === mode.value
                        ? 'border-gray-900 dark:border-accent bg-gray-900 text-white dark:bg-accent'
                        : 'border-gray-200 dark:border-dark-border text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated',
                    ]}
                  >
                    <div class="flex items-center gap-2 text-xs font-semibold uppercase tracking-wide">
                      {#if mode.value === 'package'}
                        <Package size={13} />
                      {:else if mode.value === 'tools'}
                        <Wrench size={13} />
                      {:else}
                        <Boxes size={13} />
                      {/if}
                      {mode.label}
                    </div>
                    <p class={['mt-2 text-xs leading-relaxed', formMode === mode.value ? 'text-white/75' : 'text-gray-400 dark:text-dark-text-muted']}>
                      {mode.description}
                    </p>
                  </button>
                {/each}
              </div>
            </div>

            <div class="grid grid-cols-1 gap-4 md:grid-cols-4 md:items-start">
              <div class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1">
                Skills
                <div class="text-xs font-normal text-gray-400 dark:text-dark-text-muted mt-1">{formSkills.length} selected</div>
              </div>
              <div class="md:col-span-3 space-y-3">
                <div class="relative">
                  <Search size={13} class="absolute left-3 top-2.5 text-gray-400 dark:text-dark-text-muted" />
                  <input
                    type="text"
                    bind:value={skillQuery}
                    placeholder="Filter skills"
                    class="w-full border border-gray-300 dark:border-dark-border-subtle pl-8 pr-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
                  />
                </div>

                {#if formSkills.length > 0}
                  <div class="flex flex-wrap gap-1.5">
                    {#each formSkills as ref}
                      <span class="inline-flex items-center gap-1.5 px-2 py-1 text-xs border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base text-gray-700 dark:text-dark-text-secondary">
                        {skillLabel(ref)}
                        <button type="button" onclick={() => removeSkillRef(ref)} class="text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text-secondary">
                          <X size={11} />
                        </button>
                      </span>
                    {/each}
                  </div>
                {/if}

                {#if missingSkillRefs.length > 0}
                  <div class="text-xs text-amber-600 dark:text-amber-400 bg-amber-50 dark:bg-amber-950/20 border border-amber-200 dark:border-amber-900 px-3 py-2">
                    These saved references do not currently resolve to local skills: {missingSkillRefs.join(', ')}
                  </div>
                {/if}

                <div class="border border-gray-200 dark:border-dark-border max-h-80 overflow-y-auto bg-gray-50/50 dark:bg-dark-base/30">
                  {#if skillsLoading}
                    <div class="p-4 text-xs text-gray-400 dark:text-dark-text-muted">Loading skills...</div>
                  {:else if filteredSkills.length === 0}
                    <div class="p-4 text-xs text-gray-400 dark:text-dark-text-muted">No skills found.</div>
                  {:else}
                    {#each filteredSkills as skill}
                      <label class="flex items-start gap-3 p-3 border-b border-gray-100 dark:border-dark-border last:border-b-0 hover:bg-white dark:hover:bg-dark-elevated cursor-pointer transition-colors">
                        <input
                          type="checkbox"
                          checked={isSkillSelected(skill)}
                          onchange={() => toggleSkill(skill)}
                          class="mt-0.5 w-3.5 h-3.5 dark:bg-dark-elevated dark:border-dark-border-subtle dark:accent-accent"
                        />
                        <div class="flex-1 min-w-0">
                          <div class="flex flex-wrap items-center gap-2">
                            <span class="text-xs font-mono font-medium text-gray-800 dark:text-dark-text">{skill.name}</span>
                            {#if skill.category}
                              <span class="px-1.5 py-0.5 text-[10px] uppercase tracking-wide bg-gray-100 dark:bg-dark-elevated text-gray-400 dark:text-dark-text-muted">{skill.category}</span>
                            {/if}
                            <span class="text-[10px] text-gray-400 dark:text-dark-text-muted">{(skill.tools || []).length} tools</span>
                          </div>
                          {#if skill.description}
                            <p class="text-xs text-gray-500 dark:text-dark-text-muted mt-1 line-clamp-2">{skill.description}</p>
                          {/if}
                        </div>
                      </label>
                    {/each}
                  {/if}
                </div>
              </div>
            </div>

            <div class="flex justify-end gap-2 pt-2 border-t border-gray-200 dark:border-dark-border">
              <button type="button" onclick={resetForm} class="px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors">
                Cancel
              </button>
              <button type="submit" disabled={saving} class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 disabled:opacity-50 dark:bg-accent dark:hover:bg-accent-hover transition-colors">
                <Save size={12} />
                {saving ? 'Saving...' : 'Save Skill Server'}
              </button>
            </div>
          </form>
        </div>
      {/if}

      <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div class="relative w-full sm:w-72">
          <Search size={13} class="absolute left-3 top-2.5 text-gray-400 dark:text-dark-text-muted" />
          <input
            type="text"
            bind:value={searchQuery}
            placeholder="Search servers or skills"
            class="w-full border border-gray-300 dark:border-dark-border-subtle pl-8 pr-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
          />
        </div>
        <div class="text-xs text-gray-400 dark:text-dark-text-muted">
          {filteredServers.length} of {servers.length} skill servers
        </div>
      </div>

      {#if publicServers.length > 0}
        <div class="border border-blue-200 dark:border-blue-900 bg-blue-50/70 dark:bg-blue-950/20 p-4 space-y-3">
          <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
            <div>
              <div class="flex items-center gap-2">
                <Package size={15} class="text-blue-600 dark:text-blue-400" />
                <h2 class="text-sm font-semibold text-blue-950 dark:text-blue-100">Claude Code marketplace export</h2>
                <span class="text-[10px] uppercase tracking-wide text-blue-600 dark:text-blue-300 border border-blue-200 dark:border-blue-800 px-1.5 py-0.5">{publicServers.length} public</span>
              </div>
              <p class="text-xs text-blue-800/80 dark:text-blue-200/80 mt-1 max-w-3xl leading-relaxed">
                Public Skill Servers can be packaged as Claude Code plugins. Download the marketplace ZIP, unzip it, then add that directory locally with
                <code class="font-mono bg-white/70 dark:bg-blue-950 px-1 py-0.5">/plugin marketplace add ./at-claude-marketplace</code>
                or commit it to a Git repo for team distribution. Direct MCP endpoints still work separately for opencode, Claude MCP, Cursor, and other MCP clients.
              </p>
            </div>
            <div class="flex flex-wrap items-center gap-2 shrink-0">
              <button
                onclick={() => copyMarketplace('json')}
                class="inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs border border-blue-200 dark:border-blue-800 text-blue-700 dark:text-blue-200 hover:bg-white/70 dark:hover:bg-blue-950 transition-colors"
              >
                {#if copiedMarketplace === 'json'}<Check size={12} />{:else}<Copy size={12} />{/if}
                Copy JSON
              </button>
              <button
                onclick={() => copyMarketplace('zip')}
                class="inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs border border-blue-200 dark:border-blue-800 text-blue-700 dark:text-blue-200 hover:bg-white/70 dark:hover:bg-blue-950 transition-colors"
              >
                {#if copiedMarketplace === 'zip'}<Check size={12} />{:else}<Copy size={12} />{/if}
                Copy ZIP URL
              </button>
              <a
                href={marketplaceZipURL()}
                class="inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs bg-blue-700 text-white hover:bg-blue-800 dark:bg-blue-500 dark:hover:bg-blue-600 transition-colors"
              >
                <Download size={12} />
                Download ZIP
              </a>
            </div>
          </div>
          <code class="block text-[11px] text-blue-700 dark:text-blue-200 truncate bg-white/70 dark:bg-blue-950/50 border border-blue-100 dark:border-blue-900 px-2 py-1.5">{marketplaceJSONURL()}</code>
        </div>
      {/if}

      {#if loading}
        <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-8 text-center text-sm text-gray-400 dark:text-dark-text-muted">
          Loading skill servers...
        </div>
      {:else if filteredServers.length === 0}
        <div class="border border-dashed border-gray-300 dark:border-dark-border bg-white dark:bg-dark-surface p-8 text-center">
          <Server size={24} class="mx-auto text-gray-300 dark:text-dark-text-muted mb-2" />
          <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">No skill servers found</h2>
          <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">Create one to publish a curated skill bundle through MCP.</p>
          <button onclick={openCreate} class="mt-4 inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors">
            <Plus size={12} />
            New Skill Server
          </button>
        </div>
      {:else}
        <div class="grid gap-3">
          {#each filteredServers as server}
            <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface overflow-hidden">
              <div class="p-4 flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
                <div class="min-w-0 flex-1">
                  <div class="flex flex-wrap items-center gap-2">
                    <Server size={15} class="text-gray-400 dark:text-dark-text-muted" />
                    <h2 class="text-sm font-semibold text-gray-900 dark:text-dark-text font-mono">{server.name}</h2>
                    <span class="inline-flex items-center gap-1 px-2 py-0.5 text-[10px] uppercase tracking-wide bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-secondary">
                      {#if server.mode === 'tools'}
                        <Wrench size={11} />
                      {:else if server.mode === 'both'}
                        <Boxes size={11} />
                      {:else}
                        <Package size={11} />
                      {/if}
                      {modeLabel(server.mode)}
                    </span>
                    {#if server.public}
                      <span class="inline-flex items-center px-2 py-0.5 text-[10px] uppercase tracking-wide bg-green-50 dark:bg-green-950/20 text-green-700 dark:text-green-400 border border-green-200 dark:border-green-900">
                        Public
                      </span>
                    {/if}
                  </div>
                  {#if server.description}
                    <p class="text-xs text-gray-500 dark:text-dark-text-muted mt-1">{server.description}</p>
                  {/if}
                  <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">{modeDescription(server.mode)}</p>
                </div>

                <div class="flex items-center gap-1 shrink-0">
                  <button onclick={() => copyEndpoint(server)} class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors" title="Copy MCP endpoint">
                    {#if copiedName === server.name}
                      <Check size={14} />
                    {:else}
                      <Copy size={14} />
                    {/if}
                  </button>
                  {#if server.public}
                    <a href={pluginZipFor(server.name)} class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors" title="Download Claude plugin ZIP">
                      <Download size={14} />
                    </a>
                  {/if}
                  <button onclick={() => openEdit(server)} class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors" title="Edit">
                    <Pencil size={14} />
                  </button>
                  {#if deleteConfirm === server.id}
                    <button onclick={() => handleDelete(server.id)} class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 transition-colors">Confirm</button>
                    <button onclick={() => (deleteConfirm = null)} class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors">Cancel</button>
                  {:else}
                    <button onclick={() => (deleteConfirm = server.id)} class="p-1.5 hover:bg-red-50 dark:hover:bg-red-950/20 text-gray-400 dark:text-dark-text-muted hover:text-red-600 dark:hover:text-red-400 transition-colors" title="Delete">
                      <Trash2 size={14} />
                    </button>
                  {/if}
                </div>
              </div>

              <div class="px-4 pb-4 space-y-3">
                <button
                  onclick={() => copyEndpoint(server)}
                  class="w-full flex items-center gap-2 text-left border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base px-3 py-2 hover:bg-gray-100 dark:hover:bg-dark-elevated transition-colors"
                  title="Copy MCP endpoint"
                >
                  <Copy size={13} class="text-gray-400 dark:text-dark-text-muted shrink-0" />
                  <code class="text-xs text-gray-600 dark:text-dark-text-secondary truncate">{endpointFor(server.name)}</code>
                </button>

                {#if server.public}
                  <a
                    href={pluginZipFor(server.name)}
                    class="w-full flex items-center gap-2 text-left border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base px-3 py-2 hover:bg-gray-100 dark:hover:bg-dark-elevated transition-colors"
                    title="Download Claude plugin ZIP"
                  >
                    <Download size={13} class="text-gray-400 dark:text-dark-text-muted shrink-0" />
                    <code class="text-xs text-gray-600 dark:text-dark-text-secondary truncate">Claude plugin ZIP: {pluginZipFor(server.name)}</code>
                  </a>
                {/if}

                <div>
                  <div class="text-xs font-medium text-gray-500 dark:text-dark-text-secondary mb-2">Published skills ({(server.skills || []).length})</div>
                  {#if (server.skills || []).length === 0}
                    <div class="text-xs text-gray-400 dark:text-dark-text-muted">No skills selected.</div>
                  {:else}
                    <div class="flex flex-wrap gap-1.5">
                      {#each server.skills as ref}
                        <span class="px-2 py-1 text-xs border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base text-gray-700 dark:text-dark-text-secondary">
                          {skillLabel(ref)}
                        </span>
                      {/each}
                    </div>
                  {/if}
                </div>
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </div>
  </div>
</div>
