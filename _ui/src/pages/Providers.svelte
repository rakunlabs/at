<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listProviders,
    createProvider,
    updateProvider,
    deleteProvider,
    discoverModels,
    startDeviceAuth,
    getDeviceAuthStatus,
    startClaudeAuth,
    submitClaudeAuthCode,
    submitClaudeAuthToken,
    syncClaudeAuthFromCLI,
    type ProviderRecord,
    type LLMConfig,
  } from '@/lib/api/providers';
  import { Plus, Pencil, Trash2, X, Save, ChevronDown, BookOpen, Layers, ExternalLink, RefreshCw, LogIn, FileCode, Copy, Check, KeyRound, DownloadCloud } from 'lucide-svelte';
  import { generateYamlSnippet, generateJsonSnippet } from '@/lib/helper/config-snippet';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';

  storeNavbar.title = 'Providers';

  const PROVIDER_TYPES = ['openai', 'anthropic', 'vertex', 'gemini', 'minimax'] as const;

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
      id: 'github-models',
      name: 'GitHub Models',
      description: 'Access models via the GitHub Models marketplace',
      key: 'github-models',
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
        'Under "Account permissions", enable "Models: Read"',
        'Click "Generate token" and copy the token (starts with github_pat_)',
        'Paste the token in the API Key field below',
      ],
      setupLinks: [
        { label: 'Create PAT', url: 'https://github.com/settings/tokens?type=beta' },
        { label: 'Model Catalog', url: 'https://github.com/marketplace/models' },
      ],
      notes: [
        'Token must be a Fine-grained PAT (classic tokens do not work)',
        'Rate limits: Free tier ~15 req/min, 150 req/day for standard models',
        'Model names include the vendor prefix (e.g., openai/gpt-4.1)',
      ],
    },
    {
      id: 'github-copilot',
      name: 'GitHub Copilot',
      description: 'Access models via a GitHub Copilot subscription',
      key: 'github-copilot',
      config: {
        type: 'openai',
        auth_type: 'copilot',
        base_url: 'https://api.githubcopilot.com/chat/completions?api-version=2025-04-01',
        model: 'gpt-4.1',
        models: [
          'gpt-4.1',
          'gpt-5-mini',
          'gpt-5.1',
          'gpt-5.1-codex',
          'gpt-5.1-codex-mini',
          'gpt-5.1-codex-max',
          'gpt-5.2',
          'gpt-5.2-codex',
          'gpt-5.3-codex',
          'claude-haiku-4.5',
          'claude-sonnet-4',
          'claude-sonnet-4.5',
          'claude-sonnet-4.6',
          'claude-opus-4.5',
          'claude-opus-4.6',
          'gemini-2.5-pro',
          'gemini-3-flash',
          'gemini-3-pro',
          'gemini-3.1-pro',
          'grok-code-fast-1',
        ],
      },
      extraHeaders: [
        { key: 'Accept', value: 'application/vnd.github+json' },
      ],
      setupSteps: [
        'You need an active GitHub Copilot subscription (Individual, Business, or Enterprise)',
        'Click "Create" to save the provider configuration',
        'Edit the provider and click "Authorize with GitHub"',
        'A code will appear - copy it and open the GitHub link',
        'Enter the code at github.com/login/device and authorize the application',
        'The provider will be ready to use once authorization completes',
      ],
      setupLinks: [
        { label: 'Copilot Plans', url: 'https://github.com/features/copilot/plans' },
      ],
      notes: [
        'Requires an active GitHub Copilot subscription',
        'Authorization is done via your browser - no tokens to copy/paste',
        'The OAuth token is stored securely and refreshed automatically',
        'Some premium models require a Copilot Pro subscription',
        'Model names do NOT include the vendor prefix (e.g., gpt-4.1, not openai/gpt-4.1)',
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
        models: ['claude-sonnet-4-20250514', 'claude-sonnet-4-5-20250514', 'claude-haiku-4-5-20250620', 'claude-opus-4-20250514'],
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
      id: 'anthropic-claude-code',
      name: 'Anthropic (Claude Pro / Max)',
      description: 'Use your Claude Pro or Max subscription via OAuth — no API account needed',
      key: 'anthropic-claude-code',
      config: {
        type: 'anthropic',
        auth_type: 'claude-code',
        model: 'claude-sonnet-4-5-20250929',
        models: [
          'claude-haiku-4-5',
          'claude-haiku-4-5-20251001',
          'claude-sonnet-4-5',
          'claude-sonnet-4-5-20250929',
          'claude-sonnet-4-6',
          'claude-opus-4-5',
          'claude-opus-4-5-20251101',
          'claude-opus-4-6',
          'claude-opus-4-7',
        ],
      },
      setupSteps: [
        'You need an active Claude Pro or Max subscription (claude.ai)',
        'Click "Create" to save the provider configuration',
        'Edit the provider and click "Authorize with Claude" (or use "Paste Token" / "Sync from CLI" if you have Claude Code installed)',
        'For browser auth: a link will appear - open it, sign in, copy the code, and paste it back',
        'For CLI sync: click "Sync from CLI" to auto-extract tokens from your Claude Code installation',
      ],
      setupLinks: [
        { label: 'Claude Pricing', url: 'https://claude.com/pricing' },
        { label: 'Subscription Models', url: 'https://docs.anthropic.com/en/docs/about-claude/models' },
      ],
      notes: [
        'Requires an active Claude Pro or Max subscription (NOT a separate Anthropic API account)',
        'IMPORTANT: Use full model IDs with date suffixes (e.g. claude-sonnet-4-5-20250929) for stability — bare aliases (claude-sonnet-4-5) work but resolve to the latest snapshot',
        'OAuth tokens are stored encrypted and refreshed automatically (5 min before expiry)',
        'Uses the Claude Code OAuth client — same credentials Claude Code CLI uses',
        'If you have Claude Code CLI installed, "Sync from CLI" is the easiest auth method',
        'Long context (1M) is NOT enabled by default — exceeding 200k tokens errors with "Extra usage required"',
        'Subject to subscription rate limits (5h windows). Heavy automated use may hit limits faster than interactive Claude Code use.',
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
      id: 'google-ai',
      name: 'Google AI',
      description: 'Google Gemini models with simple API key authentication',
      key: 'google-ai',
      config: {
        type: 'gemini',
        model: 'gemini-2.5-flash',
        models: ['gemini-2.5-flash', 'gemini-2.5-pro', 'gemini-2.0-flash'],
      },
      setupSteps: [
        'Go to aistudio.google.com and sign in with your Google account',
        'Click "Get API key" in the left sidebar',
        'Click "Create API key" and select or create a Google Cloud project',
        'Copy the generated API key (starts with AIza)',
        'Paste the key in the API Key field below',
      ],
      setupLinks: [
        { label: 'Get API Key', url: 'https://aistudio.google.com/apikey' },
        { label: 'Model List', url: 'https://ai.google.dev/gemini-api/docs/models/gemini' },
        { label: 'Pricing', url: 'https://ai.google.dev/pricing' },
      ],
      notes: [
        'Much simpler than Vertex AI - no GCP project setup, billing, or gcloud CLI needed',
        'Just an API key from Google AI Studio is all you need',
        'Uses the native Gemini API (generativelanguage.googleapis.com)',
        'Base URL is auto-configured - leave the Base URL field empty',
        'Free tier available with generous rate limits for experimentation',
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
      id: 'ollama-cloud',
      name: 'Ollama Cloud',
      description: 'Run large open-weight models on Ollama\'s hosted cloud (gpt-oss, kimi-k2, qwen3-coder)',
      key: 'ollama-cloud',
      config: {
        type: 'openai',
        base_url: 'https://ollama.com/v1/chat/completions',
        model: 'gpt-oss:120b',
        models: [
          'gpt-oss:120b',
          'gpt-oss:20b',
          'kimi-k2:1t',
          'qwen3-coder:480b',
          'deepseek-v3.1:671b',
        ],
      },
      setupSteps: [
        'Sign up at ollama.com (free) or run "ollama signin" in the CLI',
        'Go to ollama.com/settings/keys and create an API key',
        'Paste the key in the API Key field below',
      ],
      setupLinks: [
        { label: 'Sign Up', url: 'https://ollama.com/signup' },
        { label: 'API Keys', url: 'https://ollama.com/settings/keys' },
        { label: 'Cloud Models', url: 'https://ollama.com/search?c=cloud' },
        { label: 'Cloud Docs', url: 'https://docs.ollama.com/cloud' },
      ],
      notes: [
        'OpenAI-compatible endpoint at ollama.com/v1 — uses the standard openai provider type',
        'Model names use the Ollama tag format (e.g. gpt-oss:120b, not gpt-oss-120b)',
        'Lets you run large open-weight models without local GPU',
        'For local Ollama (free, on-machine), use the separate "Ollama" preset',
        'Requires an Ollama account with cloud access enabled',
      ],
    },
    {
      id: 'minimax',
      name: 'MiniMax',
      description: 'MiniMax M2.7 with image generation and text-to-speech',
      key: 'minimax',
      config: {
        type: 'minimax',
        model: 'MiniMax-M2.7',
        models: ['MiniMax-M2.7', 'MiniMax-M2.7-highspeed', 'MiniMax-M2.5', 'MiniMax-M2.5-highspeed'],
      },
      setupSteps: [
        'Go to platform.minimax.io and sign in (or create an account)',
        'Navigate to the API Keys page in the console',
        'Create a new API key and copy it',
        'Paste the key in the API Key field below',
      ],
      setupLinks: [
        { label: 'Console', url: 'https://platform.minimax.io/user-center/basic-information/interface-key' },
        { label: 'Docs', url: 'https://platform.minimaxi.com/document/introduction' },
        { label: 'Pricing', url: 'https://platform.minimaxi.com/document/Price%20Description' },
      ],
      notes: [
        'Uses the dedicated minimax provider type for full feature support',
        'Chat is OpenAI-compatible (204k context window)',
        'Supports image generation (image-01 model) in workflow nodes',
        'Supports text-to-speech (speech-2.8-hd, 300+ voices) in workflow nodes',
        'Base URL is auto-configured - leave the Base URL field empty',
        'Note: Temperature must be in the range (0, 1] — sending 0 will error',
      ],
    },
    {
      id: 'opencode-go',
      name: 'OpenCode Go',
      description: 'Curated open coding models via OpenCode Go subscription (GLM, Kimi, MiMo, Qwen)',
      key: 'opencode-go',
      config: {
        type: 'openai',
        base_url: 'https://opencode.ai/zen/go/v1/chat/completions',
        model: 'qwen3.6-plus',
        models: [
          'qwen3.6-plus',
          'qwen3.5-plus',
          'kimi-k2.6',
          'kimi-k2.5',
          'glm-5.1',
          'glm-5',
          'mimo-v2-pro',
          'mimo-v2-omni',
        ],
      },
      setupSteps: [
        'Sign in to OpenCode Zen at opencode.ai/auth',
        'Subscribe to the Go plan ($5 first month, then $10/month)',
        'Copy your OpenCode Zen API key from the console',
        'Paste the key in the API Key field below',
      ],
      setupLinks: [
        { label: 'Sign In / Subscribe', url: 'https://opencode.ai/auth' },
        { label: 'Go Docs', url: 'https://opencode.ai/docs/go/' },
        { label: 'Pricing', url: 'https://opencode.ai/docs/go/#usage-limits' },
      ],
      notes: [
        'OpenAI-compatible endpoint — uses the standard openai provider type',
        'Only one member per workspace can subscribe to OpenCode Go',
        'Dollar-based limits: $12 per 5h, $30/week, $60/month',
        'Cheapest models (Qwen3.5 Plus) get ~10,200 requests per 5h; priciest (GLM-5.1) get ~880',
        'Model IDs are bare names (no prefix) in the API — e.g. `kimi-k2.6`',
        'For MiniMax M2.5 / M2.7 on Go use the separate "OpenCode Go (MiniMax)" preset — same subscription, different endpoint',
        'Zero-retention policy — your data is not used for training',
      ],
    },
    {
      id: 'opencode-go-minimax',
      name: 'OpenCode Go (MiniMax)',
      description: 'MiniMax M2.5 / M2.7 via your OpenCode Go subscription',
      key: 'opencode-go-minimax',
      config: {
        type: 'anthropic',
        base_url: 'https://opencode.ai/zen/go',
        model: 'minimax-m2.7',
        models: [
          'minimax-m2.7',
          'minimax-m2.5',
        ],
      },
      setupSteps: [
        'Use the same OpenCode Zen / Go subscription as the main OpenCode Go preset',
        'Copy your OpenCode Zen API key from the console',
        'Paste the key in the API Key field below',
      ],
      setupLinks: [
        { label: 'Sign In / Subscribe', url: 'https://opencode.ai/auth' },
        { label: 'Go Endpoints', url: 'https://opencode.ai/docs/go/#endpoints' },
      ],
      notes: [
        'Uses the anthropic provider type because MiniMax on Go is served in Anthropic Messages format at /v1/messages',
        'Same API key as the main OpenCode Go preset — no separate subscription needed',
        'MiniMax M2.5 has the biggest request budget on Go (31,800/month); M2.7 is stronger but pricier (17,000/month)',
        'If your Go subscription is already authed in the main preset, you can reuse the same API key here',
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
    {
      id: 'xai',
      name: 'xAI (Grok)',
      description: 'Grok models from xAI — fast, competitive on coding and reasoning',
      key: 'xai',
      config: {
        type: 'openai',
        base_url: 'https://api.x.ai/v1/chat/completions',
        model: 'grok-4',
        models: ['grok-4', 'grok-4-fast', 'grok-4-fast-reasoning', 'grok-3', 'grok-3-mini', 'grok-code-fast-1'],
      },
      setupSteps: [
        'Go to console.x.ai and sign in (or create an account)',
        'Navigate to API Keys',
        'Click "Create API Key", name it, and copy the key',
        'Paste the key (starts with xai-) in the API Key field below',
      ],
      setupLinks: [
        { label: 'Console', url: 'https://console.x.ai/' },
        { label: 'API Docs', url: 'https://docs.x.ai/' },
        { label: 'Pricing', url: 'https://docs.x.ai/docs/models' },
      ],
      notes: [
        'OpenAI-compatible — uses the standard openai provider type',
        'grok-code-fast-1 is the cheapest tier and well-suited as a fallback for coding tasks',
        'grok-4-fast variants are tuned for low latency; grok-4 has a larger reasoning budget',
      ],
    },
    {
      id: 'mistral',
      name: 'Mistral AI',
      description: 'EU-hosted Mistral models — Large, Codestral, Pixtral',
      key: 'mistral',
      config: {
        type: 'openai',
        base_url: 'https://api.mistral.ai/v1/chat/completions',
        model: 'mistral-large-latest',
        models: [
          'mistral-large-latest',
          'mistral-medium-latest',
          'mistral-small-latest',
          'codestral-latest',
          'pixtral-large-latest',
          'ministral-8b-latest',
          'ministral-3b-latest',
        ],
      },
      setupSteps: [
        'Go to console.mistral.ai and sign in (or create an account)',
        'Navigate to API Keys',
        'Click "Create new key", name it, and copy the key',
        'Paste the key in the API Key field below',
      ],
      setupLinks: [
        { label: 'API Keys', url: 'https://console.mistral.ai/api-keys/' },
        { label: 'Models', url: 'https://docs.mistral.ai/getting-started/models/models_overview/' },
        { label: 'Pricing', url: 'https://mistral.ai/technology/#pricing' },
      ],
      notes: [
        'OpenAI-compatible endpoint',
        'EU-hosted — useful when data residency in the EU is a requirement',
        'codestral-latest is tuned specifically for code completion / fill-in-the-middle',
        'pixtral-large supports vision input',
      ],
    },
    {
      id: 'qwen-dashscope',
      name: 'Qwen (Alibaba DashScope)',
      description: 'Qwen3 models via Alibaba DashScope international endpoint',
      key: 'qwen-dashscope',
      config: {
        type: 'openai',
        base_url: 'https://dashscope-intl.aliyuncs.com/compatible-mode/v1/chat/completions',
        model: 'qwen3-max',
        models: [
          'qwen3-max',
          'qwen3-plus',
          'qwen3-flash',
          'qwen3-coder-plus',
          'qwen3-coder-flash',
          'qwen3-vl-plus',
        ],
      },
      setupSteps: [
        'Sign up at dashscope.console.aliyun.com (international) — international endpoint, no Chinese phone needed',
        'Navigate to "API Key Management"',
        'Click "Create API Key" and copy the key (starts with sk-)',
        'Paste the key in the API Key field below',
      ],
      setupLinks: [
        { label: 'Console (Intl)', url: 'https://dashscope.console.aliyun.com/' },
        { label: 'API Docs', url: 'https://www.alibabacloud.com/help/en/model-studio/developer-reference/use-qwen-by-calling-api' },
        { label: 'Pricing', url: 'https://www.alibabacloud.com/help/en/model-studio/billing-for-model-studio' },
      ],
      notes: [
        'OpenAI-compatible endpoint via DashScope "compatible-mode"',
        'Use the international endpoint (dashscope-intl.aliyuncs.com) unless you have a Chinese mainland account',
        'qwen3-coder-* models are tuned for code; qwen3-max is the largest general model',
        'qwen3-vl-plus supports vision',
        'Domestic endpoint is dashscope.aliyuncs.com (requires Chinese mainland account)',
      ],
    },
    {
      id: 'kimi-moonshot',
      name: 'Kimi (Moonshot AI)',
      description: 'Kimi K2 — long context (256k+), strong on coding',
      key: 'kimi-moonshot',
      config: {
        type: 'openai',
        base_url: 'https://api.moonshot.ai/v1/chat/completions',
        model: 'kimi-k2-0905-preview',
        models: [
          'kimi-k2-0905-preview',
          'kimi-k2-turbo-preview',
          'moonshot-v1-128k',
          'moonshot-v1-32k',
          'moonshot-v1-8k',
        ],
      },
      setupSteps: [
        'Go to platform.moonshot.ai and sign in (or create an account)',
        'Navigate to API Keys',
        'Click "Create API Key", name it, and copy the key',
        'Paste the key (starts with sk-) in the API Key field below',
      ],
      setupLinks: [
        { label: 'Platform (Intl)', url: 'https://platform.moonshot.ai/' },
        { label: 'Docs', url: 'https://platform.moonshot.ai/docs/' },
      ],
      notes: [
        'OpenAI-compatible endpoint',
        'Use the international endpoint (api.moonshot.ai) unless you have a Chinese mainland account',
        'kimi-k2-* models support 256k+ context and are competitive on coding benchmarks',
        'Domestic endpoint is api.moonshot.cn (requires Chinese mainland account)',
      ],
    },
    {
      id: 'zai-glm',
      name: 'Z.ai (Zhipu / GLM)',
      description: 'GLM-5 family — coding and general models from Zhipu AI',
      key: 'zai-glm',
      config: {
        type: 'openai',
        base_url: 'https://api.z.ai/api/paas/v4/chat/completions',
        model: 'glm-5.1',
        models: [
          'glm-5.1',
          'glm-5.1-air',
          'glm-5',
          'glm-5-air',
          'glm-4-plus',
          'glm-4-flash',
        ],
      },
      setupSteps: [
        'Go to z.ai and sign in (or create an account)',
        'Navigate to the API Keys section',
        'Create a new API Key and copy it',
        'Paste the key in the API Key field below',
      ],
      setupLinks: [
        { label: 'Z.ai Platform', url: 'https://z.ai/' },
        { label: 'API Docs', url: 'https://docs.z.ai/' },
        { label: 'GLM Coding Plan', url: 'https://docs.z.ai/devpack/overview' },
      ],
      notes: [
        'OpenAI-compatible endpoint via the international Z.ai gateway',
        'glm-5.1 is the flagship; glm-5.1-air is the cheap fast tier',
        'glm-4-flash has a generous free tier on the international endpoint',
        'For the GLM Coding Plan subscription, use the separate "Z.ai (GLM Coding Plan)" preset — same API key, different endpoint',
        'Domestic endpoint is open.bigmodel.cn (requires Chinese mainland account)',
      ],
    },
    {
      id: 'zai-glm-coding',
      name: 'Z.ai (GLM Coding Plan)',
      description: 'GLM Coding Plan subscription via Z.ai\'s Anthropic-compatible endpoint',
      key: 'zai-glm-coding',
      config: {
        type: 'anthropic',
        base_url: 'https://api.z.ai/api/anthropic',
        model: 'glm-5.1',
        models: [
          'glm-5.1',
          'glm-5-turbo',
          'glm-4.7',
          'glm-4.5-air',
        ],
      },
      setupSteps: [
        'Subscribe to the GLM Coding Plan at z.ai/subscribe (Lite ~$18/mo, Pro, or Max)',
        'Get an API Key from z.ai/manage-apikey/apikey-list',
        'Paste the key in the API Key field below — same key used by the standard Z.ai preset',
        'Use the Anthropic provider type (this preset auto-selects it)',
      ],
      setupLinks: [
        { label: 'Subscribe', url: 'https://z.ai/subscribe' },
        { label: 'API Keys', url: 'https://z.ai/manage-apikey/apikey-list' },
        { label: 'Coding Plan Docs', url: 'https://docs.z.ai/devpack/overview' },
        { label: 'Usage Statistics', url: 'https://z.ai/manage-apikey/subscription' },
      ],
      notes: [
        'Uses the anthropic provider type — Z.ai exposes the Coding Plan via /api/anthropic/v1/messages',
        'Coding Plan is metered separately from pay-per-token API usage (5h windows + weekly cap)',
        'Lite ~80 prompts/5h, Pro ~400, Max ~1600 (one prompt ≈ 15-20 model invocations)',
        'GLM-5.1 / GLM-5-Turbo consume 3x quota during peak hours (14:00-18:00 UTC+8); use GLM-4.7 for routine work',
        'Same API key as the standard "Z.ai (Zhipu / GLM)" preset — no separate key needed',
        'Z.ai\'s ToS restricts Coding Plan use to officially supported tools — automated routing through AT may be flagged',
      ],
    },
    {
      id: 'openrouter',
      name: 'OpenRouter',
      description: '500+ models from every major provider through one key',
      key: 'openrouter',
      config: {
        type: 'openai',
        base_url: 'https://openrouter.ai/api/v1/chat/completions',
        model: 'openai/gpt-4o',
        models: [
          'openai/gpt-4o',
          'openai/gpt-4o-mini',
          'anthropic/claude-sonnet-4',
          'anthropic/claude-haiku-4',
          'google/gemini-2.5-pro',
          'google/gemini-2.5-flash',
          'meta-llama/llama-3.3-70b-instruct',
          'mistralai/mistral-large',
          'deepseek/deepseek-chat',
          'x-ai/grok-4',
        ],
      },
      extraHeaders: [
        { key: 'HTTP-Referer', value: 'https://github.com/rakunlabs/at' },
        { key: 'X-Title', value: 'AT Gateway' },
      ],
      setupSteps: [
        'Go to openrouter.ai and sign in with Google / GitHub / email',
        'Navigate to Keys',
        'Click "Create Key", name it, and copy the key (starts with sk-or-)',
        'Paste the key in the API Key field below',
        'Add credit to your OpenRouter balance (pay-as-you-go) or use the free tier',
      ],
      setupLinks: [
        { label: 'API Keys', url: 'https://openrouter.ai/keys' },
        { label: 'Models', url: 'https://openrouter.ai/models' },
        { label: 'Docs', url: 'https://openrouter.ai/docs/' },
      ],
      notes: [
        'OpenAI-compatible — model names use the form "vendor/model" (e.g. anthropic/claude-sonnet-4)',
        'Useful as a backup for niche models you do not want to set up directly',
        'OpenRouter adds a small markup on top of native pricing',
        'Some models are free-tier; many require credit',
        'HTTP-Referer + X-Title headers identify your app on the OpenRouter dashboard',
      ],
    },
    {
      id: 'lmstudio',
      name: 'LM Studio',
      description: 'Run open-weight models locally with the LM Studio desktop app',
      key: 'lmstudio',
      config: {
        type: 'openai',
        base_url: 'http://localhost:1234/v1/chat/completions',
        model: 'local-model',
      },
      setupSteps: [
        'Install LM Studio from lmstudio.ai',
        'Open LM Studio and download a model from the "Discover" tab',
        'Switch to the "Developer" tab and click "Start Server" (default port 1234)',
        'No API key needed — leave the API Key field empty',
        'Set the model name to whatever your loaded model identifier is (or use "local-model" as a placeholder)',
      ],
      setupLinks: [
        { label: 'Install LM Studio', url: 'https://lmstudio.ai/' },
        { label: 'Server Docs', url: 'https://lmstudio.ai/docs/api/server' },
      ],
      notes: [
        'Completely free and private — runs entirely on your machine',
        'OpenAI-compatible API exposed by LM Studio\'s built-in server',
        'Default port is 1234 — change the Base URL if you use a different port',
        'If LM Studio is on another machine, replace localhost with the IP/hostname',
        'For CLI/headless setups, prefer the Ollama preset instead',
      ],
    },
  ];

  // ─── State ───

  let providers = $state<ProviderRecord[]>([]);
  let loading = $state(true);
  
  // Pagination
  let offset = $state(0);
  let limit = $state(10);
  let total = $state(0);

  // Search & Sort
  let searchQuery = $state('');
  let sorts = $state<SortEntry[]>([]);

  let showForm = $state(false);
  let showPresets = $state(false);
  let editingKey = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);
  let activePreset = $state<Preset | null>(null);

  // Config viewer state
  let configViewProvider = $state<ProviderRecord | null>(null);
  let configFormat = $state<'yaml' | 'json'>('yaml');
  let configCopied = $state(false);

  // Form fields
  let formKey = $state('');
  let formType = $state<string>('openai');
  let formApiKey = $state('');
  let formBaseUrl = $state('');
  let formModel = $state('');
  let formModels = $state<string[]>([]);
  let newModelInput = $state('');
  let formAuthType = $state('');
  let formProxy = $state('');
  let formInsecureSkipVerify = $state(false);
  let formHasStoredKey = $state(false);
  let formExtraHeaders = $state<{ key: string; value: string }[]>([]);
  let discoveringModels = $state(false);

  // Device auth state (GitHub OAuth Device Flow for Copilot)
  let deviceAuthPending = $state(false);
  let deviceAuthCode = $state('');
  let deviceAuthURI = $state('');
  let deviceAuthInterval = $state(5);
  let deviceAuthPolling = $state(false);
  let deviceAuthTimer = $state<ReturnType<typeof setInterval> | null>(null);

  // Claude auth state (Anthropic OAuth Authorization Code + PKCE)
  let claudeAuthPending = $state(false);
  let claudeAuthURL = $state('');
  let claudeAuthCode = $state('');
  let claudeAuthSubmitting = $state(false);

  // Claude auth token paste state
  let claudeTokenMode = $state(false);
  let claudeTokenAccess = $state('');
  let claudeTokenRefresh = $state('');
  let claudeTokenSubmitting = $state(false);

  // Claude auth sync state
  let claudeSyncing = $state(false);

  // ─── Load ───

  async function load() {
    loading = true;
    try {
      const params: any = { _offset: offset, _limit: limit };
      if (searchQuery) params['key[like]'] = `%${searchQuery}%`;
      const sortParam = buildSortParam(sorts);
      if (sortParam) params._sort = sortParam;
      const res = await listProviders(params);
      providers = res.data || [];
      total = res.meta?.total || 0;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load providers', 'alert');
    } finally {
      loading = false;
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

  // ─── Form ───

  function resetForm() {
    stopDeviceAuthPolling();
    resetClaudeAuth();
    formKey = '';
    formType = 'openai';
    formApiKey = '';
    formBaseUrl = '';
    formModel = '';
    formModels = [];
    newModelInput = '';
    formAuthType = '';
    formProxy = '';
    formInsecureSkipVerify = false;
    formHasStoredKey = false;
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
    formModels = [...(preset.config.models || [])];
    formAuthType = preset.config.auth_type || '';
    formProxy = '';
    formExtraHeaders = preset.extraHeaders ? [...preset.extraHeaders] : [];
    showPresets = false;
    showForm = true;
  }

  function openEdit(rec: ProviderRecord) {
    resetForm();
    editingKey = rec.key;
    formKey = rec.key;
    formType = rec.config.type;
    // The API redacts secrets as "***". Don't load the sentinel into the form —
    // leave it empty so buildConfig() omits it and the backend preserves the real value.
    formApiKey = rec.config.api_key === '***' ? '' : (rec.config.api_key || '');
    formHasStoredKey = !!rec.config.api_key;
    formBaseUrl = rec.config.base_url || '';
    formModel = rec.config.model;
    formModels = [...(rec.config.models || [])];
    formAuthType = rec.config.auth_type || '';
    formProxy = rec.config.proxy || '';
    formInsecureSkipVerify = rec.config.insecure_skip_verify || false;
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
    if (formAuthType) cfg.auth_type = formAuthType;
    if (formProxy) cfg.proxy = formProxy;

    const models = formModels.filter(Boolean);
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

  function addModel() {
    const model = newModelInput.trim();
    if (!model) return;
    if (formModels.includes(model)) {
      addToast(`"${model}" is already in the list`, 'warn');
      return;
    }
    formModels = [...formModels, model];
    newModelInput = '';
  }

  function removeModel(index: number) {
    formModels = formModels.filter((_, i) => i !== index);
  }

  // ─── Device Auth ───

  function stopDeviceAuthPolling() {
    if (deviceAuthTimer) {
      clearInterval(deviceAuthTimer);
      deviceAuthTimer = null;
    }
    deviceAuthPolling = false;
    deviceAuthPending = false;
    deviceAuthCode = '';
    deviceAuthURI = '';
  }

  async function handleDeviceAuth() {
    if (!editingKey) {
      addToast('Save the provider first, then click Authorize', 'warn');
      return;
    }

    try {
      deviceAuthPending = true;
      const resp = await startDeviceAuth(editingKey);
      deviceAuthCode = resp.user_code;
      deviceAuthURI = resp.verification_uri;
      deviceAuthInterval = resp.interval || 5;
      deviceAuthPolling = true;

      // Start polling for authorization status
      deviceAuthTimer = setInterval(async () => {
        try {
          const status = await getDeviceAuthStatus(editingKey!);
          if (status.status === 'authorized') {
            stopDeviceAuthPolling();
            addToast('GitHub Copilot authorized successfully');
            await load();
          } else if (status.status === 'expired') {
            stopDeviceAuthPolling();
            addToast('Authorization expired - please try again', 'alert');
          } else if (status.status === 'error') {
            stopDeviceAuthPolling();
            addToast(status.error || 'Authorization failed', 'alert');
          }
          // 'pending' and 'none' — keep polling
        } catch {
          stopDeviceAuthPolling();
          addToast('Failed to check authorization status', 'alert');
        }
      }, deviceAuthInterval * 1000);
    } catch (e: any) {
      deviceAuthPending = false;
      addToast(e?.response?.data?.message || 'Failed to start device authorization', 'alert');
    }
  }

  // ─── Claude Auth ───

  function resetClaudeAuth() {
    claudeAuthPending = false;
    claudeAuthURL = '';
    claudeAuthCode = '';
    claudeAuthSubmitting = false;
    claudeTokenMode = false;
    claudeTokenAccess = '';
    claudeTokenRefresh = '';
    claudeTokenSubmitting = false;
    claudeSyncing = false;
  }

  async function handleClaudeAuth() {
    if (!editingKey) {
      addToast('Save the provider first, then click Authorize', 'warn');
      return;
    }

    try {
      claudeAuthPending = true;
      claudeAuthCode = '';
      const resp = await startClaudeAuth(editingKey);
      claudeAuthURL = resp.auth_url;
    } catch (e: any) {
      claudeAuthPending = false;
      addToast(e?.response?.data?.message || 'Failed to start Claude authorization', 'alert');
    }
  }

  async function handleClaudeAuthSubmit() {
    if (!editingKey || !claudeAuthCode.trim()) {
      addToast('Please paste the authorization code', 'warn');
      return;
    }

    claudeAuthSubmitting = true;
    try {
      await submitClaudeAuthCode(editingKey, claudeAuthCode.trim());
      resetClaudeAuth();
      formHasStoredKey = true;
      addToast('Claude authorized successfully');
      await load();
    } catch (e: any) {
      claudeAuthSubmitting = false;
      addToast(e?.response?.data?.message || 'Failed to exchange authorization code', 'alert');
    }
  }

  async function handleClaudeTokenSubmit() {
    if (!editingKey || !claudeTokenAccess.trim() || !claudeTokenRefresh.trim()) {
      addToast('Both access token and refresh token are required', 'warn');
      return;
    }

    claudeTokenSubmitting = true;
    try {
      await submitClaudeAuthToken(editingKey, claudeTokenAccess.trim(), claudeTokenRefresh.trim());
      resetClaudeAuth();
      formHasStoredKey = true;
      addToast('Claude authorized successfully via token paste');
      await load();
    } catch (e: any) {
      claudeTokenSubmitting = false;
      addToast(e?.response?.data?.message || 'Failed to save tokens', 'alert');
    }
  }

  async function handleClaudeSync() {
    if (!editingKey) {
      addToast('Save the provider first', 'warn');
      return;
    }

    claudeSyncing = true;
    try {
      const resp = await syncClaudeAuthFromCLI(editingKey);
      resetClaudeAuth();
      formHasStoredKey = true;
      let msg = `Claude authorized via ${resp.source}`;
      if (resp.expires_at) {
        const exp = new Date(resp.expires_at);
        msg += ` (token expires ${exp.toLocaleString()})`;
      }
      addToast(msg);
      await load();
    } catch (e: any) {
      claudeSyncing = false;
      addToast(e?.response?.data?.message || 'Failed to sync from Claude Code CLI', 'alert');
    }
  }

  async function handleDiscoverModels() {
    if (!formType) {
      addToast('Select a provider type first', 'warn');
      return;
    }

    if (formType === 'vertex') {
      addToast('Model discovery is not supported for this provider type', 'warn');
      return;
    }

    discoveringModels = true;
    try {
      const cfg: Record<string, any> = { type: formType };
      if (formApiKey) cfg.api_key = formApiKey;
      if (formBaseUrl) cfg.base_url = formBaseUrl;
    if (formProxy) cfg.proxy = formProxy;
    if (formInsecureSkipVerify) cfg.insecure_skip_verify = true;

      const headers: Record<string, string> = {};
      for (const h of formExtraHeaders) {
        if (h.key && h.value) headers[h.key] = h.value;
      }
      if (Object.keys(headers).length > 0) cfg.extra_headers = headers;

      const models = await discoverModels(cfg as any, editingKey || undefined);
      if (models.length === 0) {
        addToast('No models found', 'warn');
      } else {
        formModels = models;
        addToast(`Found ${models.length} models`);
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to discover models', 'alert');
    } finally {
      discoveringModels = false;
    }
  }

  // ─── Config Viewer ───

  function openConfigView(rec: ProviderRecord) {
    configViewProvider = rec;
    configFormat = 'yaml';
    configCopied = false;
  }

  function closeConfigView() {
    configViewProvider = null;
    configCopied = false;
  }

  function getConfigSnippet(): string {
    if (!configViewProvider) return '';
    if (configFormat === 'yaml') {
      return generateYamlSnippet(configViewProvider.key, configViewProvider.config);
    }
    return generateJsonSnippet(configViewProvider.key, configViewProvider.config);
  }

  function copyConfigSnippet() {
    const snippet = getConfigSnippet();
    navigator.clipboard.writeText(snippet);
    configCopied = true;
    addToast('Config copied to clipboard');
    setTimeout(() => { configCopied = false; }, 2000);
  }
</script>

<svelte:head>
  <title>AT | Providers</title>
</svelte:head>

<div class="p-6 max-w-5xl mx-auto">
  <!-- Header -->
  <div class="flex items-start justify-between mb-6">
    <div>
      <h1 class="text-lg font-semibold text-gray-900 dark:text-dark-text">Providers</h1>
      <p class="text-sm text-gray-500 dark:text-dark-text-muted mt-0.5">Configure LLM backends for the gateway</p>
      <span class="text-xs text-gray-400 dark:text-dark-text-muted">({total})</span>
    </div>
    <div class="flex gap-2">
      <button
        onclick={openPresets}
        class="flex items-center gap-1.5 px-3 py-1.5 bg-gray-900 dark:bg-accent text-white text-sm hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors"
      >
        <Layers size={14} />
        From Template
      </button>
      <button
        onclick={openCreate}
        class="flex items-center gap-1.5 px-3 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-highest text-gray-700 dark:text-dark-text-secondary transition-colors"
      >
        <Plus size={14} />
        Custom
      </button>
    </div>
  </div>

  <!-- Preset Picker -->
  {#if showPresets}
    <div class="border border-gray-200 dark:border-dark-border mb-6 bg-white dark:bg-dark-surface overflow-hidden">
      <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border">
        <span class="text-sm font-medium text-gray-900 dark:text-dark-text">Choose a Template</span>
        <button onclick={resetForm} class="p-1 hover:bg-gray-100 dark:hover:bg-dark-highest text-gray-400 dark:text-dark-text-faint hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors">
          <X size={14} />
        </button>
      </div>
      <div class="p-4 grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-3">
        {#each PRESETS as preset}
          <button
            onclick={() => applyPreset(preset)}
            class="text-left border border-gray-200 dark:border-dark-border p-3 hover:border-gray-400 dark:hover:border-dark-border-subtle hover:shadow-sm transition-all group"
          >
            <div class="font-medium text-sm text-gray-900 dark:text-dark-text group-hover:text-gray-900 dark:group-hover:text-dark-text">{preset.name}</div>
            <div class="text-xs text-gray-500 dark:text-dark-text-muted mt-1 leading-relaxed">{preset.description}</div>
            <div class="mt-2.5">
              <span class="text-xs px-1.5 py-0.5 bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary font-mono">{preset.config.type}</span>
            </div>
          </button>
        {/each}
      </div>
    </div>
  {/if}

  <!-- Form -->
  {#if showForm}
    <div class="border border-gray-200 dark:border-dark-border mb-6 bg-white dark:bg-dark-surface overflow-hidden">
      <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-elevated">
        <span class="text-sm font-medium text-gray-900 dark:text-dark-text">
          {#if editingKey}
            Edit: {editingKey}
          {:else if activePreset}
            New Provider: {activePreset.name}
          {:else}
            New Provider (Custom)
          {/if}
        </span>
        <button onclick={resetForm} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-highest text-gray-400 dark:text-dark-text-faint hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors">
          <X size={14} />
        </button>
      </div>

      <!-- Setup Guide (shown when using a preset) -->
      {#if activePreset}
        <div class="border-b border-gray-200 dark:border-dark-border bg-blue-50/50 dark:bg-blue-900/20 px-4 py-4">
          <div class="flex items-start gap-2.5">
            <BookOpen size={16} class="text-blue-600 mt-0.5 shrink-0" />
            <div class="flex-1 min-w-0">
              <div class="text-sm font-medium text-gray-900 dark:text-dark-text mb-2">Setup Guide</div>
              <ol class="text-xs text-gray-700 dark:text-dark-text-secondary space-y-1.5 list-decimal list-inside leading-relaxed">
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
                      class="inline-flex items-center gap-1 text-xs px-2 py-1 bg-white dark:bg-dark-elevated border border-gray-200 dark:border-dark-border text-gray-600 dark:text-dark-text-secondary hover:border-gray-400 dark:hover:border-dark-border-subtle hover:text-gray-900 dark:hover:text-dark-text transition-colors"
                    >
                      {link.label}
                      <ExternalLink size={10} />
                    </a>
                  {/each}
                </div>
              {/if}

              {#if activePreset.notes && activePreset.notes.length > 0}
                <div class="mt-3 pt-3 border-t border-blue-100 dark:border-blue-800">
                  {#each activePreset.notes as note}
                    <div class="text-xs text-gray-500 dark:text-dark-text-muted mt-1 leading-relaxed">{note}</div>
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
          <label for="form-key" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Key</label>
          <input
            id="form-key"
            type="text"
            bind:value={formKey}
            disabled={!!editingKey}
            placeholder="e.g., anthropic, groq, ollama"
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle disabled:bg-gray-50 disabled:text-gray-500 dark:disabled:bg-dark-surface dark:disabled:text-dark-text-muted dark:bg-dark-elevated dark:text-dark-text dark:placeholder-dark-text-muted transition-colors"
          />
        </div>

        <!-- Type -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-type" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Type</label>
          <div class="col-span-3 relative">
            <select
              id="form-type"
              bind:value={formType}
              onchange={() => { formAuthType = ''; }}
              class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm appearance-none bg-white dark:bg-dark-elevated dark:text-dark-text pr-8 focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors"
            >
              {#each PROVIDER_TYPES as t}
                <option value={t}>{t}</option>
              {/each}
            </select>
            <ChevronDown size={14} class="absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none text-gray-400 dark:text-dark-text-faint" />
          </div>
        </div>

        <!-- Auth Type (for openai and anthropic) -->
        {#if formType === 'openai' || formType === 'anthropic'}
          <div class="grid grid-cols-4 gap-3 items-center">
            <label for="form-authtype" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Auth Type</label>
            <div class="col-span-3 relative">
              <select
                id="form-authtype"
                bind:value={formAuthType}
                class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm appearance-none bg-white dark:bg-dark-elevated dark:text-dark-text pr-8 focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors"
              >
                <option value="">(none)</option>
                {#if formType === 'openai'}
                  <option value="copilot">copilot</option>
                {/if}
                {#if formType === 'anthropic'}
                  <option value="claude-code">claude-code</option>
                {/if}
              </select>
              <ChevronDown size={14} class="absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none text-gray-400 dark:text-dark-text-faint" />
            </div>
          </div>
        {/if}

        <!-- API Key / Device Auth -->
        {#if formAuthType === 'copilot'}
          <div class="grid grid-cols-4 gap-3 items-start">
            <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Authorization</span>
            <div class="col-span-3">
              {#if deviceAuthPending}
                <!-- Device flow in progress -->
                <div class="border border-blue-200 dark:border-blue-800 bg-blue-50/50 dark:bg-blue-900/20 p-4 space-y-3">
                  <div class="text-sm text-gray-700 dark:text-dark-text-secondary">
                    Open <a href={deviceAuthURI} target="_blank" rel="noopener noreferrer" class="font-medium text-blue-600 hover:text-blue-800 dark:text-blue-400 dark:hover:text-blue-300 underline">{deviceAuthURI}</a> and enter the code:
                  </div>
                  <div class="flex items-center gap-3">
                    <code class="text-2xl font-bold font-mono tracking-widest text-gray-900 dark:text-dark-text bg-white dark:bg-dark-elevated border border-gray-200 dark:border-dark-border px-4 py-2 select-all">{deviceAuthCode}</code>
                    <button
                      type="button"
                      onclick={() => { navigator.clipboard.writeText(deviceAuthCode); addToast('Code copied to clipboard'); }}
                      class="px-2.5 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-highest text-gray-600 dark:text-dark-text-secondary transition-colors"
                    >
                      Copy
                    </button>
                  </div>
                  <div class="flex items-center gap-2 text-xs text-gray-500 dark:text-dark-text-muted">
                    <RefreshCw size={12} class="animate-spin" />
                    Waiting for authorization...
                  </div>
                  <button
                    type="button"
                    onclick={stopDeviceAuthPolling}
                    class="text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors"
                  >
                    Cancel
                  </button>
                </div>
              {:else if editingKey && formHasStoredKey}
                <!-- Already authorized -->
                <div class="flex items-center gap-3">
                  <span class="inline-flex items-center gap-1.5 text-sm text-green-700 dark:text-green-400 bg-green-50 dark:bg-green-900/30 border border-green-200 dark:border-green-800 px-3 py-1.5">
                    <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"/></svg>
                    Authorized via GitHub
                  </span>
                  <button
                    type="button"
                    onclick={handleDeviceAuth}
                    class="flex items-center gap-1.5 px-2.5 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-highest text-gray-600 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text transition-colors"
                  >
                    <LogIn size={13} />
                    Re-authorize
                  </button>
                </div>
              {:else if editingKey}
                <!-- Not yet authorized, editing existing provider -->
                <div class="flex items-center gap-3">
                  <button
                    type="button"
                    onclick={handleDeviceAuth}
                    class="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors"
                  >
                    <LogIn size={14} />
                    Authorize with GitHub
                  </button>
                  <span class="text-xs text-gray-500 dark:text-dark-text-muted">Opens github.com in your browser</span>
                </div>
              {:else}
                <!-- New provider, not yet saved -->
                <div class="text-sm text-gray-500 dark:text-dark-text-muted py-1.5">
                  Save the provider first, then click Authorize to sign in via GitHub.
                </div>
              {/if}
            </div>
          </div>
        {:else if formAuthType === 'claude-code'}
          <!-- Claude Code OAuth flow -->
          <div class="grid grid-cols-4 gap-3 items-start">
            <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Authorization</span>
            <div class="col-span-3">
              {#if claudeAuthPending}
                <!-- Auth flow in progress -->
                <div class="border border-blue-200 dark:border-blue-800 bg-blue-50/50 dark:bg-blue-900/20 p-4 space-y-3">
                  <div class="text-sm text-gray-700 dark:text-dark-text-secondary">
                    1. Open this link and sign in with your Claude account:
                  </div>
                  <div class="flex items-center gap-2">
                    <a href={claudeAuthURL} target="_blank" rel="noopener noreferrer" class="text-sm font-medium text-blue-600 hover:text-blue-800 dark:text-blue-400 dark:hover:text-blue-300 underline break-all">
                      {claudeAuthURL.length > 80 ? claudeAuthURL.substring(0, 80) + '...' : claudeAuthURL}
                    </a>
                    <button
                      type="button"
                      onclick={() => { navigator.clipboard.writeText(claudeAuthURL); addToast('URL copied to clipboard'); }}
                      class="shrink-0 px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-highest text-gray-600 dark:text-dark-text-secondary transition-colors"
                    >
                      Copy
                    </button>
                  </div>
                  <div class="text-sm text-gray-700 dark:text-dark-text-secondary mt-2">
                    2. After authorizing, you'll see a code on the page. Paste it here:
                  </div>
                  <div class="flex items-center gap-2">
                    <input
                      type="text"
                      bind:value={claudeAuthCode}
                      placeholder="Paste authorization code here"
                      class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder-dark-text-muted transition-colors"
                    />
                    <button
                      type="button"
                      onclick={handleClaudeAuthSubmit}
                      disabled={claudeAuthSubmitting || !claudeAuthCode.trim()}
                      class="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
                    >
                      {#if claudeAuthSubmitting}
                        <RefreshCw size={13} class="animate-spin" />
                        Authorizing...
                      {:else}
                        <Check size={13} />
                        Submit
                      {/if}
                    </button>
                  </div>
                  <button
                    type="button"
                    onclick={resetClaudeAuth}
                    class="text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors"
                  >
                    Cancel
                  </button>
                </div>
              {:else if claudeTokenMode}
                <!-- Token paste mode -->
                <div class="border border-amber-200 dark:border-amber-800 bg-amber-50/50 dark:bg-amber-900/20 p-4 space-y-3">
                  <div class="text-sm font-medium text-gray-900 dark:text-dark-text">Paste Tokens from Claude Code CLI</div>
                  <div class="text-xs text-gray-500 dark:text-dark-text-muted leading-relaxed">
                    Extract tokens from Claude Code CLI. On macOS run:<br/>
                    <code class="text-xs bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 mt-1 inline-block font-mono">security find-generic-password -s "Claude Code-credentials" -w | python3 -c "import json,sys; d=json.loads(sys.stdin.read()); o=d.get('claudeAiOauth',d); print('Access:', o['accessToken']); print('Refresh:', o['refreshToken'])"</code>
                    <br/>On Linux:<br/>
                    <code class="text-xs bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 mt-1 inline-block font-mono">cat ~/.claude/.credentials.json | python3 -c "import json,sys; d=json.loads(sys.stdin.read()); o=d.get('claudeAiOauth',d); print('Access:', o['accessToken']); print('Refresh:', o['refreshToken'])"</code>
                  </div>
                  <div class="space-y-2">
                    <input
                      type="password"
                      bind:value={claudeTokenAccess}
                      placeholder="Access Token"
                      autocomplete="off"
                      class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder-dark-text-muted transition-colors"
                    />
                    <input
                      type="password"
                      bind:value={claudeTokenRefresh}
                      placeholder="Refresh Token"
                      autocomplete="off"
                      class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder-dark-text-muted transition-colors"
                    />
                  </div>
                  <div class="flex items-center gap-2">
                    <button
                      type="button"
                      onclick={handleClaudeTokenSubmit}
                      disabled={claudeTokenSubmitting || !claudeTokenAccess.trim() || !claudeTokenRefresh.trim()}
                      class="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
                    >
                      {#if claudeTokenSubmitting}
                        <RefreshCw size={13} class="animate-spin" />
                        Saving...
                      {:else}
                        <Check size={13} />
                        Save Tokens
                      {/if}
                    </button>
                    <button
                      type="button"
                      onclick={() => { claudeTokenMode = false; claudeTokenAccess = ''; claudeTokenRefresh = ''; }}
                      class="text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors"
                    >
                      Cancel
                    </button>
                  </div>
                </div>
              {:else if editingKey && formHasStoredKey}
                <!-- Already authorized -->
                <div class="flex flex-wrap items-center gap-2">
                  <span class="inline-flex items-center gap-1.5 text-sm text-green-700 dark:text-green-400 bg-green-50 dark:bg-green-900/30 border border-green-200 dark:border-green-800 px-3 py-1.5">
                    <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"/></svg>
                    Authorized via Claude
                  </span>
                  <button
                    type="button"
                    onclick={handleClaudeAuth}
                    class="flex items-center gap-1.5 px-2.5 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-highest text-gray-600 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text transition-colors"
                  >
                    <LogIn size={13} />
                    Re-authorize
                  </button>
                  <button
                    type="button"
                    onclick={() => { claudeTokenMode = true; }}
                    class="flex items-center gap-1.5 px-2.5 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-highest text-gray-600 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text transition-colors"
                  >
                    <KeyRound size={13} />
                    Paste Token
                  </button>
                  <button
                    type="button"
                    onclick={handleClaudeSync}
                    disabled={claudeSyncing}
                    class="flex items-center gap-1.5 px-2.5 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-highest text-gray-600 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text disabled:opacity-50 transition-colors"
                  >
                    {#if claudeSyncing}
                      <RefreshCw size={13} class="animate-spin" />
                      Syncing...
                    {:else}
                      <DownloadCloud size={13} />
                      Sync from CLI
                    {/if}
                  </button>
                </div>
              {:else if editingKey}
                <!-- Not yet authorized, editing existing provider -->
                <div class="flex flex-wrap items-center gap-2">
                  <button
                    type="button"
                    onclick={handleClaudeAuth}
                    class="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors"
                  >
                    <LogIn size={14} />
                    Authorize with Claude
                  </button>
                  <button
                    type="button"
                    onclick={() => { claudeTokenMode = true; }}
                    class="flex items-center gap-1.5 px-3 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-highest text-gray-600 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text transition-colors"
                  >
                    <KeyRound size={14} />
                    Paste Token
                  </button>
                  <button
                    type="button"
                    onclick={handleClaudeSync}
                    disabled={claudeSyncing}
                    class="flex items-center gap-1.5 px-3 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-highest text-gray-600 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text disabled:opacity-50 transition-colors"
                  >
                    {#if claudeSyncing}
                      <RefreshCw size={14} class="animate-spin" />
                      Syncing...
                    {:else}
                      <DownloadCloud size={14} />
                      Sync from CLI
                    {/if}
                  </button>
                </div>
                <div class="text-xs text-gray-500 dark:text-dark-text-muted mt-1.5">
                  Browser login, paste tokens manually, or auto-sync from Claude Code CLI
                </div>
              {:else}
                <!-- New provider, not yet saved -->
                <div class="text-sm text-gray-500 dark:text-dark-text-muted py-1.5">
                  Save the provider first, then authorize via browser, paste tokens, or sync from Claude Code CLI.
                </div>
              {/if}
            </div>
          </div>
        {:else}
          <!-- Standard API Key input -->
          <div class="grid grid-cols-4 gap-3 items-center">
            <label for="form-apikey" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">API Key</label>
            <input
              id="form-apikey"
              type="password"
              autocomplete="off"
              bind:value={formApiKey}
              placeholder={formHasStoredKey ? '(stored - leave blank to keep)' : activePreset?.id === 'vertex' ? '(not needed - uses ADC)' : activePreset?.id === 'ollama' ? '(not needed)' : activePreset?.id === 'google-ai' ? 'AIza...' : activePreset?.id === 'github-models' ? 'github_pat_...' : 'sk-...'}
              class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder-dark-text-muted transition-colors"
            />
          </div>
        {/if}

        <!-- Base URL -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-baseurl" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Base URL</label>
          <input
            id="form-baseurl"
            type="text"
            bind:value={formBaseUrl}
            placeholder={activePreset?.id === 'vertex'
              ? 'https://{LOCATION}-aiplatform.googleapis.com/v1/projects/{PROJECT}/locations/{LOCATION}/endpoints/openapi/chat/completions'
              : activePreset?.id === 'google-ai'
              ? '(default: https://generativelanguage.googleapis.com)'
              : 'https://api.example.com/v1/chat/completions'}
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder-dark-text-muted transition-colors"
          />
        </div>

        <!-- Proxy -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-proxy" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Proxy</label>
          <input
            id="form-proxy"
            type="text"
            bind:value={formProxy}
            placeholder="e.g., http://proxy:8080 or socks5://127.0.0.1:1080"
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder-dark-text-muted transition-colors"
          />
        </div>

        <!-- Insecure Skip Verify -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Skip TLS Verify</span>
          <label class="col-span-3 flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              bind:checked={formInsecureSkipVerify}
              class="accent-gray-900 dark:accent-accent w-4 h-4"
            />
            <span class="text-sm text-gray-600 dark:text-dark-text-secondary">Disable certificate verification (insecure)</span>
          </label>
        </div>

        <!-- Model -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-model" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Default Model</label>
          <input
            id="form-model"
            type="text"
            bind:value={formModel}
            placeholder="e.g., gpt-4o, claude-haiku-4-5"
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder-dark-text-muted transition-colors"
          />
        </div>

        <!-- Models -->
        <div class="grid grid-cols-4 gap-3">
          <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Models</span>
          <div class="col-span-3 space-y-2">
            {#each formModels as model, i}
              <div class="flex gap-2 items-center">
                <span class="flex-1 border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-elevated px-3 py-1.5 text-sm font-mono text-gray-700 dark:text-dark-text-secondary">{model}</span>
                <button
                  type="button"
                  onclick={() => removeModel(i)}
                  class="p-1.5 border border-gray-300 dark:border-dark-border-subtle hover:bg-red-50 dark:hover:bg-red-900/30 hover:border-red-300 dark:hover:border-red-800 hover:text-red-600 dark:hover:text-red-400 text-gray-400 dark:text-dark-text-faint transition-colors"
                >
                  <X size={12} />
                </button>
              </div>
            {/each}
            <div class="flex gap-2">
              <input
                type="text"
                bind:value={newModelInput}
                placeholder="model name"
                onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addModel(); } }}
                class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder-dark-text-muted transition-colors"
              />
              <button
                type="button"
                onclick={addModel}
                class="px-2.5 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-highest text-gray-600 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text transition-colors shrink-0"
              >
                + Add
              </button>
              <button
                type="button"
                onclick={handleDiscoverModels}
                disabled={discoveringModels}
                class="flex items-center gap-1.5 px-2.5 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-highest text-gray-600 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text transition-colors disabled:opacity-50 disabled:cursor-not-allowed shrink-0"
                title="Fetch available models from the provider using the API key above"
              >
                <RefreshCw size={13} class={discoveringModels ? 'animate-spin' : ''} />
                {discoveringModels ? 'Fetching...' : 'Fetch'}
              </button>
            </div>
          </div>
        </div>

        <!-- Extra Headers -->
        <div class="grid grid-cols-4 gap-3">
          <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Extra Headers</span>
          <div class="col-span-3 space-y-2">
            {#each formExtraHeaders as header, i}
              <div class="flex gap-2">
                <input
                  type="text"
                  bind:value={header.key}
                  placeholder="Header-Name"
                  class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder-dark-text-muted transition-colors"
                />
                <input
                  type="text"
                  bind:value={header.value}
                  placeholder="value"
                  class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder-dark-text-muted transition-colors"
                />
                <button
                  type="button"
                  onclick={() => removeHeader(i)}
                  class="p-1.5 border border-gray-300 dark:border-dark-border-subtle hover:bg-red-50 dark:hover:bg-red-900/30 hover:border-red-300 dark:hover:border-red-800 hover:text-red-600 dark:hover:text-red-400 text-gray-400 dark:text-dark-text-faint transition-colors"
                >
                  <X size={12} />
                </button>
              </div>
            {/each}
            <button
              type="button"
              onclick={addHeader}
              class="text-sm text-gray-500 dark:text-dark-text-muted hover:text-gray-900 dark:hover:text-dark-text transition-colors"
            >
              + Add header
            </button>
          </div>
        </div>

        <!-- Actions -->
        <div class="flex justify-end gap-2 pt-3 border-t border-gray-100 dark:border-dark-border">
          <button
            type="button"
            onclick={resetForm}
            class="px-3 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-highest text-gray-700 dark:text-dark-text-secondary transition-colors"
          >
            Cancel
          </button>
          <button
            type="submit"
            class="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors"
          >
            <Save size={14} />
            {editingKey ? 'Update' : 'Create'}
          </button>
        </div>
      </form>
    </div>
  {/if}

  <!-- Provider list -->
  {#if loading || providers.length > 0 || (!showForm && !showPresets)}
    <DataTable
      items={providers}
      {loading}
      {total}
      {limit}
      bind:offset
      onchange={load}
      onsearch={handleSearch}
      searchPlaceholder="Search by key..."
      emptyIcon={Layers}
      emptyTitle="No providers configured"
      emptyDescription="Add a provider to start routing requests"
    >
      {#snippet header()}
        <SortableHeader field="key" label="Key" {sorts} onsort={handleSort} />
        <SortableHeader field="type" label="Type" {sorts} onsort={handleSort} />
        <SortableHeader field="model" label="Model" {sorts} onsort={handleSort} />
        <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Models</th>
        <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Base URL</th>
        <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-28"></th>
      {/snippet}

      {#snippet row(rec)}
        <tr class="hover:bg-gray-50/50 dark:hover:bg-dark-highest/50 transition-colors">
          <td class="px-4 py-2.5 font-mono font-medium text-gray-900 dark:text-dark-text">{rec.key}</td>
          <td class="px-4 py-2.5">
            <span class="px-2 py-0.5 text-xs bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary font-mono">{rec.config.type}</span>
          </td>
          <td class="px-4 py-2.5 font-mono text-xs text-gray-600 dark:text-dark-text-secondary">{rec.config.model}</td>
          <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">
            {(rec.config.models || []).length > 0 ? (rec.config.models || []).join(', ') : '-'}
          </td>
          <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted truncate max-w-48" title={rec.config.base_url || ''}>
            {rec.config.base_url || 'default'}
          </td>
          <td class="px-4 py-2.5 text-right">
            <div class="flex justify-end gap-1">
              <button
                onclick={() => openConfigView(rec)}
                class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-highest text-gray-400 dark:text-dark-text-faint hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors"
                title="View Config"
              >
                <FileCode size={14} />
              </button>
              <button
                onclick={() => openEdit(rec)}
                class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-highest text-gray-400 dark:text-dark-text-faint hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors"
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
                  class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-highest transition-colors"
                >
                  Cancel
                </button>
              {:else}
                <button
                  onclick={() => (deleteConfirm = rec.key)}
                  class="p-1.5 hover:bg-red-50 dark:hover:bg-red-900/30 text-gray-400 dark:text-dark-text-faint hover:text-red-600 dark:hover:text-red-400 transition-colors"
                  title="Delete"
                >
                  <Trash2 size={14} />
                </button>
              {/if}
            </div>
          </td>
        </tr>
      {/snippet}
    </DataTable>
  {/if}

  <!-- Config Viewer Modal -->
  {#if configViewProvider}
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      class="fixed inset-0 bg-black/40 dark:bg-black/60 z-50 flex items-center justify-center p-4"
      onkeydown={(e) => { if (e.key === 'Escape') closeConfigView(); }}
      onclick={(e) => { if (e.target === e.currentTarget) closeConfigView(); }}
    >
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <div class="bg-white dark:bg-dark-surface shadow-xl w-full max-w-xl overflow-hidden" onclick={(e) => e.stopPropagation()}>
        <!-- Header -->
        <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-elevated">
          <span class="text-sm font-medium text-gray-900 dark:text-dark-text">
            Config: <span class="font-mono">{configViewProvider.key}</span>
          </span>
          <button onclick={closeConfigView} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-highest text-gray-400 dark:text-dark-text-faint hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors">
            <X size={14} />
          </button>
        </div>

        <!-- Format Toggle + Copy -->
        <div class="flex items-center justify-between px-4 py-2 border-b border-gray-100 dark:border-dark-border">
          <div class="flex gap-1">
            <button
              onclick={() => { configFormat = 'yaml'; configCopied = false; }}
              class="px-2.5 py-1 text-xs font-medium transition-colors {configFormat === 'yaml' ? 'bg-gray-900 dark:bg-accent text-white' : 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary hover:bg-gray-200 dark:hover:bg-dark-highest'}"
            >
              YAML
            </button>
            <button
              onclick={() => { configFormat = 'json'; configCopied = false; }}
              class="px-2.5 py-1 text-xs font-medium transition-colors {configFormat === 'json' ? 'bg-gray-900 dark:bg-accent text-white' : 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary hover:bg-gray-200 dark:hover:bg-dark-highest'}"
            >
              JSON
            </button>
          </div>
          <button
            onclick={copyConfigSnippet}
            class="flex items-center gap-1.5 px-2.5 py-1 text-xs border border-gray-200 dark:border-dark-border hover:bg-gray-50 dark:hover:bg-dark-highest text-gray-600 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text transition-colors"
          >
            {#if configCopied}
              <Check size={12} class="text-green-600" />
              <span class="text-green-600">Copied</span>
            {:else}
              <Copy size={12} />
              Copy
            {/if}
          </button>
        </div>

        <!-- Code Block -->
        <div class="p-4 bg-gray-50 dark:bg-dark-elevated max-h-96 overflow-auto">
          <pre class="text-xs font-mono text-gray-800 dark:text-dark-text-secondary whitespace-pre leading-relaxed">{getConfigSnippet()}</pre>
        </div>

        <!-- Hint -->
        <div class="px-4 py-2.5 border-t border-gray-100 dark:border-dark-border bg-white dark:bg-dark-surface">
          <p class="text-xs text-gray-500 dark:text-dark-text-muted">
            Add this to your <span class="font-mono font-medium">at.yaml</span> configuration file to define this provider.
          </p>
        </div>
      </div>
    </div>
  {/if}
</div>
