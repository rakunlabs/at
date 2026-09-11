<script lang="ts">
  // One sidebar-driven documentation surface: the API reference and the guide
  // library share a single navigation tree, a single search box and a single
  // URL scheme. This page is the shell — data loading, selection state and
  // layout. Rendering lives in lib/components/docs/.
  import { untrack, tick } from 'svelte';
  import { push, querystring } from 'svelte-spa-router';
  import { Menu, X, RefreshCw, BookOpen } from 'lucide-svelte';

  import { storeNavbar, storeInfo } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { getInfo, type InfoProvider } from '@/lib/api/gateway';
  import { listMCPServers, type MCPServer } from '@/lib/api/mcp-servers';
  import {
    listGuides,
    createGuide,
    updateGuide,
    deleteGuide,
    type Guide as UserGuide,
    type GuideInput,
  } from '@/lib/api/guides';

  import {
    DOCS_DEFAULT_GROUP_STATE,
    DOCS_GROUP_STORAGE_KEY,
    docsPath,
    filterEntries,
    parseDocsQuery,
    parseGroupState,
    sameSelection,
    serializeGroupState,
    type DocsGroupState,
    type DocsSelection,
  } from '@/lib/helper/docs-nav';
  import { apiSections, findApiSection } from '@/lib/components/docs/api-sections';
  import { builtinGuides, type DisplayGuide } from '@/lib/components/docs/builtin-guides';
  import DocsSidebar from '@/lib/components/docs/DocsSidebar.svelte';
  import DocsApiPane from '@/lib/components/docs/DocsApiPane.svelte';
  import DocsPaneHeader from '@/lib/components/docs/DocsPaneHeader.svelte';
  import DocsCopyLinkButton from '@/lib/components/docs/DocsCopyLinkButton.svelte';
  import GuideViewer from '@/lib/components/docs/GuideViewer.svelte';
  import GuideEditor from '@/lib/components/docs/GuideEditor.svelte';

  storeNavbar.title = 'Documentation';

  const DEFAULT_SELECTION: DocsSelection = { kind: 'api', id: 'overview' };

  // ─── Gateway info (API reference live data) ───
  let providers = $state<InfoProvider[]>([]);
  let mcpServers = $state<MCPServer[]>([]);
  let infoLoading = $state(true);
  let infoError = $state('');

  // ─── Guides ───
  let userGuides = $state<UserGuide[]>([]);
  let guidesLoading = $state(true);
  let guidesError = $state('');
  let saving = $state(false);
  let deleting = $state(false);

  // ─── Navigation ───
  let query = $state('');
  let selection = $state<DocsSelection>(DEFAULT_SELECTION);
  let groups = $state<DocsGroupState>(readGroupState());
  let navOpen = $state(false);
  let mainEl = $state<HTMLElement | null>(null);
  let navToggleEl = $state<HTMLButtonElement | null>(null);

  // ─── Editor ───
  let mode = $state<'view' | 'new' | 'edit'>('view');
  let editingGuide = $state<DisplayGuide | null>(null);

  // Section-local state lifted here so it survives navigating away and back.
  let codeTab = $state('python');
  let mcpName = $state('');

  function readGroupState(): DocsGroupState {
    try {
      return parseGroupState(localStorage.getItem(DOCS_GROUP_STORAGE_KEY));
    } catch {
      return { ...DOCS_DEFAULT_GROUP_STATE };
    }
  }

  $effect(() => {
    const raw = serializeGroupState(groups);
    try {
      localStorage.setItem(DOCS_GROUP_STORAGE_KEY, raw);
    } catch {
      // Private mode / quota — collapsing still works for this session.
    }
  });

  // ─── Derived data ───
  const allGuides = $derived<DisplayGuide[]>([
    ...builtinGuides,
    ...userGuides.map<DisplayGuide>((g) => ({
      id: g.id,
      title: g.title || '(untitled)',
      description: g.description || '',
      iconName: g.icon || 'BookOpen',
      content: g.content || '',
      builtin: false,
      body: g.content || '',
    })),
  ]);

  const filteredApi = $derived(filterEntries(apiSections, query));
  const filteredGuides = $derived(filterEntries(allGuides, query));

  const activeSection = $derived(
    selection.kind === 'api' ? findApiSection(selection.id) : undefined,
  );
  const activeGuide = $derived(
    selection.kind === 'guides' ? allGuides.find((g) => g.id === selection.id) : undefined,
  );

  const groupLabel = $derived(selection.kind === 'api' ? 'API docs' : 'Guides');
  const currentTitle = $derived(
    mode === 'new'
      ? 'New guide'
      : mode === 'edit'
        ? 'Edit guide'
        : (activeSection?.title ?? activeGuide?.title ?? 'Documentation'),
  );

  const baseUrl = $derived(
    (window.location.origin + window.location.pathname).replace(/\/+$/, ''),
  );

  const allModels = $derived(
    providers.flatMap((p) =>
      p.models && p.models.length > 0
        ? p.models.map((m) => `${p.key}/${m}`)
        : [`${p.key}/${p.default_model}`],
    ),
  );
  const exampleModel = $derived(allModels.length > 0 ? allModels[0] : 'provider/model-name');

  // ─── URL ⇆ selection ───
  function resolveSelection(parsed: DocsSelection | null): DocsSelection {
    if (!parsed) return DEFAULT_SELECTION;
    if (parsed.kind === 'api') {
      return findApiSection(parsed.id) ? parsed : DEFAULT_SELECTION;
    }
    // A guide id that is not loaded yet stays pending; the pane shows a
    // loading or not-found state instead of silently rewriting the URL.
    if (!parsed.id) return { kind: 'guides', id: allGuides[0]?.id ?? '' };
    return parsed;
  }

  $effect(() => {
    const resolved = resolveSelection(parseDocsQuery($querystring || ''));
    if (!sameSelection(resolved, untrack(() => selection))) {
      selection = resolved;
      // Back/forward navigation must abandon a half-written draft rather than
      // render the editor against a different guide.
      mode = 'view';
      editingGuide = null;
    }
  });

  // Expand the group that owns the selection whenever the selection changes —
  // but never fight a deliberate collapse of an already-selected group.
  let lastSelectionKey = '';
  $effect(() => {
    const key = `${selection.kind}:${selection.id}`;
    if (key === lastSelectionKey) return;
    lastSelectionKey = key;
    untrack(() => {
      if (!groups[selection.kind]) groups = { ...groups, [selection.kind]: true };
    });
  });

  // Searching reveals hits in both groups.
  let wasSearching = false;
  $effect(() => {
    const searching = query.trim().length > 0;
    if (searching && !wasSearching) {
      untrack(() => (groups = { api: true, guides: true }));
    }
    wasSearching = searching;
  });

  // Landing on a deep link (or switching entries) starts the reader at the top.
  let lastScrollKey = '';
  $effect(() => {
    const key = `${selection.kind}:${selection.id}:${mode}`;
    if (key === lastScrollKey) return;
    lastScrollKey = key;
    if (mainEl) mainEl.scrollTop = 0;
  });

  // ─── Loading ───
  async function loadInfo() {
    infoLoading = true;
    infoError = '';
    // Both are best-effort: the API reference is static content and must stay
    // readable when the gateway info endpoint is unavailable.
    const [info, mcpRes] = await Promise.allSettled([
      getInfo(),
      listMCPServers({ _limit: 100 }),
    ]);

    if (info.status === 'fulfilled') {
      providers = info.value.providers;
    } else {
      infoError =
        (info.reason as any)?.response?.data?.message ||
        'Failed to load gateway info. Model lists may be incomplete.';
    }

    if (mcpRes.status === 'fulfilled') {
      mcpServers = mcpRes.value.data || [];
      if (mcpServers.length > 0 && !mcpName) {
        const mgmt = mcpServers.find((s) => s.name === 'management');
        mcpName = mgmt?.name || mcpServers[0].name;
      }
    }

    infoLoading = false;
  }

  async function loadGuides() {
    guidesLoading = true;
    guidesError = '';
    try {
      const res = await listGuides();
      userGuides = res.data ?? [];
    } catch (e: any) {
      guidesError = e?.response?.data?.message || 'Failed to load guides.';
      addToast(guidesError, 'alert');
    } finally {
      guidesLoading = false;
    }
  }

  function refreshAll() {
    void loadInfo();
    void loadGuides();
  }

  void loadInfo();
  void loadGuides();

  // ─── Actions ───
  function discardGuard(): boolean {
    if (mode === 'view') return true;
    return confirm('Discard unsaved changes?');
  }

  function select(next: DocsSelection) {
    if (!discardGuard()) return;
    mode = 'view';
    editingGuide = null;
    navOpen = false;
    if (!sameSelection(next, selection)) selection = next;
    push(docsPath(next));
  }

  function startNewGuide() {
    if (!discardGuard()) return;
    editingGuide = null;
    mode = 'new';
    navOpen = false;
    if (!groups.guides) groups = { ...groups, guides: true };
  }

  function startEditGuide(guide: DisplayGuide) {
    if (guide.builtin) return;
    editingGuide = guide;
    mode = 'edit';
  }

  function cancelEdit() {
    mode = 'view';
    editingGuide = null;
  }

  async function saveGuide(input: GuideInput) {
    saving = true;
    try {
      if (mode === 'edit' && editingGuide) {
        const updated = await updateGuide(editingGuide.id, input);
        userGuides = userGuides.map((g) => (g.id === updated.id ? updated : g));
        selection = { kind: 'guides', id: updated.id };
        push(docsPath(selection));
        addToast('Guide updated');
      } else {
        const created = await createGuide(input);
        userGuides = [...userGuides, created];
        selection = { kind: 'guides', id: created.id };
        push(docsPath(selection));
        addToast('Guide created');
      }
      mode = 'view';
      editingGuide = null;
      await tick();
      if (mainEl) mainEl.scrollTop = 0;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save guide', 'alert');
    } finally {
      saving = false;
    }
  }

  async function removeGuide(id: string) {
    deleting = true;
    try {
      await deleteGuide(id);
      userGuides = userGuides.filter((g) => g.id !== id);
      addToast('Guide deleted');
      if (selection.kind === 'guides' && selection.id === id) {
        const next: DocsSelection = { kind: 'guides', id: allGuides[0]?.id ?? '' };
        selection = next;
        push(docsPath(next));
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete guide', 'alert');
    } finally {
      deleting = false;
    }
  }

  async function openNav() {
    navOpen = true;
    await tick();
    document.getElementById('docs-search')?.focus();
  }

  function closeNav() {
    navOpen = false;
    navToggleEl?.focus();
  }

  function onWindowKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape' && navOpen) {
      e.preventDefault();
      closeNav();
    }
  }

  const toolbarButton =
    'inline-flex items-center gap-1.5 border border-gray-300 px-2 py-1 text-xs font-medium text-gray-700 transition-colors hover:bg-gray-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 motion-reduce:transition-none dark:border-dark-border-subtle dark:text-dark-text-secondary dark:hover:bg-dark-elevated dark:focus-visible:outline-accent';
</script>

<svelte:head>
  <title>AT | Documentation</title>
</svelte:head>

<svelte:window onkeydown={onWindowKeydown} />

<div class="flex h-full min-h-0 bg-gray-50 dark:bg-dark-base">
  {#if navOpen}
    <!-- Mobile scrim. Desktop keeps the sidebar in flow, so it is hidden there. -->
    <button
      type="button"
      aria-label="Close navigation"
      onclick={closeNav}
      class="fixed inset-0 z-30 bg-gray-900/50 lg:hidden"
    ></button>
  {/if}

  <aside
    id="docs-nav"
    class={[
      'z-40 flex-col border-gray-200 bg-white dark:border-dark-border dark:bg-dark-surface',
      'lg:static lg:z-auto lg:flex lg:w-64 lg:shrink-0 lg:border-r lg:shadow-none',
      navOpen ? 'fixed inset-y-0 left-0 flex w-72 max-w-[85vw] border-r shadow-xl' : 'hidden',
    ]}
  >
    <div
      class="flex shrink-0 items-center justify-between border-b border-gray-200 px-3 py-2 lg:hidden dark:border-dark-border"
    >
      <span class="text-xs font-semibold text-gray-900 dark:text-dark-text">Contents</span>
      <button
        type="button"
        onclick={closeNav}
        aria-label="Close navigation"
        class="p-1 text-gray-700 hover:text-gray-900 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 dark:text-dark-text-secondary dark:hover:text-dark-text dark:focus-visible:outline-accent"
      >
        <X size={14} aria-hidden="true" />
      </button>
    </div>

    <DocsSidebar
      apiEntries={filteredApi}
      guideEntries={filteredGuides}
      apiTotal={apiSections.length}
      guideTotal={allGuides.length}
      {selection}
      bind:query
      bind:groups
      {guidesLoading}
      {guidesError}
      onselect={select}
      onnewguide={startNewGuide}
      onretryguides={loadGuides}
    />
  </aside>

  <div class="flex min-h-0 min-w-0 flex-1 flex-col">
    <div
      class="flex shrink-0 items-center gap-2 border-b border-gray-200 bg-white px-3 py-2 dark:border-dark-border dark:bg-dark-surface"
    >
      <button
        type="button"
        bind:this={navToggleEl}
        onclick={openNav}
        aria-expanded={navOpen}
        aria-controls="docs-nav"
        class={[toolbarButton, 'lg:hidden']}
      >
        <Menu size={13} aria-hidden="true" />
        Contents
      </button>
      <p class="min-w-0 flex-1 truncate text-xs text-gray-600 dark:text-dark-text-secondary">
        <span class="hidden sm:inline">{groupLabel} <span aria-hidden="true">/</span> </span>
        <span class="font-medium text-gray-900 dark:text-dark-text">{currentTitle}</span>
      </p>
      <button type="button" onclick={refreshAll} class={toolbarButton} aria-label="Refresh gateway data and guides">
        <RefreshCw
          size={13}
          class={infoLoading || guidesLoading ? 'animate-spin motion-reduce:animate-none' : ''}
          aria-hidden="true"
        />
        <span class="hidden sm:inline">Refresh</span>
      </button>
    </div>

    <main
      bind:this={mainEl}
      class={[
        'min-h-0 min-w-0 flex-1',
        mode === 'view' ? 'overflow-y-auto' : 'overflow-hidden',
      ]}
    >
      {#if mode !== 'view'}
        {#key `${mode}:${editingGuide?.id ?? ''}`}
          <GuideEditor
            mode={mode === 'new' ? 'new' : 'edit'}
            guide={editingGuide}
            {saving}
            onsave={saveGuide}
            oncancel={cancelEdit}
          />
        {/key}
      {:else if selection.kind === 'api'}
        {#if activeSection}
          <div
            class={[
              'mx-auto w-full px-5 py-6 sm:px-8',
              activeSection.wide ? 'max-w-4xl' : 'max-w-3xl',
            ]}
          >
            <DocsPaneHeader title={activeSection.title} description={activeSection.description}>
              {#snippet actions()}
                <DocsCopyLinkButton {selection} />
              {/snippet}
            </DocsPaneHeader>
            <div class="space-y-4">
              <DocsApiPane
                sectionId={activeSection.id}
                {baseUrl}
                instanceName={storeInfo.name || 'AT'}
                {providers}
                {mcpServers}
                models={allModels}
                {exampleModel}
                loading={infoLoading}
                {infoError}
                onretry={loadInfo}
                bind:codeTab
                bind:mcpName
              />
            </div>
          </div>
        {/if}
      {:else if activeGuide}
        <div class="mx-auto w-full max-w-3xl px-5 py-6 sm:px-8">
          <GuideViewer
            guide={activeGuide}
            onedit={startEditGuide}
            ondelete={removeGuide}
            {deleting}
          />
        </div>
      {:else if guidesLoading}
        <div class="mx-auto w-full max-w-3xl px-5 py-10">
          <p class="text-sm text-gray-600 dark:text-dark-text-secondary">Loading guide…</p>
        </div>
      {:else}
        <div class="mx-auto w-full max-w-3xl px-5 py-10 text-center">
          <BookOpen
            size={22}
            class="mx-auto text-gray-600 dark:text-dark-text-secondary"
            aria-hidden="true"
          />
          <h1 class="mt-3 text-base font-semibold text-gray-900 dark:text-dark-text">
            Guide not found
          </h1>
          <p class="mt-1 text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
            {guidesError
              ? guidesError
              : 'This guide no longer exists, or the link points at another workspace.'}
          </p>
          <div class="mt-4 flex flex-wrap justify-center gap-2">
            <button type="button" onclick={loadGuides} class={toolbarButton}>Reload guides</button>
            <button
              type="button"
              onclick={() => select({ kind: 'api', id: 'overview' })}
              class={toolbarButton}
            >
              Go to Overview
            </button>
          </div>
        </div>
      {/if}
    </main>
  </div>
</div>
