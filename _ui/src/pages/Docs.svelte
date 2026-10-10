<script lang="ts">
  // One sidebar-driven documentation surface: the reference pages and the
  // guide library share a single navigation tree, a single search box, a
  // single reading order and a single URL scheme. This page is the shell —
  // data loading, selection state and layout. Rendering lives in
  // lib/components/docs/.
  import { untrack, tick } from 'svelte';
  import { push, router } from 'svelte-spa-router';
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
    DOCS_GROUP_STORAGE_KEY,
    adjacentEntries,
    docsPath,
    filterEntries,
    groupExpanded,
    parseDocsQuery,
    parseGroupState,
    sameSelection,
    serializeGroupState,
    type DocsGroupState,
    type DocsSelection,
  } from '@/lib/helper/docs-nav';
  import { apiGroups, apiSections, findApiSection } from '@/lib/components/docs/api-sections';
  import { builtinGuides, type DisplayGuide } from '@/lib/components/docs/builtin-guides';
  import type { SidebarGroup } from '@/lib/components/docs/docs-types';
  import DocsSidebar from '@/lib/components/docs/DocsSidebar.svelte';
  import DocsApiPane from '@/lib/components/docs/DocsApiPane.svelte';
  import DocsPaneHeader from '@/lib/components/docs/DocsPaneHeader.svelte';
  import DocsPager from '@/lib/components/docs/DocsPager.svelte';
  import DocsCopyLinkButton from '@/lib/components/docs/DocsCopyLinkButton.svelte';
  import GuideViewer from '@/lib/components/docs/GuideViewer.svelte';
  import GuideEditor from '@/lib/components/docs/GuideEditor.svelte';
  import { deploymentOrigin } from '@/lib/helper/deployment-url';

  storeNavbar.title = 'Documentation';

  const DEFAULT_SELECTION: DocsSelection = { kind: 'api', id: 'overview' };

  // ─── Gateway info (reference live data) ───
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
  let expanded = $state<DocsGroupState>(readGroupState());
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
      return {};
    }
  }

  $effect(() => {
    const raw = serializeGroupState(expanded);
    try {
      localStorage.setItem(DOCS_GROUP_STORAGE_KEY, raw);
    } catch {
      // Private mode / quota — collapsing still works for this session.
    }
  });

  // ─── Derived data ───
  const myGuides = $derived<DisplayGuide[]>(
    userGuides.map((g) => ({
      id: g.id,
      title: g.title || '(untitled)',
      description: g.description || '',
      iconName: g.icon || 'BookOpen',
      content: g.content || '',
      builtin: false,
      body: g.content || '',
    })),
  );
  const allGuides = $derived<DisplayGuide[]>([...builtinGuides, ...myGuides]);

  // Sidebar groups double as the reading order for previous/next.
  const navGroups = $derived<SidebarGroup[]>([
    ...apiGroups.map((g) => {
      const entries = apiSections.filter((s) => s.group === g.id);
      return { id: g.id, kind: 'api' as const, title: g.title, total: entries.length, entries };
    }),
    { id: 'guides', kind: 'guides', title: 'Guides', total: builtinGuides.length, entries: builtinGuides },
    { id: 'my-guides', kind: 'guides', title: 'My guides', total: myGuides.length, entries: myGuides },
  ]);

  const visibleGroups = $derived(
    navGroups.map((g) => ({ ...g, entries: filterEntries(g.entries, query) })),
  );

  const readingOrder = $derived(
    navGroups.flatMap((g) =>
      g.entries.map((e) => ({
        id: `${g.kind}:${e.id}`,
        selection: { kind: g.kind, id: e.id } as DocsSelection,
        title: e.title,
        group: g.title,
      })),
    ),
  );
  const currentEntry = $derived(
    readingOrder.find((e) => e.id === `${selection.kind}:${selection.id}`),
  );
  const pager = $derived(adjacentEntries(readingOrder, `${selection.kind}:${selection.id}`));

  const activeSection = $derived(
    selection.kind === 'api' ? findApiSection(selection.id) : undefined,
  );
  const activeGuide = $derived(
    selection.kind === 'guides' ? allGuides.find((g) => g.id === selection.id) : undefined,
  );

  const currentTitle = $derived(
    mode === 'new'
      ? 'New guide'
      : mode === 'edit'
        ? 'Edit guide'
        : (activeSection?.title ?? activeGuide?.title ?? 'Documentation'),
  );

  // Resolved from the document base, not location.pathname: on a SPA
  // fallback deep path the latter yields the route instead of the
  // deployment root, and every generated snippet inherits it.
  const baseUrl = deploymentOrigin();

  const allModels = $derived(
    providers.flatMap((p) =>
      p.models && p.models.length > 0
        ? p.models.map((m) => `${p.key}/${m}`)
        : p.default_model
          ? [`${p.key}/${p.default_model}`]
          : [],
    ),
  );
  const exampleModel = $derived(allModels[0] ?? 'openai/gpt-4o');
  // A fallback from another provider when one exists — that is the case the
  // examples are meant to show.
  const fallbackModel = $derived(
    allModels.find((m) => m.split('/')[0] !== exampleModel.split('/')[0]) ??
      allModels[1] ??
      'anthropic/claude-sonnet-4-5',
  );

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
    const resolved = resolveSelection(parseDocsQuery(router.querystring || ''));
    if (!sameSelection(resolved, untrack(() => selection))) {
      selection = resolved;
      // Back/forward navigation must abandon a half-written draft rather than
      // render the editor against a different guide.
      mode = 'view';
      editingGuide = null;
    }
  });

  function groupOf(sel: DocsSelection): string | undefined {
    return navGroups.find((g) => g.kind === sel.kind && g.entries.some((e) => e.id === sel.id))?.id;
  }

  // Expand the group that owns the selection whenever the selection changes —
  // but never fight a deliberate collapse of an already-selected group.
  let lastSelectionKey = '';
  $effect(() => {
    const key = `${selection.kind}:${selection.id}`;
    const owner = groupOf(selection);
    if (key === lastSelectionKey || !owner) return;
    lastSelectionKey = key;
    untrack(() => {
      if (!groupExpanded(expanded, owner)) expanded = { ...expanded, [owner]: true };
    });
  });

  // Searching reveals hits in every group.
  let wasSearching = false;
  $effect(() => {
    const searching = query.trim().length > 0;
    if (searching && !wasSearching) untrack(() => (expanded = {}));
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
    // Both are best-effort: the reference is static content and must stay
    // readable when the gateway info endpoint is unavailable.
    const [info, mcpRes] = await Promise.allSettled([getInfo(), listMCPServers({ _limit: 100 })]);

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
    if (!groupExpanded(expanded, 'my-guides')) expanded = { ...expanded, 'my-guides': true };
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

  function isTyping(target: EventTarget | null): boolean {
    const el = target as HTMLElement | null;
    if (!el) return false;
    return el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName);
  }

  async function onWindowKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape' && navOpen) {
      e.preventDefault();
      closeNav();
      return;
    }
    // "/" focuses search, like most documentation sites.
    if (e.key === '/' && !e.ctrlKey && !e.metaKey && !e.altKey && mode === 'view' && !isTyping(e.target)) {
      if (document.querySelector('dialog[open], [role="dialog"]')) return;
      e.preventDefault();
      if (window.matchMedia('(min-width: 1024px)').matches) {
        document.getElementById('docs-search')?.focus();
      } else {
        await openNav();
      }
    }
  }
</script>

<svelte:head>
  <title>AT | {currentTitle}</title>
</svelte:head>

<svelte:window onkeydown={onWindowKeydown} />

<div class="flex h-full min-h-0">
  {#if navOpen}
    <!-- Mobile scrim. Desktop keeps the sidebar in flow, so it is hidden there. -->
    <button
      type="button"
      aria-label="Close navigation"
      onclick={closeNav}
      class="fixed inset-0 z-30 bg-black/50 lg:hidden"
    ></button>
  {/if}

  <aside
    id="docs-nav"
    class={[
      'z-40 flex-col border-dark-border bg-dark-base',
      'lg:static lg:z-auto lg:flex lg:w-64 lg:shrink-0 lg:border-r',
      navOpen ? 'fixed inset-y-0 left-0 flex w-72 max-w-[85vw] border-r bg-dark-surface' : 'hidden',
    ]}
  >
    <div class="flex shrink-0 items-center justify-between border-b border-dark-border px-3 py-2 lg:hidden">
      <span class="text-xs font-semibold text-dark-text">Contents</span>
      <button
        type="button"
        onclick={closeNav}
        aria-label="Close navigation"
        class="p-1 text-dark-text-muted hover:text-dark-text focus-visible:outline-2 focus-visible:outline-accent"
      >
        <X size={14} aria-hidden="true" />
      </button>
    </div>

    <DocsSidebar
      groups={visibleGroups}
      {selection}
      bind:query
      bind:expanded
      {guidesLoading}
      {guidesError}
      onselect={select}
      onnewguide={startNewGuide}
      onretryguides={loadGuides}
    />
  </aside>

  <div class="flex min-h-0 min-w-0 flex-1 flex-col">
    <div class="flex shrink-0 items-center gap-2 border-b border-dark-border px-3 py-1.5">
      <button
        type="button"
        bind:this={navToggleEl}
        onclick={openNav}
        aria-expanded={navOpen}
        aria-controls="docs-nav"
        class="settings-button lg:hidden"
      >
        <Menu size={13} aria-hidden="true" />
        Contents
      </button>
      <p class="min-w-0 flex-1 truncate text-xs text-dark-text-muted">
        {#if currentEntry && mode === 'view'}
          <span class="hidden sm:inline">{currentEntry.group} <span aria-hidden="true">/</span> </span>
        {/if}
        <span class="text-dark-text">{currentTitle}</span>
      </p>
      <button
        type="button"
        onclick={refreshAll}
        class="settings-button"
        aria-label="Refresh gateway data and guides"
        title="Refresh gateway data and guides"
      >
        <RefreshCw
          size={13}
          class={infoLoading || guidesLoading ? 'animate-spin motion-reduce:animate-none' : ''}
          aria-hidden="true"
        />
      </button>
    </div>

    <main
      bind:this={mainEl}
      class={['min-h-0 min-w-0 flex-1', mode === 'view' ? 'overflow-y-auto' : 'overflow-hidden']}
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
          <div class="mx-auto w-full max-w-4xl px-5 pb-12 pt-8 sm:px-10">
            <DocsPaneHeader
              title={activeSection.title}
              description={activeSection.description}
              eyebrow={currentEntry?.group}
            >
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
                {fallbackModel}
                loading={infoLoading}
                {infoError}
                onretry={loadInfo}
                bind:codeTab
                bind:mcpName
              />
            </div>
            <DocsPager prev={pager.prev} next={pager.next} />
          </div>
        {/if}
      {:else if activeGuide}
        <div class="mx-auto w-full max-w-5xl px-5 pb-12 pt-8 sm:px-10">
          <GuideViewer
            guide={activeGuide}
            eyebrow={currentEntry?.group}
            onedit={startEditGuide}
            ondelete={removeGuide}
            {deleting}
          />
          <DocsPager prev={pager.prev} next={pager.next} />
        </div>
      {:else if guidesLoading}
        <div class="mx-auto w-full max-w-4xl px-5 py-10 sm:px-10">
          <p class="text-sm text-dark-text-muted">Loading guide…</p>
        </div>
      {:else}
        <div class="mx-auto w-full max-w-4xl px-5 py-16 text-center sm:px-10">
          <BookOpen size={22} class="mx-auto text-dark-text-muted" aria-hidden="true" />
          <h1 class="mt-3 text-base font-semibold text-dark-text">Guide not found</h1>
          <p class="mt-1 text-sm leading-relaxed text-dark-text-muted">
            {guidesError || 'This guide no longer exists, or the link points at another workspace.'}
          </p>
          <div class="mt-4 flex flex-wrap justify-center gap-2">
            <button type="button" onclick={loadGuides} class="settings-button">Reload guides</button>
            <button type="button" onclick={() => select(DEFAULT_SELECTION)} class="settings-button">
              Go to Overview
            </button>
          </div>
        </div>
      {/if}
    </main>
  </div>
</div>
