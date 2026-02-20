<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listProviders,
    createProvider,
    updateProvider,
    deleteProvider,
    type ProviderRecord,
    type LLMConfig,
  } from '@/lib/api/providers';
  import { Plus, Pencil, Trash2, X, Save, ChevronDown, BookOpen, Layers, ExternalLink } from 'lucide-svelte';

  storeNavbar.title = 'Providers';

  const PROVIDER_TYPES = ['openai', 'anthropic', 'vertex'] as const;

  // ─── Presets ───

  interface Preset {
    id: string;
    name: string;
    description: string;
    key: string;
    config: Partial<LLMConfig>;
    extraHeaders?: { key: string; value: string }[];
    setupSteps: string[];
    setupLinks?: { label: string; url: string }[];
    notes?: string[];
  }

  const PRESETS: Preset[] = [
    {
      id: 'github',
      name: 'GitHub',
      description: 'Access models via GitHub Copilot or GitHub Models marketplace',
      key: 'github',
      config: {
        type: 'openai',
        base_url: 'https://models.github.ai/inference/chat/completions',
        model: 'openai/gpt-4.1',
        models: ['openai/gpt-4.1', 'openai/gpt-4o', 'openai/gpt-4o-mini', 'openai/o3-mini', 'openai/o4-mini'],
      },
      extraHeaders: [
        { key: 'Accept', value: 'application/vnd.github+json' },
        { key: 'X-GitHub-Api-Version', value: '2022-11-28' },
      ],
      setupSteps: [
        'Go to github.com/settings/tokens?type=beta to create a Fine-grained Personal Access Token',
        'Click "Generate new token" and set a name (e.g., "at-gateway")',
        'Set an expiration period (recommended: 90 days)',
        'Under "Account permissions", enable "Models: Read" (for GitHub Models) or "GitHub Copilot: Read" (for Copilot)',
        'Click "Generate token" and copy the token (starts with github_pat_)',
        'Paste the token in the API Key field below',
      ],
      setupLinks: [
        { label: 'Create PAT', url: 'https://github.com/settings/tokens?type=beta' },
        { label: 'Model Catalog', url: 'https://github.com/marketplace/models' },
        { label: 'Copilot Plans', url: 'https://github.com/features/copilot/plans' },
      ],
      notes: [
        'GitHub Models endpoint: models.github.ai/inference (default)',
        'GitHub Copilot endpoint: api.githubcopilot.com (change Base URL if using Copilot)',
        'Token must be a Fine-grained PAT (classic tokens do not work)',
        'Rate limits: Free tier ~15 req/min, 150 req/day for standard models',
        'Some premium models require a Copilot Pro subscription',
        'Model names include the vendor prefix (e.g., openai/gpt-4.1)',
      ],
    },
    {
      id: 'openai',
      name: 'OpenAI',
      description: 'GPT-4o, GPT-4.1, o3-mini direct from OpenAI',
      key: 'openai',
      config: {
        type: 'openai',
        model: 'gpt-4o',
        models: ['gpt-4o', 'gpt-4o-mini', 'gpt-4.1', 'gpt-4.1-mini', 'gpt-4.1-nano', 'o3-mini'],
      },
      setupSteps: [
        'Go to platform.openai.com and sign in (or create an account)',
        'Navigate to API Keys in the left sidebar',
        'Click "Create new secret key", give it a name, and copy the key',
        'Paste the key (starts with sk-) in the API Key field below',
      ],
      setupLinks: [
        { label: 'API Keys', url: 'https://platform.openai.com/api-keys' },
        { label: 'Models', url: 'https://platform.openai.com/docs/models' },
        { label: 'Pricing', url: 'https://openai.com/api/pricing/' },
      ],
      notes: [
        'Requires a paid OpenAI API account (separate from ChatGPT Plus subscription)',
        'Base URL is auto-configured - leave the Base URL field empty',
      ],
    },
    {
      id: 'anthropic',
      name: 'Anthropic',
      description: 'Claude Sonnet 4, Haiku 4.5, Opus 4',
      key: 'anthropic',
      config: {
        type: 'anthropic',
        model: 'claude-sonnet-4-20250514',
        models: ['claude-sonnet-4-20250514', 'claude-haiku-4-5', 'claude-opus-4-20250514'],
      },
      setupSteps: [
        'Go to console.anthropic.com and sign in (or create an account)',
        'Navigate to Settings > API Keys in the left sidebar',
        'Click "Create Key", name it, and copy the key',
        'Paste the key (starts with sk-ant-) in the API Key field below',
      ],
      setupLinks: [
        { label: 'API Keys', url: 'https://console.anthropic.com/settings/keys' },
        { label: 'Models', url: 'https://docs.anthropic.com/en/docs/about-claude/models' },
        { label: 'Pricing', url: 'https://www.anthropic.com/pricing#anthropic-api' },
      ],
      notes: [
        'Requires a paid Anthropic API account',
        'Base URL is auto-configured - leave the Base URL field empty',
      ],
    },
    {
      id: 'vertex',
      name: 'Vertex AI',
      description: 'Google Gemini models via Google Cloud Platform',
      key: 'vertex',
      config: {
        type: 'vertex',
        model: 'gemini-2.5-flash',
        models: ['gemini-2.5-flash', 'gemini-2.5-pro', 'gemini-2.0-flash'],
      },
      setupSteps: [
        'Prerequisites: A Google Cloud project with billing enabled and Vertex AI API enabled',
        'Enable the Vertex AI API at console.cloud.google.com/apis/library/aiplatform.googleapis.com',
        'Install the Google Cloud CLI (gcloud) from cloud.google.com/sdk/docs/install',
        'Run: gcloud auth application-default login',
        'A browser window opens - sign in with your Google Cloud account and grant access',
        'This creates a credentials file at ~/.config/gcloud/application_default_credentials.json',
        'Set the Base URL below using your GCP project ID and preferred region',
        'Leave the API Key field empty - authentication is handled automatically via ADC',
      ],
      setupLinks: [
        { label: 'Install gcloud', url: 'https://cloud.google.com/sdk/docs/install' },
        { label: 'Enable Vertex AI', url: 'https://console.cloud.google.com/apis/library/aiplatform.googleapis.com' },
        { label: 'Vertex AI Docs', url: 'https://cloud.google.com/vertex-ai/generative-ai/docs/multimodal/call-gemini-using-openai-library' },
        { label: 'Pricing', url: 'https://cloud.google.com/vertex-ai/generative-ai/pricing' },
      ],
      notes: [
        'No API key needed - uses Google Application Default Credentials (ADC)',
        'ADC tokens are automatically refreshed by the vertex provider',
        'Base URL format (replace the two placeholders):',
        '  https://{LOCATION}-aiplatform.googleapis.com/v1/projects/{PROJECT_ID}/locations/{LOCATION}/endpoints/openapi/chat/completions',
        'Common locations: us-central1, europe-west4, asia-northeast1',
        'Find your project ID: gcloud config get-value project',
        'If running in GKE/Cloud Run, ADC uses the service account automatically',
      ],
    },
    {
      id: 'groq',
      name: 'Groq',
      description: 'Ultra-fast inference for Llama, Mixtral, and more',
      key: 'groq',
      config: {
        type: 'openai',
        base_url: 'https://api.groq.com/openai/v1/chat/completions',
        model: 'llama-3.3-70b-versatile',
        models: ['llama-3.3-70b-versatile', 'llama-3.1-8b-instant', 'mixtral-8x7b-32768'],
      },
      setupSteps: [
        'Go to console.groq.com and sign in (or create a free account)',
        'Navigate to API Keys in the left sidebar',
        'Click "Create API Key", name it, and copy the key',
        'Paste the key (starts with gsk_) in the API Key field below',
      ],
      setupLinks: [
        { label: 'API Keys', url: 'https://console.groq.com/keys' },
        { label: 'Models', url: 'https://console.groq.com/docs/models' },
      ],
      notes: [
        'Groq has a generous free tier for experimentation',
        'Known for extremely fast inference speeds (LPU hardware)',
      ],
    },
    {
      id: 'ollama',
      name: 'Ollama',
      description: 'Run models locally on your machine - completely free',
      key: 'ollama',
      config: {
        type: 'openai',
        base_url: 'http://localhost:11434/v1/chat/completions',
        model: 'llama3.2',
      },
      setupSteps: [
        'Install Ollama from ollama.com/download',
        'Run: ollama pull llama3.2 (or any model from the library)',
        'Ollama starts automatically after install and listens on port 11434',
        'No API key is needed - leave the API Key field empty',
      ],
      setupLinks: [
        { label: 'Install Ollama', url: 'https://ollama.com/download' },
        { label: 'Model Library', url: 'https://ollama.com/library' },
      ],
      notes: [
        'Completely free and private - runs entirely on your machine',
        'No account or API key needed',
        'Default port is 11434 - change the Base URL if you use a different port',
        'If Ollama is running on a different machine, replace localhost with the IP/hostname',
      ],
    },
    {
      id: 'deepseek',
      name: 'DeepSeek',
      description: 'DeepSeek-V3 and DeepSeek-R1 reasoning model',
      key: 'deepseek',
      config: {
        type: 'openai',
        base_url: 'https://api.deepseek.com/chat/completions',
        model: 'deepseek-chat',
        models: ['deepseek-chat', 'deepseek-reasoner'],
      },
      setupSteps: [
        'Go to platform.deepseek.com and sign in',
        'Navigate to API Keys',
        'Create a new API key and copy it',
        'Paste the key in the API Key field below',
      ],
      setupLinks: [
        { label: 'API Keys', url: 'https://platform.deepseek.com/api_keys' },
        { label: 'Docs', url: 'https://api-docs.deepseek.com/' },
      ],
      notes: [
        'deepseek-chat is the general purpose model (DeepSeek-V3)',
        'deepseek-reasoner is the reasoning model (DeepSeek-R1)',
      ],
    },
  ];

  // ─── State ───

  let providers = $state<ProviderRecord[]>([]);
  let loading = $state(true);
  let showForm = $state(false);
  let showPresets = $state(false);
  let editingKey = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);
  let activePreset = $state<Preset | null>(null);

  // Form fields
  let formKey = $state('');
  let formType = $state<string>('openai');
  let formApiKey = $state('');
  let formBaseUrl = $state('');
  let formModel = $state('');
  let formModels = $state('');
  let formExtraHeaders = $state<{ key: string; value: string }[]>([]);

  // ─── Load ───

  async function load() {
    loading = true;
    try {
      providers = await listProviders();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load providers', 'alert');
    } finally {
      loading = false;
    }
  }

  load();

  // ─── Form ───

  function resetForm() {
    formKey = '';
    formType = 'openai';
    formApiKey = '';
    formBaseUrl = '';
    formModel = '';
    formModels = '';
    formExtraHeaders = [];
    editingKey = null;
    activePreset = null;
    showForm = false;
    showPresets = false;
  }

  function openCreate() {
    resetForm();
    activePreset = null;
    showForm = true;
  }

  function openPresets() {
    resetForm();
    showPresets = true;
  }

  function applyPreset(preset: Preset) {
    resetForm();
    activePreset = preset;
    formKey = preset.key;
    formType = preset.config.type || 'openai';
    formApiKey = '';
    formBaseUrl = preset.config.base_url || '';
    formModel = preset.config.model || '';
    formModels = (preset.config.models || []).join(', ');
    formExtraHeaders = preset.extraHeaders ? [...preset.extraHeaders] : [];
    showPresets = false;
    showForm = true;
  }

  function openEdit(rec: ProviderRecord) {
    resetForm();
    editingKey = rec.key;
    formKey = rec.key;
    formType = rec.config.type;
    formApiKey = rec.config.api_key || '';
    formBaseUrl = rec.config.base_url || '';
    formModel = rec.config.model;
    formModels = (rec.config.models || []).join(', ');
    formExtraHeaders = Object.entries(rec.config.extra_headers || {}).map(
      ([key, value]) => ({ key, value })
    );
    showForm = true;
  }

  function buildConfig(): LLMConfig {
    const cfg: LLMConfig = {
      type: formType,
      model: formModel,
    };
    if (formApiKey) cfg.api_key = formApiKey;
    if (formBaseUrl) cfg.base_url = formBaseUrl;

    const models = formModels
      .split(',')
      .map((m) => m.trim())
      .filter(Boolean);
    if (models.length > 0) cfg.models = models;

    const headers: Record<string, string> = {};
    for (const h of formExtraHeaders) {
      if (h.key && h.value) headers[h.key] = h.value;
    }
    if (Object.keys(headers).length > 0) cfg.extra_headers = headers;

    return cfg;
  }

  async function handleSubmit() {
    if (!formKey || !formType || !formModel) {
      addToast('Key, type and model are required', 'warn');
      return;
    }

    try {
      const cfg = buildConfig();
      if (editingKey) {
        await updateProvider(editingKey, cfg);
        addToast(`Provider "${editingKey}" updated`);
      } else {
        await createProvider(formKey, cfg);
        addToast(`Provider "${formKey}" created`);
      }
      resetForm();
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save provider', 'alert');
    }
  }

  async function handleDelete(key: string) {
    try {
      await deleteProvider(key);
      addToast(`Provider "${key}" deleted`);
      deleteConfirm = null;
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete provider', 'alert');
    }
  }

  function addHeader() {
    formExtraHeaders = [...formExtraHeaders, { key: '', value: '' }];
  }

  function removeHeader(index: number) {
    formExtraHeaders = formExtraHeaders.filter((_, i) => i !== index);
  }
</script>

<div class="p-6 max-w-5xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-6">
    <div>
      <h1 class="text-lg font-semibold text-gray-900">Providers</h1>
      <p class="text-sm text-gray-500 mt-0.5">Configure LLM backends for the gateway</p>
    </div>
    <div class="flex gap-2">
      <button
        onclick={openPresets}
        class="flex items-center gap-1.5 px-3 py-1.5 bg-gray-900 text-white text-sm hover:bg-gray-800 transition-colors"
      >
        <Layers size={14} />
        From Template
      </button>
      <button
        onclick={openCreate}
        class="flex items-center gap-1.5 px-3 py-1.5 text-sm border border-gray-300 hover:bg-gray-50 text-gray-700 transition-colors"
      >
        <Plus size={14} />
        Custom
      </button>
    </div>
  </div>

  <!-- Preset Picker -->
  {#if showPresets}
    <div class="border border-gray-200 mb-6 bg-white shadow-sm overflow-hidden">
      <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200">
        <span class="text-sm font-medium text-gray-900">Choose a Template</span>
        <button onclick={resetForm} class="p-1 hover:bg-gray-100 text-gray-400 hover:text-gray-600 transition-colors">
          <X size={14} />
        </button>
      </div>
      <div class="p-4 grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-3">
        {#each PRESETS as preset}
          <button
            onclick={() => applyPreset(preset)}
            class="text-left border border-gray-200 p-3 hover:border-gray-400 hover:shadow-sm transition-all group"
          >
            <div class="font-medium text-sm text-gray-900 group-hover:text-gray-900">{preset.name}</div>
            <div class="text-xs text-gray-500 mt-1 leading-relaxed">{preset.description}</div>
            <div class="mt-2.5">
              <span class="text-xs px-1.5 py-0.5 bg-gray-100 text-gray-600 font-mono">{preset.config.type}</span>
            </div>
          </button>
        {/each}
      </div>
    </div>
  {/if}

  <!-- Form -->
  {#if showForm}
    <div class="border border-gray-200 mb-6 bg-white shadow-sm overflow-hidden">
      <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 bg-gray-50">
        <span class="text-sm font-medium text-gray-900">
          {#if editingKey}
            Edit: {editingKey}
          {:else if activePreset}
            New Provider: {activePreset.name}
          {:else}
            New Provider (Custom)
          {/if}
        </span>
        <button onclick={resetForm} class="p-1 hover:bg-gray-200 text-gray-400 hover:text-gray-600 transition-colors">
          <X size={14} />
        </button>
      </div>

      <!-- Setup Guide (shown when using a preset) -->
      {#if activePreset}
        <div class="border-b border-gray-200 bg-blue-50/50 px-4 py-4">
          <div class="flex items-start gap-2.5">
            <BookOpen size={16} class="text-blue-600 mt-0.5 shrink-0" />
            <div class="flex-1 min-w-0">
              <div class="text-sm font-medium text-gray-900 mb-2">Setup Guide</div>
              <ol class="text-xs text-gray-700 space-y-1.5 list-decimal list-inside leading-relaxed">
                {#each activePreset.setupSteps as step}
                  <li>{step}</li>
                {/each}
              </ol>

              {#if activePreset.setupLinks && activePreset.setupLinks.length > 0}
                <div class="flex flex-wrap gap-2 mt-3">
                  {#each activePreset.setupLinks as link}
                    <a
                      href={link.url}
                      target="_blank"
                      rel="noopener noreferrer"
                      class="inline-flex items-center gap-1 text-xs px-2 py-1 bg-white border border-gray-200 text-gray-600 hover:border-gray-400 hover:text-gray-900 transition-colors"
                    >
                      {link.label}
                      <ExternalLink size={10} />
                    </a>
                  {/each}
                </div>
              {/if}

              {#if activePreset.notes && activePreset.notes.length > 0}
                <div class="mt-3 pt-3 border-t border-blue-100">
                  {#each activePreset.notes as note}
                    <div class="text-xs text-gray-500 mt-1 leading-relaxed">{note}</div>
                  {/each}
                </div>
              {/if}
            </div>
          </div>
        </div>
      {/if}

      <form onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="p-4 space-y-4">
        <!-- Key -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-key" class="text-sm font-medium text-gray-700">Key</label>
          <input
            id="form-key"
            type="text"
            bind:value={formKey}
            disabled={!!editingKey}
            placeholder="e.g., anthropic, groq, ollama"
            class="col-span-3 border border-gray-300 px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 disabled:bg-gray-50 disabled:text-gray-500 transition-colors"
          />
        </div>

        <!-- Type -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-type" class="text-sm font-medium text-gray-700">Type</label>
          <div class="col-span-3 relative">
            <select
              id="form-type"
              bind:value={formType}
              class="w-full border border-gray-300 px-3 py-1.5 text-sm appearance-none bg-white pr-8 focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
            >
              {#each PROVIDER_TYPES as t}
                <option value={t}>{t}</option>
              {/each}
            </select>
            <ChevronDown size={14} class="absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none text-gray-400" />
          </div>
        </div>

        <!-- API Key -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-apikey" class="text-sm font-medium text-gray-700">API Key</label>
          <input
            id="form-apikey"
            type="password"
            bind:value={formApiKey}
            placeholder={activePreset?.id === 'vertex' ? '(not needed - uses ADC)' : activePreset?.id === 'ollama' ? '(not needed)' : activePreset?.id === 'github' ? 'github_pat_...' : 'sk-...'}
            class="col-span-3 border border-gray-300 px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
          />
        </div>

        <!-- Base URL -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-baseurl" class="text-sm font-medium text-gray-700">Base URL</label>
          <input
            id="form-baseurl"
            type="text"
            bind:value={formBaseUrl}
            placeholder={activePreset?.id === 'vertex'
              ? 'https://{LOCATION}-aiplatform.googleapis.com/v1/projects/{PROJECT}/locations/{LOCATION}/endpoints/openapi/chat/completions'
              : 'https://api.example.com/v1/chat/completions'}
            class="col-span-3 border border-gray-300 px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
          />
        </div>

        <!-- Model -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-model" class="text-sm font-medium text-gray-700">Default Model</label>
          <input
            id="form-model"
            type="text"
            bind:value={formModel}
            placeholder="e.g., gpt-4o, claude-haiku-4-5"
            class="col-span-3 border border-gray-300 px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
          />
        </div>

        <!-- Models -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-models" class="text-sm font-medium text-gray-700">Models</label>
          <input
            id="form-models"
            type="text"
            bind:value={formModels}
            placeholder="model-a, model-b (comma-separated, optional)"
            class="col-span-3 border border-gray-300 px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
          />
        </div>

        <!-- Extra Headers -->
        <div class="grid grid-cols-4 gap-3">
          <span class="text-sm font-medium text-gray-700 pt-1.5">Extra Headers</span>
          <div class="col-span-3 space-y-2">
            {#each formExtraHeaders as header, i}
              <div class="flex gap-2">
                <input
                  type="text"
                  bind:value={header.key}
                  placeholder="Header-Name"
                  class="flex-1 border border-gray-300 px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
                />
                <input
                  type="text"
                  bind:value={header.value}
                  placeholder="value"
                  class="flex-1 border border-gray-300 px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
                />
                <button
                  type="button"
                  onclick={() => removeHeader(i)}
                  class="p-1.5 border border-gray-300 hover:bg-red-50 hover:border-red-300 hover:text-red-600 text-gray-400 transition-colors"
                >
                  <X size={12} />
                </button>
              </div>
            {/each}
            <button
              type="button"
              onclick={addHeader}
              class="text-sm text-gray-500 hover:text-gray-900 transition-colors"
            >
              + Add header
            </button>
          </div>
        </div>

        <!-- Actions -->
        <div class="flex justify-end gap-2 pt-3 border-t border-gray-100">
          <button
            type="button"
            onclick={resetForm}
            class="px-3 py-1.5 text-sm border border-gray-300 hover:bg-gray-50 text-gray-700 transition-colors"
          >
            Cancel
          </button>
          <button
            type="submit"
            class="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-gray-900 text-white hover:bg-gray-800 transition-colors"
          >
            <Save size={14} />
            {editingKey ? 'Update' : 'Create'}
          </button>
        </div>
      </form>
    </div>
  {/if}

  <!-- Provider list -->
  {#if loading}
    <div class="text-center py-12 text-gray-400 text-sm">Loading...</div>
  {:else if providers.length === 0 && !showForm && !showPresets}
    <div class="border border-dashed border-gray-300 py-12 bg-white">
      <div class="text-center">
        <div class="text-gray-400 mb-1">No providers configured</div>
        <p class="text-sm text-gray-400 mb-4">Add a provider to start routing requests</p>
        <div class="flex justify-center gap-2">
          <button
            onclick={openPresets}
            class="flex items-center gap-1.5 px-3 py-1.5 bg-gray-900 text-white text-sm hover:bg-gray-800 transition-colors"
          >
            <Layers size={14} />
            From Template
          </button>
          <button
            onclick={openCreate}
            class="flex items-center gap-1.5 px-3 py-1.5 text-sm border border-gray-300 hover:bg-gray-50 text-gray-700 transition-colors"
          >
            <Plus size={14} />
            Custom
          </button>
        </div>
      </div>
    </div>
  {:else if providers.length > 0}
    <div class="border border-gray-200 bg-white shadow-sm overflow-hidden">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-gray-200 bg-gray-50">
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 text-xs uppercase tracking-wider">Key</th>
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 text-xs uppercase tracking-wider">Type</th>
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 text-xs uppercase tracking-wider">Model</th>
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 text-xs uppercase tracking-wider">Models</th>
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 text-xs uppercase tracking-wider">Base URL</th>
            <th class="text-right px-4 py-2.5 font-medium text-gray-500 text-xs uppercase tracking-wider w-28"></th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100">
          {#each providers as rec}
            <tr class="hover:bg-gray-50/50 transition-colors">
              <td class="px-4 py-2.5 font-mono font-medium text-gray-900">{rec.key}</td>
              <td class="px-4 py-2.5">
                <span class="px-2 py-0.5 text-xs bg-gray-100 text-gray-600 font-mono">{rec.config.type}</span>
              </td>
              <td class="px-4 py-2.5 font-mono text-xs text-gray-600">{rec.config.model}</td>
              <td class="px-4 py-2.5 text-xs text-gray-500">
                {(rec.config.models || []).length > 0 ? (rec.config.models || []).join(', ') : '-'}
              </td>
              <td class="px-4 py-2.5 text-xs text-gray-500 truncate max-w-48" title={rec.config.base_url || ''}>
                {rec.config.base_url || 'default'}
              </td>
              <td class="px-4 py-2.5 text-right">
                <div class="flex justify-end gap-1">
                  <button
                    onclick={() => openEdit(rec)}
                    class="p-1.5 hover:bg-gray-100 text-gray-400 hover:text-gray-700 transition-colors"
                    title="Edit"
                  >
                    <Pencil size={14} />
                  </button>
                  {#if deleteConfirm === rec.key}
                    <button
                      onclick={() => handleDelete(rec.key)}
                      class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 transition-colors"
                    >
                      Confirm
                    </button>
                    <button
                      onclick={() => (deleteConfirm = null)}
                      class="px-2 py-1 text-xs border border-gray-300 hover:bg-gray-50 transition-colors"
                    >
                      Cancel
                    </button>
                  {:else}
                    <button
                      onclick={() => (deleteConfirm = rec.key)}
                      class="p-1.5 hover:bg-red-50 text-gray-400 hover:text-red-600 transition-colors"
                      title="Delete"
                    >
                      <Trash2 size={14} />
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
