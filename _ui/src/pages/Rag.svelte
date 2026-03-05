<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listCollections,
    createCollection,
    updateCollection,
    deleteCollection,
    uploadDocument,
    importFromURL,
    searchRAG,
    discoverEmbeddingModels,
    testEmbedding,
    type RAGCollection,
    type SearchResult,
  } from '@/lib/api/rag';
  import { listProviders } from '@/lib/api/providers';
  import type { ProviderRecord } from '@/lib/api/providers';
  import {
    Database,
    Plus,
    Pencil,
    Trash2,
    X,
    Save,
    RefreshCw,
    Upload,
    Link,
    Search,
    FileText,
    ChevronDown,
    ChevronRight,
    Zap,
  } from 'lucide-svelte';
  import { formatDate } from '@/lib/helper/format';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';

  storeNavbar.title = 'RAG';

  // ─── State ───

  let collections = $state<RAGCollection[]>([]);
  let providers = $state<ProviderRecord[]>([]);
  let loading = $state(true);
  
  // Pagination
  let offset = $state(0);
  let limit = $state(10);
  let total = $state(0);

  // Search & Sort (table filter, not RAG search)
  let nameSearch = $state('');
  let sorts = $state<SortEntry[]>([]);

  let showForm = $state(false);
  let editingId = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);
  let saving = $state(false);

  // Collection form fields
  let formName = $state('');
  let formDescription = $state('');
  let formVectorStoreType = $state('pgvector');
  let formVectorStoreConfig = $state('{}');
  let formEmbeddingProvider = $state('');
  let formEmbeddingModel = $state('');
  let formEmbeddingURL = $state('');
  let formEmbeddingAPIType = $state('openai');
  let formEmbeddingBearerAuth = $state(false);
  let formChunkSize = $state(512);
  let formChunkOverlap = $state(100);

  // Upload state
  let uploadCollectionId = $state<string | null>(null);
  let uploading = $state(false);

  // URL import state
  let importCollectionId = $state<string | null>(null);
  let importURL = $state('');
  let importing = $state(false);

  // Search state
  let searchQuery = $state('');
  let searchCollectionIds = $state<string[]>([]);
  let searchNumResults = $state(5);
  let searchResults = $state<SearchResult[]>([]);
  let searching = $state(false);
  let showSearch = $state(false);

  // Expanded row for actions
  let expandedId = $state<string | null>(null);

  // Embedding model discovery
  let fetchingModels = $state(false);
  let discoveredModels = $state<string[]>([]);

  // Embedding test
  let testingEmbedding = $state(false);
  let testResult = $state<{ success: boolean; dimensions: number; error?: string } | null>(null);

  const vectorStoreTypes = ['pgvector', 'chroma', 'qdrant', 'weaviate', 'pinecone', 'milvus'];

  const vectorStoreExamples: Record<string, string> = {
    pgvector: JSON.stringify({ connection_url: 'postgres://user:pass@localhost:5432/dbname', collection_name: 'documents' }, null, 2),
    chroma: JSON.stringify({ url: 'http://localhost:8000', collection_name: 'documents', namespace: '' }, null, 2),
    qdrant: JSON.stringify({ url: 'http://localhost:6334', collection_name: 'documents', api_key: '' }, null, 2),
    weaviate: JSON.stringify({ scheme: 'http', host: 'localhost:8080', index_name: 'Documents', api_key: '' }, null, 2),
    pinecone: JSON.stringify({ api_key: '', environment: 'us-east-1', index_name: 'documents', project_name: '', namespace: '' }, null, 2),
    milvus: JSON.stringify({ url: 'localhost:19530', collection_name: 'documents', username: '', password: '' }, null, 2),
  };

  function onVectorStoreTypeChange(newType: string) {
    formVectorStoreType = newType;
    // Only auto-fill if the current config is empty or matches another example
    const currentTrimmed = formVectorStoreConfig.trim();
    const isDefaultOrExample = currentTrimmed === '{}' || currentTrimmed === '' ||
      Object.values(vectorStoreExamples).some(ex => currentTrimmed === ex.trim());
    if (isDefaultOrExample) {
      formVectorStoreConfig = vectorStoreExamples[newType] || '{}';
    }
  }

  // ─── Load ───

  async function load() {
    loading = true;
    try {
      const params: any = { _offset: offset, _limit: limit };
      if (nameSearch) params['name[like]'] = `%${nameSearch}%`;
      const sortParam = buildSortParam(sorts);
      if (sortParam) params._sort = sortParam;
      const [cRes, pRes] = await Promise.all([
        listCollections(params),
        listProviders().catch(() => ({ data: [], meta: { total: 0, offset: 0, limit: 0 } })),
      ]);
      collections = cRes.data || [];
      total = cRes.meta?.total || 0;
      providers = pRes.data || [];
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load collections', 'alert');
    } finally {
      loading = false;
    }
  }

  function handleTableSearch(value: string) {
    nameSearch = value;
    offset = 0;
    load();
  }

  function handleSort(field: string, multiSort: boolean) {
    sorts = toggleSort(sorts, field, multiSort);
    offset = 0;
    load();
  }

  load();

  // ─── Collection Form ───

  function resetForm() {
    formName = '';
    formDescription = '';
    formVectorStoreType = 'pgvector';
    formVectorStoreConfig = '{}';
    formEmbeddingProvider = '';
    formEmbeddingModel = '';
    formEmbeddingURL = '';
    formEmbeddingAPIType = 'openai';
    formEmbeddingBearerAuth = false;
    formChunkSize = 512;
    formChunkOverlap = 100;
    editingId = null;
    showForm = false;
    discoveredModels = [];
    testResult = null;
  }

  function openCreate() {
    resetForm();
    formVectorStoreConfig = vectorStoreExamples['pgvector'] || '{}';
    showForm = true;
  }

  function openEdit(c: RAGCollection) {
    resetForm();
    editingId = c.id;
    formName = c.name;
    formDescription = c.config.description || '';
    formVectorStoreType = c.config.vector_store.type;
    formVectorStoreConfig = JSON.stringify(c.config.vector_store.config || {}, null, 2);
    formEmbeddingProvider = c.config.embedding_provider || '';
    formEmbeddingModel = c.config.embedding_model || '';
    formEmbeddingURL = c.config.embedding_url || '';
    formEmbeddingAPIType = c.config.embedding_api_type || 'openai';
    formEmbeddingBearerAuth = c.config.embedding_bearer_auth || false;
    formChunkSize = c.config.chunk_size || 512;
    formChunkOverlap = c.config.chunk_overlap || 100;
    showForm = true;
  }

  async function handleSubmit() {
    if (!formName.trim()) {
      addToast('Collection name is required', 'warn');
      return;
    }
    if (!formEmbeddingProvider.trim()) {
      addToast('Embedding provider is required', 'warn');
      return;
    }
    if (!formEmbeddingModel.trim() && !formEmbeddingURL.trim()) {
      addToast('Embedding model is required when embed URL is not set', 'warn');
      return;
    }

    let vsConfig: Record<string, any> = {};
    try {
      vsConfig = JSON.parse(formVectorStoreConfig);
    } catch {
      addToast('Vector store config must be valid JSON', 'warn');
      return;
    }

    saving = true;
    try {
      const chunkSize = Number(formChunkSize) || 512;
      const chunkOverlap = Number(formChunkOverlap) || 100;

      const payload = {
        name: formName.trim(),
        config: {
          description: formDescription.trim(),
          vector_store: {
            type: formVectorStoreType,
            config: vsConfig,
          },
          embedding_provider: formEmbeddingProvider.trim(),
          embedding_model: formEmbeddingModel.trim(),
          embedding_url: formEmbeddingURL.trim(),
          embedding_api_type: formEmbeddingAPIType,
          embedding_bearer_auth: formEmbeddingBearerAuth,
          chunk_size: chunkSize,
          chunk_overlap: chunkOverlap,
        },
      };

      if (editingId) {
        await updateCollection(editingId, payload);
        addToast(`Collection "${formName}" updated`);
      } else {
        await createCollection(payload);
        addToast(`Collection "${formName}" created`);
      }
      resetForm();
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save collection', 'alert');
    } finally {
      saving = false;
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteCollection(id);
      addToast('Collection deleted');
      deleteConfirm = null;
      expandedId = null;
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete collection', 'alert');
    }
  }

  // ─── Upload ───

  async function handleFileUpload(event: Event) {
    const input = event.target as HTMLInputElement;
    if (!input.files?.length || !uploadCollectionId) return;

    uploading = true;
    let successCount = 0;
    let failCount = 0;

    for (const file of input.files) {
      try {
        const result = await uploadDocument(uploadCollectionId, file);
        successCount++;
        addToast(`"${file.name}" ingested (${result.chunks_stored} chunks)`);
      } catch (e: any) {
        failCount++;
        addToast(e?.response?.data?.message || `Failed to upload "${file.name}"`, 'alert');
      }
    }

    uploading = false;
    input.value = '';
    if (successCount > 0 && failCount === 0) {
      uploadCollectionId = null;
    }
  }

  // ─── URL Import ───

  async function handleImportURL() {
    if (!importURL.trim() || !importCollectionId) return;

    importing = true;
    try {
      const result = await importFromURL(importCollectionId, importURL.trim());
      addToast(`URL imported (${result.chunks_stored} chunks)`);
      importURL = '';
      importCollectionId = null;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to import URL', 'alert');
    } finally {
      importing = false;
    }
  }

  // ─── Search ───

  async function handleSearch() {
    if (!searchQuery.trim()) {
      addToast('Search query is required', 'warn');
      return;
    }

    searching = true;
    try {
      searchResults = await searchRAG({
        query: searchQuery.trim(),
        collection_ids: searchCollectionIds.length > 0 ? searchCollectionIds : undefined,
        num_results: searchNumResults,
      });
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Search failed', 'alert');
    } finally {
      searching = false;
    }
  }

  function toggleExpand(id: string) {
    expandedId = expandedId === id ? null : id;
    // Close upload/import when collapsing
    if (expandedId !== id) {
      if (uploadCollectionId === id) uploadCollectionId = null;
      if (importCollectionId === id) importCollectionId = null;
    }
  }

  // ─── Embedding Model Discovery ───

  async function handleDiscoverModels() {
    if (!formEmbeddingProvider.trim()) {
      addToast('Select an embedding provider first', 'warn');
      return;
    }

    fetchingModels = true;
    discoveredModels = [];
    try {
      const models = await discoverEmbeddingModels({
        embedding_provider: formEmbeddingProvider.trim(),
        embedding_api_type: formEmbeddingAPIType || undefined,
        embedding_url: formEmbeddingURL.trim() || undefined,
        embedding_bearer_auth: formEmbeddingBearerAuth || undefined,
      });
      discoveredModels = models;
      if (models.length === 0) {
        addToast('No embedding models found for this provider', 'warn');
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to discover embedding models', 'alert');
    } finally {
      fetchingModels = false;
    }
  }

  function selectModel(model: string) {
    formEmbeddingModel = model;
    discoveredModels = [];
  }

  // ─── Test Embedding ───

  async function handleTestEmbedding() {
    if (!formEmbeddingProvider.trim()) {
      addToast('Select an embedding provider first', 'warn');
      return;
    }
    if (!formEmbeddingModel.trim() && !formEmbeddingURL.trim()) {
      addToast('Embedding model or URL is required', 'warn');
      return;
    }

    testingEmbedding = true;
    testResult = null;
    try {
      const result = await testEmbedding({
        embedding_provider: formEmbeddingProvider.trim(),
        embedding_model: formEmbeddingModel.trim() || undefined,
        embedding_url: formEmbeddingURL.trim() || undefined,
        embedding_api_type: formEmbeddingAPIType || undefined,
        embedding_bearer_auth: formEmbeddingBearerAuth || undefined,
      });
      testResult = { success: true, dimensions: result.dimensions };
      addToast(`Embedding test passed (${result.dimensions} dimensions)`);
    } catch (e: any) {
      const msg = e?.response?.data?.message || 'Embedding test failed';
      testResult = { success: false, dimensions: 0, error: msg };
      addToast(msg, 'alert');
    } finally {
      testingEmbedding = false;
    }
  }

</script>

<svelte:head>
  <title>AT | RAG</title>
</svelte:head>

<div class="p-6 max-w-5xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-4">
    <div class="flex items-center gap-2">
      <Database size={16} class="text-gray-500 dark:text-dark-text-muted" />
      <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">RAG Collections</h2>
      <span class="text-xs text-gray-400 dark:text-dark-text-muted">({total})</span>
    </div>
    <div class="flex items-center gap-2">
      <button
        onclick={() => { showSearch = !showSearch; }}
        class={[
          "flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium border transition-colors",
          showSearch
            ? "bg-gray-900 text-white border-gray-900 dark:bg-accent dark:border-accent dark:text-white"
            : "border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated",
        ]}
      >
        <Search size={12} />
        Search
      </button>
      <button
        onclick={load}
        class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
        title="Refresh"
      >
        <RefreshCw size={14} />
      </button>
      <button
        onclick={openCreate}
        class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors"
      >
        <Plus size={12} />
        New Collection
      </button>
    </div>
  </div>

  <!-- Search Panel -->
  {#if showSearch}
    <div class="border border-gray-200 dark:border-dark-border mb-6 bg-white dark:bg-dark-surface overflow-hidden">
      <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
        <span class="text-sm font-medium text-gray-900 dark:text-dark-text">Search Documents</span>
        <button onclick={() => { showSearch = false; searchResults = []; }} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors">
          <X size={14} />
        </button>
      </div>
      <form onsubmit={(e) => { e.preventDefault(); handleSearch(); }} class="p-4 space-y-3">
        <div class="flex gap-3">
          <input
            type="text"
            bind:value={searchQuery}
            placeholder="Enter your search query..."
            class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
          />
          <select
            bind:value={searchNumResults}
            class="border border-gray-300 dark:border-dark-border-subtle px-2 py-1.5 text-sm dark:bg-dark-elevated dark:text-dark-text transition-colors"
          >
            <option value={3}>3 results</option>
            <option value={5}>5 results</option>
            <option value={10}>10 results</option>
            <option value={20}>20 results</option>
          </select>
          <button
            type="submit"
            disabled={searching}
            class="flex items-center gap-1.5 px-4 py-1.5 text-sm bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors disabled:opacity-50"
          >
            <Search size={14} />
            {searching ? 'Searching...' : 'Search'}
          </button>
        </div>
        {#if collections.length > 1}
          <div class="flex items-center gap-2 flex-wrap">
            <span class="text-xs text-gray-500 dark:text-dark-text-muted">Filter by collection:</span>
            {#each collections as c}
              <label class="flex items-center gap-1 text-xs text-gray-600 dark:text-dark-text-secondary cursor-pointer">
                <input
                  type="checkbox"
                  value={c.id}
                  checked={searchCollectionIds.includes(c.id)}
                  onchange={(e) => {
                    const target = e.target as HTMLInputElement;
                    if (target.checked) {
                      searchCollectionIds = [...searchCollectionIds, c.id];
                    } else {
                      searchCollectionIds = searchCollectionIds.filter(id => id !== c.id);
                    }
                  }}
                  class="w-3 h-3 dark:bg-dark-elevated dark:border-dark-border-subtle dark:accent-accent"
                />
                {c.name}
              </label>
            {/each}
          </div>
        {/if}
      </form>

      <!-- Search Results -->
      {#if searchResults.length > 0}
        <div class="border-t border-gray-200 dark:border-dark-border">
          <div class="px-4 py-2 text-xs text-gray-500 dark:text-dark-text-muted bg-gray-50 dark:bg-dark-base">
            {searchResults.length} result{searchResults.length !== 1 ? 's' : ''}
          </div>
          {#each searchResults as result, i}
            <div class="px-4 py-3 border-t border-gray-100 dark:border-dark-border hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50">
              <div class="flex items-center gap-2 mb-1">
                <span class="text-xs font-mono text-gray-400 dark:text-dark-text-muted">#{i + 1}</span>
                <span class="text-xs px-1.5 py-0.5 bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted">
                  score: {result.score.toFixed(3)}
                </span>
                {#if result.metadata?.source}
                  <span class="text-xs text-gray-400 dark:text-dark-text-muted truncate" title={result.metadata.source}>
                    {result.metadata.source}
                  </span>
                {/if}
              </div>
              <div class="text-sm text-gray-700 dark:text-dark-text-secondary whitespace-pre-wrap line-clamp-4">{result.content}</div>
            </div>
          {/each}
        </div>
      {/if}
    </div>
  {/if}

  <!-- Collection Form -->
  {#if showForm}
    <div class="border border-gray-200 dark:border-dark-border mb-6 bg-white dark:bg-dark-surface overflow-hidden">
      <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
        <span class="text-sm font-medium text-gray-900 dark:text-dark-text">
          {editingId ? `Edit: ${formName}` : 'New Collection'}
        </span>
        <button onclick={resetForm} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors">
          <X size={14} />
        </button>
      </div>

      <form novalidate onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="p-4 space-y-4">
        <!-- Name -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-name" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Name</label>
          <input
            id="form-name"
            type="text"
            bind:value={formName}
            placeholder="e.g., product-docs, knowledge-base"
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
          />
        </div>

        <!-- Description -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-desc" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Description</label>
          <input
            id="form-desc"
            type="text"
            bind:value={formDescription}
            placeholder="What this collection contains (optional)"
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
          />
        </div>

        <!-- Vector Store Type -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-vs-type" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Vector Store</label>
          <select
            id="form-vs-type"
            bind:value={formVectorStoreType}
            onchange={(e) => onVectorStoreTypeChange((e.target as HTMLSelectElement).value)}
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm dark:bg-dark-elevated dark:text-dark-text transition-colors"
          >
            {#each vectorStoreTypes as type}
              <option value={type}>{type}</option>
            {/each}
          </select>
        </div>

        <!-- Vector Store Config -->
        <div class="grid grid-cols-4 gap-3 items-start">
          <label for="form-vs-config" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Store Config</label>
          <textarea
            id="form-vs-config"
            bind:value={formVectorStoreConfig}
            rows={4}
            placeholder={vectorStoreExamples[formVectorStoreType] || '{}'}
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors resize-y"
          ></textarea>
        </div>

        <!-- Embedding Provider -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-emb-provider" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Embed Provider</label>
          {#if providers.length > 0}
            <select
              id="form-emb-provider"
              bind:value={formEmbeddingProvider}
              class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm dark:bg-dark-elevated dark:text-dark-text transition-colors"
            >
              <option value="">Select provider...</option>
              {#each providers as p}
                <option value={p.key}>{p.key} ({p.config.type})</option>
              {/each}
            </select>
          {:else}
            <input
              id="form-emb-provider"
              type="text"
              bind:value={formEmbeddingProvider}
              placeholder="Provider key (e.g., openai)"
              class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
            />
          {/if}
        </div>

        <!-- Embedding Model -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-emb-model" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Embed Model</label>
          <div class="col-span-3 flex items-center gap-2">
            <input
              id="form-emb-model"
              type="text"
              bind:value={formEmbeddingModel}
              placeholder="e.g., text-embedding-3-small"
              class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
            />
            <button
              type="button"
              disabled={fetchingModels || !formEmbeddingProvider}
              onclick={handleDiscoverModels}
              class="flex items-center gap-1 px-2.5 py-1.5 text-xs font-medium border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary transition-colors disabled:opacity-50 whitespace-nowrap"
              title="Fetch available embedding models from the provider"
            >
              <RefreshCw size={12} class={fetchingModels ? 'animate-spin' : ''} />
              {fetchingModels ? 'Fetching...' : 'Fetch'}
            </button>
          </div>
          {#if discoveredModels.length > 0}
            <div class="col-start-2 col-span-3 -mt-2">
              <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface max-h-40 overflow-y-auto">
                {#each discoveredModels as model}
                  <button
                    type="button"
                    onclick={() => selectModel(model)}
                    class="w-full text-left px-3 py-1.5 text-xs font-mono hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary transition-colors border-b border-gray-100 dark:border-dark-border last:border-b-0"
                  >
                    {model}
                  </button>
                {/each}
              </div>
            </div>
          {/if}
        </div>

        <!-- Embedding API Type -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-emb-api-type" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Embed API Type</label>
          <select
            id="form-emb-api-type"
            bind:value={formEmbeddingAPIType}
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm dark:bg-dark-elevated dark:text-dark-text transition-colors"
          >
            <option value="openai">openai (OpenAI-compatible)</option>
            <option value="gemini">gemini (Google Generative Language)</option>
          </select>
        </div>

        <!-- Embedding URL (optional) -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-emb-url" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Embed URL</label>
          <input
            id="form-emb-url"
            type="text"
            bind:value={formEmbeddingURL}
            placeholder={formEmbeddingAPIType === 'gemini' ? 'Leave empty to auto-derive, or e.g. https://generativelanguage.googleapis.com/v1beta/models/text-embedding-004:batchEmbedContents' : 'Leave empty to auto-derive from provider base URL'}
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
          />
          <div class="col-start-2 col-span-3 text-xs text-gray-400 dark:text-dark-text-muted -mt-2">
            Optional. If empty, the URL is derived automatically from the provider's base URL.
          </div>
        </div>

        <!-- Bearer Auth (optional) -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-emb-bearer-auth" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Bearer Auth</label>
          <div class="col-span-3 flex items-center gap-3">
            <label class="relative inline-flex items-center cursor-pointer">
              <input
                id="form-emb-bearer-auth"
                type="checkbox"
                bind:checked={formEmbeddingBearerAuth}
                class="sr-only peer"
              />
              <div class="w-9 h-5 bg-gray-200 peer-focus:outline-none peer-focus:ring-2 peer-focus:ring-gray-900/10 dark:peer-focus:ring-accent/20 rounded-full peer dark:bg-dark-elevated peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all dark:after:border-dark-border-subtle peer-checked:bg-gray-900 dark:peer-checked:bg-accent"></div>
            </label>
            <span class="text-xs text-gray-400 dark:text-dark-text-muted">
              Send provider API key as Bearer token (for gateway proxy endpoints)
            </span>
          </div>
        </div>

        <!-- Chunk Size / Overlap -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Chunking</span>
          <div class="col-span-3 flex items-center gap-3">
            <div class="flex items-center gap-1.5">
              <label for="form-chunk-size" class="text-xs text-gray-500 dark:text-dark-text-muted">Size</label>
              <input
                id="form-chunk-size"
                type="number"
                bind:value={formChunkSize}
                min={64}
                max={8192}
                class="w-24 border border-gray-300 dark:border-dark-border-subtle px-2 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text transition-colors"
              />
            </div>
            <div class="flex items-center gap-1.5">
              <label for="form-chunk-overlap" class="text-xs text-gray-500 dark:text-dark-text-muted">Overlap</label>
              <input
                id="form-chunk-overlap"
                type="number"
                bind:value={formChunkOverlap}
                min={0}
                max={2048}
                class="w-24 border border-gray-300 dark:border-dark-border-subtle px-2 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text transition-colors"
              />
            </div>
            <span class="text-xs text-gray-400 dark:text-dark-text-muted">characters</span>
          </div>
        </div>

        <!-- Actions -->
        <div class="flex justify-between items-center pt-3 border-t border-gray-100 dark:border-dark-border">
          <div class="flex items-center gap-2">
            <button
              type="button"
              disabled={testingEmbedding || !formEmbeddingProvider}
              onclick={handleTestEmbedding}
              class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary transition-colors disabled:opacity-50"
              title="Send a test embedding request to verify the configuration"
            >
              <Zap size={12} />
              {#if testingEmbedding}
                Testing...
              {:else}
                Test Embedding
              {/if}
            </button>
            {#if testResult}
              <span class="text-xs {testResult.success ? 'text-green-600 dark:text-green-400' : 'text-red-600 dark:text-red-400'}">
                {#if testResult.success}
                  OK ({testResult.dimensions}d)
                {:else}
                  Failed
                {/if}
              </span>
            {/if}
          </div>
          <div class="flex gap-2">
            <button
              type="button"
              onclick={resetForm}
              class="px-3 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={saving}
              class="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors disabled:opacity-50"
            >
              <Save size={14} />
              {#if saving}
                Saving...
              {:else}
                {editingId ? 'Update' : 'Create'}
              {/if}
            </button>
          </div>
        </div>
      </form>
    </div>
  {/if}

  <!-- Collection List -->
  {#if loading || collections.length > 0 || !showForm}
    <DataTable
      items={collections}
      {loading}
      {total}
      {limit}
      bind:offset
      onchange={load}
      onsearch={handleTableSearch}
      searchPlaceholder="Search by name..."
      emptyIcon={Database}
      emptyTitle="No RAG collections"
      emptyDescription="Create a collection to start ingesting and searching documents"
    >
      {#snippet header()}
        <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-6"></th>
        <SortableHeader field="name" label="Name" {sorts} onsort={handleSort} />
        <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Vector Store</th>
        <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Embedding</th>
        <SortableHeader field="updated_at" label="Updated" {sorts} onsort={handleSort} />
        <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-24"></th>
      {/snippet}

      {#snippet row(c)}
        <!-- Collection Row -->
        <tr
          class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors cursor-pointer"
          onclick={() => toggleExpand(c.id)}
        >
          <td class="px-4 py-2.5 text-gray-400 dark:text-dark-text-muted">
            {#if expandedId === c.id}
              <ChevronDown size={14} />
            {:else}
              <ChevronRight size={14} />
            {/if}
          </td>
          <td class="px-4 py-2.5">
            <div class="font-medium text-gray-900 dark:text-dark-text">{c.name}</div>
            {#if c.config.description}
              <div class="text-xs text-gray-500 dark:text-dark-text-muted truncate max-w-48">{c.config.description}</div>
            {/if}
          </td>
          <td class="px-4 py-2.5">
            <span class="text-xs font-mono px-1.5 py-0.5 bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary">{c.config.vector_store.type}</span>
          </td>
          <td class="px-4 py-2.5">
            <div class="text-xs text-gray-600 dark:text-dark-text-secondary">{c.config.embedding_provider}</div>
            <div class="text-xs font-mono text-gray-400 dark:text-dark-text-muted">{c.config.embedding_model}</div>
          </td>
          <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">{formatDate(c.updated_at)}</td>
          <td class="px-4 py-2.5 text-right" onclick={(e) => e.stopPropagation()}>
            <div class="flex justify-end gap-1">
              <button
                onclick={() => openEdit(c)}
                class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text transition-colors"
                title="Edit"
              >
                <Pencil size={14} />
              </button>
              {#if deleteConfirm === c.id}
                <button
                  onclick={() => handleDelete(c.id)}
                  class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 transition-colors"
                >
                  Confirm
                </button>
                <button
                  onclick={() => (deleteConfirm = null)}
                  class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
                >
                  Cancel
                </button>
              {:else}
                <button
                  onclick={() => (deleteConfirm = c.id)}
                  class="p-1.5 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 dark:text-dark-text-muted hover:text-red-600 dark:hover:text-red-400 transition-colors"
                  title="Delete"
                >
                  <Trash2 size={14} />
                </button>
              {/if}
            </div>
          </td>
        </tr>

        <!-- Expanded Actions Row -->
        {#if expandedId === c.id}
          <tr class="bg-gray-50/50 dark:bg-dark-base/50">
            <td colspan="6" class="px-4 py-3">
              <div class="flex items-center gap-3 flex-wrap">
                <!-- Upload Button -->
                <div class="flex items-center gap-2">
                  {#if uploadCollectionId === c.id}
                    <input
                      type="file"
                      accept=".md,.txt,.pdf,.html,.csv,.json"
                      multiple
                      onchange={handleFileUpload}
                      disabled={uploading}
                      class="text-xs file:mr-2 file:px-3 file:py-1 file:text-xs file:border file:border-gray-300 dark:file:border-dark-border-subtle file:bg-white dark:file:bg-dark-elevated file:text-gray-700 dark:file:text-dark-text-secondary file:cursor-pointer hover:file:bg-gray-50 dark:hover:file:bg-dark-base disabled:opacity-50"
                    />
                    {#if uploading}
                      <span class="text-xs text-gray-500 dark:text-dark-text-muted">Uploading...</span>
                    {/if}
                    <button
                      onclick={() => (uploadCollectionId = null)}
                      class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted transition-colors"
                    >
                      <X size={12} />
                    </button>
                  {:else}
                    <button
                      onclick={() => { uploadCollectionId = c.id; importCollectionId = null; }}
                      class="flex items-center gap-1.5 px-3 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary transition-colors"
                    >
                      <Upload size={12} />
                      Upload Document
                    </button>
                  {/if}
                </div>

                <!-- URL Import -->
                <div class="flex items-center gap-2">
                  {#if importCollectionId === c.id}
                    <form onsubmit={(e) => { e.preventDefault(); handleImportURL(); }} class="flex items-center gap-2">
                      <input
                        type="url"
                        bind:value={importURL}
                        placeholder="https://example.com/page.html"
                        class="w-72 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
                      />
                      <button
                        type="submit"
                        disabled={importing || !importURL.trim()}
                        class="px-3 py-1 text-xs bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors disabled:opacity-50"
                      >
                        {importing ? 'Importing...' : 'Import'}
                      </button>
                      <button
                        type="button"
                        onclick={() => { importCollectionId = null; importURL = ''; }}
                        class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted transition-colors"
                      >
                        <X size={12} />
                      </button>
                    </form>
                  {:else}
                    <button
                      onclick={() => { importCollectionId = c.id; uploadCollectionId = null; }}
                      class="flex items-center gap-1.5 px-3 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary transition-colors"
                    >
                      <Link size={12} />
                      Import URL
                    </button>
                  {/if}
                </div>

                <!-- Details -->
                <div class="ml-auto flex items-center gap-4 text-xs text-gray-400 dark:text-dark-text-muted">
                  <span>Chunk: {c.config.chunk_size}/{c.config.chunk_overlap}</span>
                  {#if c.created_by}
                    <span>by {c.created_by}</span>
                  {/if}
                </div>
              </div>
            </td>
          </tr>
        {/if}
      {/snippet}
    </DataTable>
  {/if}
</div>
