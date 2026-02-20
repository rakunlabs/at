<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { getInfo, type InfoProvider } from '@/lib/api/gateway';
  import { Send, Trash2, ChevronDown, Square, Settings } from 'lucide-svelte';

  storeNavbar.title = 'Test';

  // ─── State ───

  interface ChatMessage {
    role: 'user' | 'assistant' | 'system';
    content: string;
  }

  let providers = $state<InfoProvider[]>([]);
  let models = $state<string[]>([]);
  let selectedModel = $state('');
  let authToken = $state('');
  let systemPrompt = $state('');
  let userInput = $state('');
  let messages = $state<ChatMessage[]>([]);
  let loading = $state(true);
  let streaming = $state(false);
  let abortController = $state<AbortController | null>(null);
  let chatContainer: HTMLDivElement | undefined = $state();
  let showSystemPrompt = $state(false);

  // ─── Load providers/models ───

  async function loadInfo() {
    loading = true;
    try {
      const info = await getInfo();
      providers = info.providers;

      // Build full model list: provider_key/model
      const allModels: string[] = [];
      for (const p of info.providers) {
        if (p.models && p.models.length > 0) {
          for (const m of p.models) {
            allModels.push(`${p.key}/${m}`);
          }
        } else if (p.default_model) {
          allModels.push(`${p.key}/${p.default_model}`);
        }
      }
      models = allModels;
      if (allModels.length > 0 && !selectedModel) {
        selectedModel = allModels[0];
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load provider info', 'alert');
    } finally {
      loading = false;
    }
  }

  loadInfo();

  // ─── Scroll ───

  function scrollToBottom() {
    if (chatContainer) {
      requestAnimationFrame(() => {
        chatContainer!.scrollTop = chatContainer!.scrollHeight;
      });
    }
  }

  // ─── Send message ───

  async function sendMessage() {
    const text = userInput.trim();
    if (!text || !selectedModel) return;
    if (streaming) return;

    // Add user message
    messages = [...messages, { role: 'user', content: text }];
    userInput = '';
    scrollToBottom();

    // Build request body
    const reqMessages: { role: string; content: string }[] = [];
    if (systemPrompt.trim()) {
      reqMessages.push({ role: 'system', content: systemPrompt.trim() });
    }
    for (const m of messages) {
      reqMessages.push({ role: m.role, content: m.content });
    }

    const body = {
      model: selectedModel,
      messages: reqMessages,
      stream: true,
    };

    // Add assistant placeholder
    messages = [...messages, { role: 'assistant', content: '' }];
    streaming = true;
    const controller = new AbortController();
    abortController = controller;

    try {
      const headers: Record<string, string> = {
        'Content-Type': 'application/json',
      };
      if (authToken.trim()) {
        headers['Authorization'] = `Bearer ${authToken.trim()}`;
      }

      const response = await fetch('/v1/chat/completions', {
        method: 'POST',
        headers,
        body: JSON.stringify(body),
        signal: controller.signal,
      });

      if (!response.ok) {
        const errBody = await response.text();
        let errMsg = `HTTP ${response.status}`;
        try {
          const errJson = JSON.parse(errBody);
          errMsg = errJson?.error?.message || errMsg;
        } catch {
          errMsg = errBody || errMsg;
        }
        throw new Error(errMsg);
      }

      // Read SSE stream
      const reader = response.body?.getReader();
      if (!reader) throw new Error('No response body');

      const decoder = new TextDecoder();
      let buffer = '';

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split('\n');
        buffer = lines.pop() || '';

        for (const line of lines) {
          const trimmed = line.trim();
          if (!trimmed || !trimmed.startsWith('data: ')) continue;

          const data = trimmed.slice(6);
          if (data === '[DONE]') continue;

          try {
            const chunk = JSON.parse(data);
            const delta = chunk.choices?.[0]?.delta;
            if (delta?.content) {
              // Update the last assistant message
              const lastIdx = messages.length - 1;
              messages[lastIdx] = {
                ...messages[lastIdx],
                content: messages[lastIdx].content + delta.content,
              };
              scrollToBottom();
            }
          } catch {
            // Skip unparseable chunks
          }
        }
      }
    } catch (e: any) {
      if (e.name === 'AbortError') {
        // User cancelled — don't show error
      } else {
        addToast(e.message || 'Chat request failed', 'alert');
        // Remove empty assistant message on error
        const lastIdx = messages.length - 1;
        if (messages[lastIdx]?.role === 'assistant' && !messages[lastIdx]?.content) {
          messages = messages.slice(0, -1);
        }
      }
    } finally {
      streaming = false;
      abortController = null;
    }
  }

  function stopStreaming() {
    if (abortController) {
      abortController.abort();
    }
  }

  function clearChat() {
    messages = [];
    systemPrompt = '';
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
    }
  }
</script>

<div class="flex flex-col h-full">
  <!-- Toolbar -->
  <div class="border-b border-gray-200 bg-white px-4 py-2 flex items-center gap-2 shrink-0">
    <!-- Model selector -->
    <div class="relative flex-1 max-w-xs">
      <select
        bind:value={selectedModel}
        disabled={loading || models.length === 0}
        class="w-full border border-gray-300 px-3 py-1.5 text-sm appearance-none bg-white pr-8 focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 disabled:bg-gray-50 disabled:text-gray-400 transition-colors"
      >
        {#if models.length === 0}
          <option value="">No models available</option>
        {/if}
        {#each models as model}
          <option value={model}>{model}</option>
        {/each}
      </select>
      <ChevronDown size={14} class="absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none text-gray-400" />
    </div>

    <!-- Auth token -->
    <input
      type="password"
      bind:value={authToken}
      placeholder="Auth token (optional)"
      class="border border-gray-300 px-3 py-1.5 text-sm w-40 focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
    />

    <!-- System prompt toggle -->
    <button
      onclick={() => (showSystemPrompt = !showSystemPrompt)}
      class="p-1.5 border border-gray-300 hover:bg-gray-50 text-gray-500 hover:text-gray-700 transition-colors"
      class:bg-gray-900={showSystemPrompt}
      class:text-white={showSystemPrompt}
      class:border-gray-900={showSystemPrompt}
      class:hover:bg-gray-800={showSystemPrompt}
      class:hover:text-white={showSystemPrompt}
      title="System prompt"
    >
      <Settings size={14} />
    </button>

    <!-- Clear -->
    <button
      onclick={clearChat}
      disabled={messages.length === 0 && !systemPrompt}
      class="p-1.5 hover:bg-red-50 text-gray-400 hover:text-red-600 disabled:opacity-30 disabled:hover:bg-transparent disabled:hover:text-gray-400 transition-colors"
      title="Clear chat"
    >
      <Trash2 size={14} />
    </button>
  </div>

  <!-- System prompt -->
  {#if showSystemPrompt}
    <div class="border-b border-gray-200 bg-gray-50/50 px-4 py-2.5 shrink-0">
      <textarea
        bind:value={systemPrompt}
        placeholder="System prompt (optional)"
        rows={2}
        class="w-full border border-gray-300 px-3 py-1.5 text-sm resize-y focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
      ></textarea>
    </div>
  {/if}

  <!-- Chat messages -->
  <div
    bind:this={chatContainer}
    class="flex-1 overflow-y-auto px-4 py-4 space-y-4"
  >
    {#if loading}
      <div class="text-center py-12 text-gray-400 text-sm">Loading providers...</div>
    {:else if models.length === 0}
      <div class="text-center py-12">
        <div class="text-gray-400 mb-2">No providers configured</div>
        <div class="text-xs text-gray-400">
          Add providers on the <a href="#/providers" class="underline underline-offset-2 hover:text-gray-700 transition-colors">Providers</a> page first.
        </div>
      </div>
    {:else if messages.length === 0}
      <div class="text-center py-12">
        <div class="text-gray-400 mb-1.5">Send a message to start chatting</div>
        <div class="text-xs text-gray-400">
          Using <code class="font-mono bg-gray-100 px-1.5 py-0.5 text-gray-600">{selectedModel}</code>
        </div>
      </div>
    {:else}
      {#each messages as msg, i}
        <div class="flex {msg.role === 'user' ? 'justify-end' : 'justify-start'}">
          <div
            class="max-w-[75%] px-4 py-2.5 text-sm whitespace-pre-wrap leading-relaxed {msg.role === 'user'
              ? 'bg-gray-900 text-white'
              : 'bg-white border border-gray-200 shadow-sm text-gray-800'}"
          >
            {#if msg.role === 'assistant' && !msg.content && streaming && i === messages.length - 1}
              <span class="text-gray-400 italic">Thinking...</span>
            {:else}
              {msg.content}
            {/if}
          </div>
        </div>
      {/each}
    {/if}
  </div>

  <!-- Input area -->
  <div class="border-t border-gray-200 bg-white px-4 py-3 shrink-0">
    <div class="flex gap-2">
      <textarea
        bind:value={userInput}
        onkeydown={handleKeydown}
        placeholder={models.length === 0 ? 'No models available' : 'Type a message... (Enter to send, Shift+Enter for new line)'}
        disabled={models.length === 0}
        rows={1}
        class="flex-1 border border-gray-300 px-4 py-2 text-sm resize-none focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 disabled:bg-gray-50 disabled:text-gray-400 transition-colors"
      ></textarea>
      {#if streaming}
        <button
          onclick={stopStreaming}
          class="px-3 py-2 bg-red-600 text-white hover:bg-red-700 flex items-center gap-1.5 transition-colors"
          title="Stop"
        >
          <Square size={14} />
        </button>
      {:else}
        <button
          onclick={sendMessage}
          disabled={!userInput.trim() || !selectedModel || models.length === 0}
          class="px-3 py-2 bg-gray-900 text-white hover:bg-gray-800 disabled:opacity-30 disabled:hover:bg-gray-900 flex items-center gap-1.5 transition-colors"
          title="Send"
        >
          <Send size={14} />
        </button>
      {/if}
    </div>
  </div>
</div>
