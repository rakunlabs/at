<script lang="ts">
  import { storeNavbar, storeInfo } from '@/lib/store/store.svelte';
  import { getInfo, type InfoProvider } from '@/lib/api/gateway';
  import { listMCPServers, type MCPServer } from '@/lib/api/mcp-servers';
  import { addToast } from '@/lib/store/toast.svelte';
  import { Copy, BookOpen, RefreshCw, Check, CheckSquare, Square } from 'lucide-svelte';

  storeNavbar.title = 'Documentation';

  let providers = $state<InfoProvider[]>([]);
  let mcpServers = $state<MCPServer[]>([]);
  let loading = $state(true);
  let copiedId = $state<string | null>(null);
  let activeTab = $state('python');
  // Currently selected MCP server name for the example snippet. Defaults
  // to the first server returned by the API; falls back to "management"
  // when none are configured so the snippet still demonstrates the
  // canonical builtin server.
  let selectedMcpName = $state('');

  const tabs = [
    { id: 'python', label: 'Python' },
    { id: 'js', label: 'JavaScript' },
    { id: 'go', label: 'Go' },
    { id: 'curl', label: 'curl' },
  ];

  async function load() {
    loading = true;
    try {
      // Pull info + MCP servers in parallel. MCP server listing is best-effort
      // because the docs page is still useful when no servers are configured;
      // failures fall back to a "management" placeholder in the snippet.
      const [info, mcpRes] = await Promise.allSettled([
        getInfo(),
        listMCPServers({ _limit: 100 }),
      ]);

      if (info.status === 'fulfilled') {
        providers = info.value.providers;
      } else {
        addToast(info.reason?.response?.data?.message || 'Failed to load info', 'alert');
      }

      if (mcpRes.status === 'fulfilled') {
        mcpServers = mcpRes.value.data || [];
        if (mcpServers.length > 0 && !selectedMcpName) {
          // Prefer "management" when present (the most common starter
          // example) — otherwise just take the first one.
          const mgmt = mcpServers.find((s) => s.name === 'management');
          selectedMcpName = mgmt?.name || mcpServers[0].name;
        }
      }

      // Initialize selection: select default models for all providers
      if (providers.length > 0) {
        // Populate default models
        const defaults = new Set<string>();
        for (const p of providers) {
          if (p.default_model) {
            defaults.add(`${p.key}/${p.default_model}`);
          } else if (p.models && p.models.length > 0) {
            defaults.add(`${p.key}/${p.models[0]}`);
          }
        }
        selectedModels = defaults;
      }
    } finally {
      loading = false;
    }
  }

  load();

  let baseUrl = $derived((window.location.origin + window.location.pathname).replace(/\/+$/, ''));

  let allModels = $derived(
    providers.flatMap((p) => {
      if (p.models && p.models.length > 0) {
        return p.models.map((m) => `${p.key}/${m}`);
      }
      return [`${p.key}/${p.default_model}`];
    })
  );

  let exampleModel = $derived(allModels.length > 0 ? allModels[0] : 'provider/model-name');

  function copyCode(id: string, text: string) {
    navigator.clipboard.writeText(text);
    copiedId = id;
    setTimeout(() => (copiedId = null), 2000);
  }

  // Opencode Config Logic
  let selectedProviderKey = $state('');
  // Stores full model IDs like "provider/model"
  let selectedModels = $state<Set<string>>(new Set());

  let visibleProviders = $derived(
    selectedProviderKey ? providers.filter((p) => p.key === selectedProviderKey) : providers
  );

  function handleProviderChange(e: Event) {
    selectedProviderKey = (e.target as HTMLSelectElement).value;
  }

  function toggleModel(fullModelId: string) {
    if (selectedModels.has(fullModelId)) {
      selectedModels.delete(fullModelId);
    } else {
      selectedModels.add(fullModelId);
    }
    selectedModels = new Set(selectedModels); // trigger update
  }

  function selectAll() {
    for (const p of visibleProviders) {
      const models = p.models && p.models.length > 0 ? p.models : [p.default_model];
      for (const m of models) {
        selectedModels.add(`${p.key}/${m}`);
      }
    }
    selectedModels = new Set(selectedModels);
  }

  function selectNone() {
    for (const p of visibleProviders) {
      const models = p.models && p.models.length > 0 ? p.models : [p.default_model];
      for (const m of models) {
        selectedModels.delete(`${p.key}/${m}`);
      }
    }
    selectedModels = new Set(selectedModels);
  }

  // ─── Opencode MCP snippet ───
  // Builds a JSON block that drops into ~/.config/opencode/opencode.json.
  // The snippet uses the canonical "remote" transport that opencode
  // supports for HTTP-MCP servers. We hardcode "at_xxxxx" as a token
  // placeholder so the user knows to swap in their own from /tokens.
  let opencodeMcpConfig = $derived.by(() => {
    // Fallback to "management" if the user has no MCP servers yet —
    // it's a builtin so the example is always meaningful.
    const name = selectedMcpName || 'management';
    const cfg = {
      $schema: 'https://opencode.ai/config.json',
      mcp: {
        [`at-${name}`]: {
          type: 'remote',
          url: `${baseUrl}/gateway/v1/mcp/${name}`,
          enabled: true,
          headers: {
            Authorization: 'Bearer at_xxxxx',
          },
        },
      },
    };
    return JSON.stringify(cfg, null, 2);
  });

  let opencodeConfig = $derived.by(() => {
    // Collect all selected models from the global set
    const modelsObj: Record<string, { name: string }> = {};
    // Sort keys for consistent output
    const sortedModels = Array.from(selectedModels).sort();
    for (const m of sortedModels) {
      modelsObj[m] = { name: m };
    }

    const providerId = (storeInfo.name || 'at').toLowerCase().replace(/\s+/g, '-');
    const config = {
      $schema: 'https://opencode.ai/config.json',
      provider: {
        [providerId]: {
          npm: '@ai-sdk/openai-compatible',
          name: storeInfo.name || 'AT',
          options: {
            baseURL: `${baseUrl}/gateway/v1`,
          },
          models: modelsObj,
        },
      },
    };
    return JSON.stringify(config, null, 2);
  });

  function pythonExample(model: string, url: string): string {
    return `from openai import OpenAI

client = OpenAI(
    base_url="${url}/gateway/v1",
    api_key="at_your_token_here",
)

response = client.chat.completions.create(
    model="${model}",
    messages=[
        {"role": "user", "content": "Hello!"}
    ],
)

print(response.choices[0].message.content)`;
  }

  function jsExample(model: string, url: string): string {
    return `import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "${url}/gateway/v1",
  apiKey: "at_your_token_here",
});

const response = await client.chat.completions.create({
  model: "${model}",
  messages: [
    { role: "user", content: "Hello!" }
  ],
});

console.log(response.choices[0].message.content);`;
  }

  function goExample(model: string, url: string): string {
    return `package main

import (
	"context"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func main() {
	client := openai.NewClient(
		option.WithBaseURL("${url}/gateway/v1"),
		option.WithAPIKey("at_your_token_here"),
	)

	resp, err := client.Chat.Completions.New(context.TODO(),
		openai.ChatCompletionNewParams{
			Model: "${model}",
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Hello!"),
			},
		},
	)
	if err != nil {
		panic(err)
	}

	fmt.Println(resp.Choices[0].Message.Content)
}`;
  }

  function curlExample(model: string, url: string): string {
    return `curl ${url}/gateway/v1/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer at_your_token_here" \\
  -d '{
    "model": "${model}",
    "messages": [
      {"role": "user", "content": "Hello!"}
    ]
  }'`;
  }

  function curlModelsExample(url: string): string {
    return `curl ${url}/gateway/v1/models \\
  -H "Authorization: Bearer at_your_token_here"`;
  }

  function getActiveExample(tab: string, model: string, url: string): string {
    switch (tab) {
      case 'python':
        return pythonExample(model, url);
      case 'js':
        return jsExample(model, url);
      case 'go':
        return goExample(model, url);
      case 'curl':
        return curlExample(model, url);
      default:
        return '';
    }
  }
</script>

<svelte:head>
  <title>AT | Documentation</title>
</svelte:head>

<div class="p-6 max-w-5xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-6">
    <div class="flex items-center gap-2">
      <BookOpen size={16} class="text-gray-500 dark:text-dark-text-muted" />
      <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">API Documentation</h2>
    </div>
    <button
      onclick={load}
      class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
      title="Refresh"
    >
      <RefreshCw size={14} />
    </button>
  </div>

  <!-- Overview -->
  <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-4 mb-4">
    <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text mb-2">Overview</h3>
    <p class="text-sm text-gray-600 dark:text-dark-text-secondary leading-relaxed">
      This gateway provides an OpenAI-compatible API. You can use any OpenAI SDK or HTTP client to interact with your
      configured LLM providers. Models are accessed using the format <code
        class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-xs text-gray-700 dark:text-dark-text-secondary"
        >provider_key/model_name</code
      >.
    </p>
  </div>

  <!-- Endpoints -->
  <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface mb-4">
    <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border">
      <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text">Endpoints</h3>
    </div>
    <div class="p-4 space-y-3 text-sm">
      <div>
        <div class="flex items-center gap-2 mb-1">
          <span
            class="shrink-0 w-12 text-center px-2 py-0.5 text-xs bg-green-50 dark:bg-green-900/20 border border-green-200 dark:border-green-800 text-green-700 dark:text-green-300 font-medium font-mono"
            >POST</span
          >
          <code class="font-mono text-gray-700 dark:text-dark-text-secondary"
            >{baseUrl}/gateway/v1/chat/completions</code
          >
        </div>
        <p class="text-xs text-gray-500 dark:text-dark-text-muted ml-14">
          Send chat messages. Compatible with OpenAI Chat Completions API.
        </p>
      </div>
      <div>
        <div class="flex items-center gap-2 mb-1">
          <span
            class="shrink-0 w-12 text-center px-2 py-0.5 text-xs bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 text-blue-700 dark:text-blue-300 font-medium font-mono"
            >GET</span
          >
          <code class="font-mono text-gray-700 dark:text-dark-text-secondary">{baseUrl}/gateway/v1/models</code>
        </div>
        <p class="text-xs text-gray-500 dark:text-dark-text-muted ml-14">List all available models.</p>
      </div>
    </div>
  </div>

  <!-- Proxy Endpoint -->
  <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface mb-4">
    <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border">
      <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text">Proxy Endpoint</h3>
    </div>
    <div class="p-4 space-y-3 text-sm">
      <p class="text-gray-600 dark:text-dark-text-secondary mb-2">
        Access any provider endpoint directly via the gateway. The gateway handles authentication and credential
        injection.
      </p>
      <div>
        <div class="flex items-center gap-2 mb-1">
          <span
            class="shrink-0 w-12 text-center px-2 py-0.5 text-xs bg-gray-50 dark:bg-dark-elevated border border-gray-200 dark:border-dark-border text-gray-700 dark:text-dark-text-secondary font-medium font-mono"
            >ANY</span
          >
          <code class="font-mono text-gray-700 dark:text-dark-text-secondary"
            >{baseUrl}/gateway/proxy/:provider/:path*</code
          >
        </div>
        <div class="text-xs text-gray-500 dark:text-dark-text-muted ml-14 space-y-1">
          <p>Forwards requests to the specified provider.</p>
          <div class="mt-2 p-2 bg-gray-50 dark:bg-dark-base border border-gray-100 dark:border-dark-border">
            <p class="font-medium mb-1">Example: Gemini File Search</p>
            <p class="font-mono mb-1">POST {baseUrl}/gateway/proxy/gemini/v1beta/files</p>
            <p class="text-gray-400 dark:text-dark-text-muted">↓ forwards to</p>
            <p class="font-mono">https://generativelanguage.googleapis.com/v1beta/files</p>
          </div>
        </div>
      </div>
    </div>
  </div>

  <!-- Authentication -->
  <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface mb-4">
    <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border">
      <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text">Authentication</h3>
    </div>
    <div class="p-4 text-sm text-gray-600 dark:text-dark-text-secondary leading-relaxed">
      <p class="mb-2">
        Include your API token in the <code
          class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-xs text-gray-700 dark:text-dark-text-secondary"
          >Authorization</code
        > header:
      </p>
      <div class="relative">
        <pre
          class="bg-gray-50 dark:bg-dark-base border border-gray-200 dark:border-dark-border p-3 text-xs font-mono text-gray-700 dark:text-dark-text-secondary overflow-x-auto">Authorization: Bearer at_your_token_here</pre>
      </div>
      <p class="mt-2 text-xs text-gray-500 dark:text-dark-text-muted">
        Generate tokens from the <a
          href="#/tokens"
          class="text-gray-700 dark:text-accent-text underline underline-offset-2 hover:text-gray-900 dark:hover:text-accent"
          >Tokens</a
        > page. Tokens can optionally be scoped to specific providers or models.
      </p>
    </div>
  </div>

  <!-- Code Examples -->
  <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface mb-4">
    <details class="group">
      <summary class="flex items-center justify-between px-4 py-3 cursor-pointer select-none">
        <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text">Code Examples</h3>
        <div class="flex items-center gap-2">
          <button
            onclick={(e) => { e.preventDefault(); copyCode(activeTab, getActiveExample(activeTab, exampleModel, baseUrl)); }}
            class="flex items-center gap-1 text-xs text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
          >
            <Copy size={12} />
            {copiedId === activeTab ? 'Copied' : 'Copy'}
          </button>
          <span class="text-gray-400 group-open:rotate-180 transition-transform">▼</span>
        </div>
      </summary>

      <div class="border-t border-gray-200 dark:border-dark-border">
        <!-- Tabs -->
        <div class="flex border-b border-gray-200 dark:border-dark-border">
          {#each tabs as tab}
            <button
              onclick={() => (activeTab = tab.id)}
              class={[
                'px-4 py-2 text-xs font-medium transition-colors border-b-2 -mb-px',
                activeTab === tab.id
                  ? 'border-gray-900 text-gray-900 dark:border-accent dark:text-accent'
                  : 'border-transparent text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary',
              ]}
            >
              {tab.label}
            </button>
          {/each}
        </div>

        <!-- Tab content -->
        <pre
          class="p-4 text-xs font-mono text-gray-700 dark:text-dark-text-secondary overflow-x-auto leading-relaxed">{getActiveExample(
            activeTab,
            exampleModel,
            baseUrl
          )}</pre>
      </div>
    </details>
  </div>

  <!-- Opencode Config (collapsable) -->
  <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface mb-4">
    <details class="group">
      <summary class="flex items-center justify-between px-4 py-3 cursor-pointer select-none border-b border-gray-200 dark:border-dark-border group-[:not([open])]:border-b-0">
        <div class="flex items-center gap-2 min-w-0">
          <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text">Opencode Config</h3>
          <span
            class="px-1.5 py-0.5 text-[10px] font-mono bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted border border-gray-200 dark:border-dark-border truncate"
            >~/.config/opencode/opencode.json</span
          >
        </div>
        <div class="flex items-center gap-2 shrink-0">
          <button
            onclick={(e) => { e.preventDefault(); e.stopPropagation(); copyCode('opencode', opencodeConfig); }}
            class="flex items-center gap-1 text-xs text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
          >
            <Copy size={12} />
            {copiedId === 'opencode' ? 'Copied' : 'Copy'}
          </button>
          <span class="text-gray-400 group-open:rotate-180 transition-transform">▼</span>
        </div>
      </summary>
    <div class="p-4 space-y-4">
      <!-- Provider Selection -->
      <div class="flex flex-col sm:flex-row sm:items-end gap-4">
        <div class="space-y-2 flex-1">
          <label class="block text-xs text-gray-700 dark:text-dark-text-secondary">
            Filter by Provider
            <select
              value={selectedProviderKey}
              onchange={handleProviderChange}
              class="block w-full sm:w-64 text-sm border border-gray-300 dark:border-dark-border focus:border-gray-900 dark:focus:border-accent focus:ring-gray-900 dark:focus:ring-accent bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text mt-1"
            >
              <option value="">All Providers</option>
              {#each providers as p}
                <option value={p.key}>{p.key}</option>
              {/each}
            </select>
          </label>
        </div>
        <div class="flex gap-2">
          <button
            onclick={selectAll}
            class="flex items-center gap-1.5 px-3 py-2 text-xs text-gray-700 dark:text-dark-text-secondary bg-white dark:bg-dark-base border border-gray-300 dark:border-dark-border hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
          >
            <CheckSquare size={14} />
            Select All
          </button>
          <button
            onclick={selectNone}
            class="flex items-center gap-1.5 px-3 py-2 text-xs text-gray-700 dark:text-dark-text-secondary bg-white dark:bg-dark-base border border-gray-300 dark:border-dark-border hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
          >
            <Square size={14} />
            Select None
          </button>
        </div>
      </div>

      <!-- Models Selection -->
      {#each visibleProviders as p}
        <fieldset class="space-y-2">
          <legend class="block text-xs text-gray-700 dark:text-dark-text-secondary"
            >Models ({p.key})</legend
          >
          <div class="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-2">
            {#each p.models && p.models.length ? p.models : [p.default_model] as m}
              {@const fullId = `${p.key}/${m}`}
              <button
                onclick={() => toggleModel(fullId)}
                class={[
                  'flex items-center gap-2 px-2 py-1.5 text-xs border text-left transition-colors',
                  selectedModels.has(fullId)
                    ? 'bg-gray-900 text-white border-gray-900 dark:bg-accent dark:border-accent dark:text-white'
                    : 'bg-white dark:bg-dark-base text-gray-700 dark:text-dark-text-secondary border-gray-200 dark:border-dark-border hover:border-gray-300 dark:hover:border-dark-border-hover',
                ]}
              >
                <div
                  class={[
                    'w-3 h-3 border flex items-center justify-center transition-colors',
                    selectedModels.has(fullId)
                      ? 'bg-white border-transparent text-gray-900 dark:text-accent'
                      : 'border-gray-300 dark:border-dark-border',
                  ]}
                >
                  {#if selectedModels.has(fullId)}
                    <Check size={10} strokeWidth={4} />
                  {/if}
                </div>
                <span class="truncate flex-1">{m}</span>
              </button>
            {/each}
          </div>
        </fieldset>
      {/each}

      <!-- Output -->
      <details class="group border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
        <summary
          class="flex items-center justify-between p-3 cursor-pointer select-none text-xs font-medium text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated transition-colors"
        >
          <span>View Configuration JSON</span>
          <span class="text-gray-400 group-open:rotate-180 transition-transform">▼</span>
        </summary>
        <div class="relative border-t border-gray-200 dark:border-dark-border">
          <pre
            class="p-3 text-xs font-mono text-gray-700 dark:text-dark-text-secondary overflow-x-auto bg-white dark:bg-dark-base">{opencodeConfig}</pre>
        </div>
      </details>
    </div>
    </details>
  </div>

  <!-- Opencode MCP Config (collapsable) -->
  <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface mb-4">
    <details class="group">
      <summary class="flex items-center justify-between px-4 py-3 cursor-pointer select-none border-b border-gray-200 dark:border-dark-border group-[:not([open])]:border-b-0">
        <div class="flex items-center gap-2 min-w-0">
          <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text">MCP Configuration</h3>
          <span
            class="px-1.5 py-0.5 text-[10px] font-mono bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted border border-gray-200 dark:border-dark-border truncate"
            >~/.config/opencode/opencode.json</span
          >
        </div>
        <div class="flex items-center gap-2 shrink-0">
          <button
            onclick={(e) => { e.preventDefault(); e.stopPropagation(); copyCode('opencode-mcp', opencodeMcpConfig); }}
            class="flex items-center gap-1 text-xs text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
          >
            <Copy size={12} />
            {copiedId === 'opencode-mcp' ? 'Copied' : 'Copy'}
          </button>
          <span class="text-gray-400 group-open:rotate-180 transition-transform">▼</span>
        </div>
      </summary>
      <div class="p-4 space-y-3">
        <p class="text-xs text-gray-600 dark:text-dark-text-secondary leading-relaxed">
          Add an AT-hosted MCP server to opencode by extending your
          <code class="font-mono bg-gray-100 dark:bg-dark-elevated px-1 py-0.5 text-[11px]">opencode.json</code>
          with an <code class="font-mono bg-gray-100 dark:bg-dark-elevated px-1 py-0.5 text-[11px]">mcp</code> entry of type
          <code class="font-mono bg-gray-100 dark:bg-dark-elevated px-1 py-0.5 text-[11px]">remote</code>. Replace
          <code class="font-mono bg-gray-100 dark:bg-dark-elevated px-1 py-0.5 text-[11px]">at_xxxxx</code> with a token
          generated on the <a href="#/tokens" class="underline underline-offset-2">Tokens</a> page.
        </p>

        <!-- MCP server selector. Only shown when there's more than one server,
             otherwise the snippet just uses the single available server. -->
        {#if mcpServers.length > 1}
          <div class="flex flex-col sm:flex-row sm:items-end gap-2">
            <label class="block text-xs text-gray-700 dark:text-dark-text-secondary">
              MCP server
              <select
                bind:value={selectedMcpName}
                class="block w-full sm:w-64 text-sm border border-gray-300 dark:border-dark-border focus:border-gray-900 dark:focus:border-accent focus:ring-gray-900 dark:focus:ring-accent bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text mt-1"
              >
                {#each mcpServers as s}
                  <option value={s.name}>{s.name}</option>
                {/each}
              </select>
            </label>
          </div>
        {:else if mcpServers.length === 0}
          <p class="text-[11px] text-gray-500 dark:text-dark-text-muted italic">
            No MCP servers are configured yet — the example below uses
            <code class="font-mono">management</code> as a placeholder. Add servers on the
            <a href="#/mcp-servers" class="underline underline-offset-2">MCP Servers</a> page.
          </p>
        {/if}

        <!-- The JSON snippet -->
        <pre
          class="p-3 text-xs font-mono text-gray-700 dark:text-dark-text-secondary overflow-x-auto bg-gray-50 dark:bg-dark-base border border-gray-200 dark:border-dark-border">{opencodeMcpConfig}</pre>

        <p class="text-[11px] text-gray-500 dark:text-dark-text-muted">
          The full endpoint list is on the
          <a href="#/mcp-servers" class="underline underline-offset-2">MCP Servers</a> page. Each entry advertises its own
          <code class="font-mono">/gateway/v1/mcp/&lt;name&gt;</code> URL — point opencode at any of them.
        </p>
      </div>
    </details>
  </div>

  <!-- List Models -->
  <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface mb-4">
    <details class="group">
      <summary class="flex items-center justify-between px-4 py-3 cursor-pointer select-none">
        <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text">List Models</h3>
        <div class="flex items-center gap-2">
          <button
            onclick={(e) => { e.preventDefault(); copyCode('models', curlModelsExample(baseUrl)); }}
            class="flex items-center gap-1 text-xs text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
          >
            <Copy size={12} />
            {copiedId === 'models' ? 'Copied' : 'Copy'}
          </button>
          <span class="text-gray-400 group-open:rotate-180 transition-transform">▼</span>
        </div>
      </summary>
      <div class="border-t border-gray-200 dark:border-dark-border">
        <pre class="p-4 text-xs font-mono text-gray-700 dark:text-dark-text-secondary overflow-x-auto leading-relaxed">{curlModelsExample(baseUrl)}</pre>
      </div>
    </details>
  </div>

  <!-- Available Models (collapsable) -->
  <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface overflow-hidden">
    <details class="group">
      <summary class="flex items-center justify-between px-4 py-3 cursor-pointer select-none border-b border-gray-200 dark:border-dark-border group-[:not([open])]:border-b-0">
        <div class="flex items-center gap-2">
          <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text">Available Models</h3>
          {#if !loading}
            <span class="text-xs text-gray-400 dark:text-dark-text-muted">({allModels.length})</span>
          {/if}
        </div>
        <span class="text-gray-400 group-open:rotate-180 transition-transform">▼</span>
      </summary>
      {#if loading}
        <div class="px-4 py-10 text-center text-gray-400 dark:text-dark-text-muted text-sm">Loading...</div>
      {:else if allModels.length === 0}
        <div class="px-4 py-10 text-center text-gray-400 dark:text-dark-text-muted text-sm">No models available</div>
      {:else}
        <div class="p-4">
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-1.5">
            {#each allModels as model}
              <div class="flex items-center gap-2 group/row">
                <code class="text-xs font-mono text-gray-700 dark:text-dark-text-secondary bg-gray-50 dark:bg-dark-elevated px-2 py-1 flex-1 truncate">{model}</code>
                <button
                  onclick={() => copyCode(`model-${model}`, model)}
                  class="shrink-0 p-1 text-gray-300 dark:text-dark-text-faint hover:text-gray-500 dark:hover:text-dark-text-muted transition-colors opacity-0 group-hover/row:opacity-100"
                  title="Copy model ID"
                >
                  <Copy size={12} />
                </button>
              </div>
            {/each}
          </div>
        </div>
      {/if}
    </details>
  </div>
</div>