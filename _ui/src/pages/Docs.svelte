<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { getInfo, type InfoProvider } from '@/lib/api/gateway';
  import { addToast } from '@/lib/store/toast.svelte';
  import { Copy, BookOpen, RefreshCw } from 'lucide-svelte';

  storeNavbar.title = 'Documentation';

  let providers = $state<InfoProvider[]>([]);
  let loading = $state(true);
  let copiedId = $state<string | null>(null);
  let activeTab = $state('python');

  const tabs = [
    { id: 'python', label: 'Python' },
    { id: 'js', label: 'JavaScript' },
    { id: 'go', label: 'Go' },
    { id: 'curl', label: 'curl' },
  ];

  async function load() {
    loading = true;
    try {
      const info = await getInfo();
      providers = info.providers;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load info', 'alert');
    } finally {
      loading = false;
    }
  }

  load();

  let baseUrl = $derived(
    (window.location.origin + window.location.pathname).replace(/\/+$/, '')
  );

  let allModels = $derived(
    providers.flatMap((p) => {
      if (p.models && p.models.length > 0) {
        return p.models.map((m) => `${p.key}/${m}`);
      }
      return [`${p.key}/${p.default_model}`];
    })
  );

  let exampleModel = $derived(allModels.length > 0 ? allModels[0] : 'provider/model-name');

  // Native proxy: providers that support native passthrough (gemini, anthropic).
  let nativeProviders = $derived(
    providers.filter((p) => p.type === 'gemini' || p.type === 'anthropic')
  );

  // Pick example providers for native proxy docs.
  let exampleGeminiProvider = $derived(
    providers.find((p) => p.type === 'gemini')
  );
  let exampleAnthropicProvider = $derived(
    providers.find((p) => p.type === 'anthropic')
  );

  function copyCode(id: string, text: string) {
    navigator.clipboard.writeText(text);
    copiedId = id;
    setTimeout(() => (copiedId = null), 2000);
  }

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

  function curlNativeGeminiExample(url: string, providerKey: string, model: string): string {
    return `curl ${url}/gateway/v1/native/${providerKey}/v1beta/models/${model}:generateContent \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer at_your_token_here" \\
  -d '{
    "contents": [
      {"role": "user", "parts": [{"text": "Hello!"}]}
    ]
  }'`;
  }

  function curlNativeGeminiStreamExample(url: string, providerKey: string, model: string): string {
    return `curl ${url}/gateway/v1/native/${providerKey}/v1beta/models/${model}:streamGenerateContent?alt=sse \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer at_your_token_here" \\
  -d '{
    "contents": [
      {"role": "user", "parts": [{"text": "Hello!"}]}
    ]
  }'`;
  }

  function curlNativeAnthropicExample(url: string, providerKey: string, model: string): string {
    return `curl ${url}/gateway/v1/native/${providerKey}/v1/messages \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer at_your_token_here" \\
  -d '{
    "model": "${model}",
    "max_tokens": 1024,
    "messages": [
      {"role": "user", "content": "Hello!"}
    ]
  }'`;
  }

  function curlNativeAnthropicStreamExample(url: string, providerKey: string, model: string): string {
    return `curl ${url}/gateway/v1/native/${providerKey}/v1/messages \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer at_your_token_here" \\
  -d '{
    "model": "${model}",
    "max_tokens": 1024,
    "stream": true,
    "messages": [
      {"role": "user", "content": "Hello!"}
    ]
  }'`;
  }

  function getActiveExample(tab: string, model: string, url: string): string {
    switch (tab) {
      case 'python': return pythonExample(model, url);
      case 'js': return jsExample(model, url);
      case 'go': return goExample(model, url);
      case 'curl': return curlExample(model, url);
      default: return '';
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
      <BookOpen size={16} class="text-gray-500" />
      <h2 class="text-sm font-medium text-gray-900">API Documentation</h2>
    </div>
    <button
      onclick={load}
      class="p-1.5 hover:bg-gray-100 text-gray-400 hover:text-gray-600 transition-colors"
      title="Refresh"
    >
      <RefreshCw size={14} />
    </button>
  </div>

  <!-- Overview -->
  <div class="border border-gray-200 bg-white p-4 mb-4">
    <h3 class="text-sm font-medium text-gray-900 mb-2">Overview</h3>
    <p class="text-sm text-gray-600 leading-relaxed">
      This gateway provides an OpenAI-compatible API. You can use any OpenAI SDK or HTTP client to interact with your configured LLM providers.
      Models are accessed using the format <code class="font-mono bg-gray-100 px-1.5 py-0.5 text-xs text-gray-700">provider_key/model_name</code>.
    </p>
  </div>

  <!-- Endpoints -->
  <div class="border border-gray-200 bg-white mb-4">
    <div class="px-4 py-3 border-b border-gray-200">
      <h3 class="text-sm font-medium text-gray-900">Endpoints</h3>
    </div>
    <div class="p-4 space-y-3 text-sm">
      <div>
        <div class="flex items-center gap-2 mb-1">
          <span class="shrink-0 w-12 text-center px-2 py-0.5 text-xs bg-green-50 border border-green-200 text-green-700 font-medium font-mono">POST</span>
          <code class="font-mono text-gray-700">{baseUrl}/gateway/v1/chat/completions</code>
        </div>
        <p class="text-xs text-gray-500 ml-14">Send chat messages. Compatible with OpenAI Chat Completions API.</p>
      </div>
      <div>
        <div class="flex items-center gap-2 mb-1">
          <span class="shrink-0 w-12 text-center px-2 py-0.5 text-xs bg-blue-50 border border-blue-200 text-blue-700 font-medium font-mono">GET</span>
          <code class="font-mono text-gray-700">{baseUrl}/gateway/v1/models</code>
        </div>
        <p class="text-xs text-gray-500 ml-14">List all available models.</p>
      </div>
      <div>
        <div class="flex items-center gap-2 mb-1">
          <span class="shrink-0 w-12 text-center px-2 py-0.5 text-xs bg-amber-50 border border-amber-200 text-amber-700 font-medium font-mono">POST</span>
          <code class="font-mono text-gray-700">{baseUrl}/gateway/v1/native/<span class="text-amber-600">{'{provider_key}'}</span>/...</code>
        </div>
        <p class="text-xs text-gray-500 ml-14">Native proxy. Passes requests unchanged to the upstream provider's API (Gemini, Anthropic).</p>
      </div>
    </div>
  </div>

  <!-- Authentication -->
  <div class="border border-gray-200 bg-white mb-4">
    <div class="px-4 py-3 border-b border-gray-200">
      <h3 class="text-sm font-medium text-gray-900">Authentication</h3>
    </div>
    <div class="p-4 text-sm text-gray-600 leading-relaxed">
      <p class="mb-2">Include your API token in the <code class="font-mono bg-gray-100 px-1.5 py-0.5 text-xs text-gray-700">Authorization</code> header:</p>
      <div class="relative">
        <pre class="bg-gray-50 border border-gray-200 p-3 text-xs font-mono text-gray-700 overflow-x-auto">Authorization: Bearer at_your_token_here</pre>
      </div>
      <p class="mt-2 text-xs text-gray-500">
        Generate tokens from the <a href="#/tokens" class="text-gray-700 underline underline-offset-2 hover:text-gray-900">Tokens</a> page.
        Tokens can optionally be scoped to specific providers or models.
      </p>
    </div>
  </div>

  <!-- Code Examples -->
  <div class="border border-gray-200 bg-white mb-4">
    <div class="px-4 py-3 border-b border-gray-200 flex items-center justify-between">
      <h3 class="text-sm font-medium text-gray-900">Code Examples</h3>
      <button
        onclick={() => copyCode(activeTab, getActiveExample(activeTab, exampleModel, baseUrl))}
        class="flex items-center gap-1 text-xs text-gray-400 hover:text-gray-600 transition-colors"
      >
        <Copy size={12} />
        {copiedId === activeTab ? 'Copied' : 'Copy'}
      </button>
    </div>

    <!-- Tabs -->
    <div class="flex border-b border-gray-200">
      {#each tabs as tab}
        <button
          onclick={() => (activeTab = tab.id)}
          class={[
            'px-4 py-2 text-xs font-medium transition-colors border-b-2 -mb-px',
            activeTab === tab.id
              ? 'border-gray-900 text-gray-900'
              : 'border-transparent text-gray-400 hover:text-gray-600'
          ]}
        >
          {tab.label}
        </button>
      {/each}
    </div>

    <!-- Tab content -->
    <pre class="p-4 text-xs font-mono text-gray-700 overflow-x-auto leading-relaxed">{getActiveExample(activeTab, exampleModel, baseUrl)}</pre>
  </div>

  <!-- List Models -->
  <div class="border border-gray-200 bg-white mb-4">
    <div class="px-4 py-3 border-b border-gray-200 flex items-center justify-between">
      <h3 class="text-sm font-medium text-gray-900">List Models</h3>
      <button
        onclick={() => copyCode('models', curlModelsExample(baseUrl))}
        class="flex items-center gap-1 text-xs text-gray-400 hover:text-gray-600 transition-colors"
      >
        <Copy size={12} />
        {copiedId === 'models' ? 'Copied' : 'Copy'}
      </button>
    </div>
    <pre class="p-4 text-xs font-mono text-gray-700 overflow-x-auto leading-relaxed">{curlModelsExample(baseUrl)}</pre>
  </div>

  <!-- Native Proxy -->
  <div class="border border-gray-200 bg-white mb-4">
    <div class="px-4 py-3 border-b border-gray-200">
      <h3 class="text-sm font-medium text-gray-900">Native Proxy</h3>
    </div>
    <div class="p-4 text-sm text-gray-600 leading-relaxed">
      <p class="mb-3">
        The native proxy passes requests <strong>unchanged</strong> to the upstream provider's API.
        Use this when you need full access to provider-specific features (thinking, grounding, caching, etc.)
        without the OpenAI translation layer.
      </p>
      <p class="mb-3 text-xs text-gray-500">
        URL pattern: <code class="font-mono bg-gray-100 px-1.5 py-0.5 text-gray-700">/gateway/v1/native/{'{provider_key}'}/{'{upstream_path}'}</code>
      </p>
      <p class="text-xs text-gray-500">
        The gateway authenticates with your token, checks model access, then proxies the request to the upstream provider with its own API key.
        Supported provider types: <code class="font-mono bg-gray-100 px-1.5 py-0.5 text-gray-700">gemini</code>, <code class="font-mono bg-gray-100 px-1.5 py-0.5 text-gray-700">anthropic</code>.
      </p>
    </div>

    {#if nativeProviders.length > 0}
      <div class="border-t border-gray-200">
        {#if exampleGeminiProvider}
          {@const geminiModel = exampleGeminiProvider.models && exampleGeminiProvider.models.length > 0 ? exampleGeminiProvider.models[0] : exampleGeminiProvider.default_model}
          <div class="p-4 border-b border-gray-100">
            <div class="flex items-center justify-between mb-2">
              <div class="flex items-center gap-2">
                <span class="px-2 py-0.5 text-xs bg-gray-100 text-gray-600 font-mono">gemini</span>
                <span class="text-xs text-gray-500">provider: <code class="font-mono text-gray-700">{exampleGeminiProvider.key}</code></span>
              </div>
              <button
                onclick={() => copyCode('native-gemini', curlNativeGeminiExample(baseUrl, exampleGeminiProvider!.key, geminiModel))}
                class="flex items-center gap-1 text-xs text-gray-400 hover:text-gray-600 transition-colors"
              >
                <Copy size={12} />
                {copiedId === 'native-gemini' ? 'Copied' : 'Copy'}
              </button>
            </div>
            <pre class="bg-gray-50 border border-gray-200 p-3 text-xs font-mono text-gray-700 overflow-x-auto leading-relaxed">{curlNativeGeminiExample(baseUrl, exampleGeminiProvider.key, geminiModel)}</pre>
            <div class="mt-2 flex items-center justify-between">
              <span class="text-xs text-gray-400">Streaming:</span>
              <button
                onclick={() => copyCode('native-gemini-stream', curlNativeGeminiStreamExample(baseUrl, exampleGeminiProvider!.key, geminiModel))}
                class="flex items-center gap-1 text-xs text-gray-400 hover:text-gray-600 transition-colors"
              >
                <Copy size={12} />
                {copiedId === 'native-gemini-stream' ? 'Copied' : 'Copy'}
              </button>
            </div>
            <pre class="bg-gray-50 border border-gray-200 p-3 text-xs font-mono text-gray-700 overflow-x-auto leading-relaxed mt-1">{curlNativeGeminiStreamExample(baseUrl, exampleGeminiProvider.key, geminiModel)}</pre>
          </div>
        {/if}

        {#if exampleAnthropicProvider}
          {@const anthropicModel = exampleAnthropicProvider.models && exampleAnthropicProvider.models.length > 0 ? exampleAnthropicProvider.models[0] : exampleAnthropicProvider.default_model}
          <div class="p-4">
            <div class="flex items-center justify-between mb-2">
              <div class="flex items-center gap-2">
                <span class="px-2 py-0.5 text-xs bg-gray-100 text-gray-600 font-mono">anthropic</span>
                <span class="text-xs text-gray-500">provider: <code class="font-mono text-gray-700">{exampleAnthropicProvider.key}</code></span>
              </div>
              <button
                onclick={() => copyCode('native-anthropic', curlNativeAnthropicExample(baseUrl, exampleAnthropicProvider!.key, anthropicModel))}
                class="flex items-center gap-1 text-xs text-gray-400 hover:text-gray-600 transition-colors"
              >
                <Copy size={12} />
                {copiedId === 'native-anthropic' ? 'Copied' : 'Copy'}
              </button>
            </div>
            <pre class="bg-gray-50 border border-gray-200 p-3 text-xs font-mono text-gray-700 overflow-x-auto leading-relaxed">{curlNativeAnthropicExample(baseUrl, exampleAnthropicProvider.key, anthropicModel)}</pre>
            <div class="mt-2 flex items-center justify-between">
              <span class="text-xs text-gray-400">Streaming:</span>
              <button
                onclick={() => copyCode('native-anthropic-stream', curlNativeAnthropicStreamExample(baseUrl, exampleAnthropicProvider!.key, anthropicModel))}
                class="flex items-center gap-1 text-xs text-gray-400 hover:text-gray-600 transition-colors"
              >
                <Copy size={12} />
                {copiedId === 'native-anthropic-stream' ? 'Copied' : 'Copy'}
              </button>
            </div>
            <pre class="bg-gray-50 border border-gray-200 p-3 text-xs font-mono text-gray-700 overflow-x-auto leading-relaxed mt-1">{curlNativeAnthropicStreamExample(baseUrl, exampleAnthropicProvider.key, anthropicModel)}</pre>
          </div>
        {/if}
      </div>
    {:else}
      <div class="border-t border-gray-200 p-4 text-xs text-gray-400 text-center">
        No Gemini or Anthropic providers configured. Add one from the <a href="#/providers" class="text-gray-500 underline underline-offset-2 hover:text-gray-700">Providers</a> page.
      </div>
    {/if}
  </div>

  <!-- Available Models -->
  <div class="border border-gray-200 bg-white shadow-sm overflow-hidden">
    <div class="px-4 py-3 border-b border-gray-200">
      <h3 class="text-sm font-medium text-gray-900">Available Models</h3>
    </div>
    {#if loading}
      <div class="px-4 py-10 text-center text-gray-400 text-sm">Loading...</div>
    {:else if allModels.length === 0}
      <div class="px-4 py-10 text-center text-gray-400 text-sm">No models available</div>
    {:else}
      <div class="p-4">
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-1.5">
          {#each allModels as model}
            <div class="flex items-center gap-2 group">
              <code class="text-xs font-mono text-gray-700 bg-gray-50 px-2 py-1 flex-1 truncate">{model}</code>
              <button
                onclick={() => copyCode(`model-${model}`, model)}
                class="shrink-0 p-1 text-gray-300 hover:text-gray-500 transition-colors opacity-0 group-hover:opacity-100"
                title="Copy model ID"
              >
                <Copy size={12} />
              </button>
            </div>
          {/each}
        </div>
      </div>
    {/if}
  </div>
</div>
