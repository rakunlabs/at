<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { getInfo } from '@/lib/api/gateway';
  import {
    type ContentPart,
    type ChatMessage,
    getTextContent,
    mergeDeltaContent,
    streamChatCompletion,
  } from '@/lib/helper/chat';
  import { Send, Trash2, ChevronDown, Square, Settings, ImagePlus, X, RotateCcw } from 'lucide-svelte';

  storeNavbar.title = 'Chat';

  // ─── Types ───

  interface PendingImage {
    name: string;
    dataUrl: string;
  }

  // ─── State ───

  let models = $state<string[]>([]);
  let selectedModel = $state('');
  let systemPrompt = $state('');
  let userInput = $state('');
  let messages = $state<ChatMessage[]>([]);
  let loading = $state(true);
  let streaming = $state(false);
  let abortController = $state<AbortController | null>(null);
  let chatContainer: HTMLDivElement | undefined = $state();
  let showSystemPrompt = $state(false);
  let pendingImages = $state<PendingImage[]>([]);
  let fileInput: HTMLInputElement | undefined = $state();
  let dragging = $state(false);

  // ─── Load providers/models ───

  async function loadInfo() {
    loading = true;
    try {
      const info = await getInfo();

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

  // ─── Image handling ───

  function readFileAsDataURL(file: File): Promise<string> {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(reader.result as string);
      reader.onerror = () => reject(new Error('Failed to read file'));
      reader.readAsDataURL(file);
    });
  }

  async function addImageFiles(files: FileList | File[]) {
    for (const file of files) {
      if (!file.type.startsWith('image/')) continue;
      if (file.size > 20 * 1024 * 1024) {
        addToast(`Image "${file.name}" is too large (max 20MB)`, 'alert');
        continue;
      }
      try {
        const dataUrl = await readFileAsDataURL(file);
        pendingImages = [...pendingImages, { name: file.name, dataUrl }];
      } catch {
        addToast(`Failed to read "${file.name}"`, 'alert');
      }
    }
  }

  function removeImage(index: number) {
    pendingImages = pendingImages.filter((_, i) => i !== index);
  }

  function handlePaste(e: ClipboardEvent) {
    const items = e.clipboardData?.items;
    if (!items) return;

    const imageFiles: File[] = [];
    for (const item of items) {
      if (item.type.startsWith('image/')) {
        const file = item.getAsFile();
        if (file) imageFiles.push(file);
      }
    }
    if (imageFiles.length > 0) {
      e.preventDefault();
      addImageFiles(imageFiles);
    }
  }

  function handleFilePick(e: Event) {
    const input = e.target as HTMLInputElement;
    if (input.files && input.files.length > 0) {
      addImageFiles(input.files);
      input.value = '';
    }
  }

  function handleDragOver(e: DragEvent) {
    e.preventDefault();
    dragging = true;
  }

  function handleDragLeave(e: DragEvent) {
    e.preventDefault();
    dragging = false;
  }

  function handleDrop(e: DragEvent) {
    e.preventDefault();
    dragging = false;
    if (e.dataTransfer?.files) {
      addImageFiles(e.dataTransfer.files);
    }
  }

  // ─── Send message ───

  async function sendMessage() {
    const text = userInput.trim();
    if ((!text && pendingImages.length === 0) || !selectedModel) return;
    if (streaming) return;

    // Build user message content
    let userContent: string | ContentPart[];
    if (pendingImages.length > 0) {
      const parts: ContentPart[] = [];
      for (const img of pendingImages) {
        parts.push({ type: 'image_url', image_url: { url: img.dataUrl } });
      }
      if (text) {
        parts.push({ type: 'text', text });
      }
      userContent = parts;
    } else {
      userContent = text;
    }

    // Add user message to chat
    messages = [...messages, { role: 'user', content: userContent }];
    userInput = '';
    pendingImages = [];
    scrollToBottom();

    await streamAssistantReply();
  }

  /** Build request messages from current state and stream a reply. */
  async function streamAssistantReply() {
    const reqMessages: { role: string; content: any }[] = [];
    if (systemPrompt.trim()) {
      reqMessages.push({ role: 'system', content: systemPrompt.trim() });
    }
    for (const m of messages) {
      reqMessages.push({ role: m.role, content: m.content });
    }

    // Add assistant placeholder
    messages = [...messages, { role: 'assistant', content: '' }];
    streaming = true;
    const controller = new AbortController();
    abortController = controller;

    try {
      await streamChatCompletion(
        'api/v1/chat/completions',
        {
          model: selectedModel,
          messages: reqMessages,
          stream: true,
        },
        {
          onDelta: (deltaContent) => {
            const lastIdx = messages.length - 1;
            const prev = messages[lastIdx];
            messages[lastIdx] = {
              ...prev,
              content: mergeDeltaContent(prev.content, deltaContent),
            };
            scrollToBottom();
          },
          onToolCalls: () => {},
          onError: (error) => {
            addToast(error, 'alert');
          },
        },
        controller.signal,
      );
    } catch (e: any) {
      if (e.name === 'AbortError') {
        // User cancelled — don't show error
      } else {
        addToast(e.message || 'Chat request failed', 'alert');
        // Remove empty assistant message on error
        const lastIdx = messages.length - 1;
        if (messages[lastIdx]?.role === 'assistant' && !getTextContent(messages[lastIdx].content)) {
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
    pendingImages = [];
  }

  /** Retry from a specific user message index.
   *  Keeps messages up to and including the user message at `index`,
   *  removes everything after it, then re-sends to get a fresh response. */
  async function retryFromIndex(index: number) {
    if (streaming) return;
    // Keep messages up to and including the user message
    messages = messages.slice(0, index + 1);
    scrollToBottom();
    await streamAssistantReply();
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
    }
  }
</script>

<svelte:head>
  <title>AT | Chat</title>
</svelte:head>

<div
  class="flex flex-col h-full"
  ondragover={handleDragOver}
  ondragleave={handleDragLeave}
  ondrop={handleDrop}
  role="application"
>
  <!-- Drag overlay -->
  {#if dragging}
    <div class="absolute inset-0 z-50 bg-gray-900/10 border-2 border-dashed border-gray-400 flex items-center justify-center pointer-events-none">
      <div class="bg-white px-4 py-2 text-sm text-gray-600 shadow-sm">Drop images here</div>
    </div>
  {/if}

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
      disabled={messages.length === 0 && !systemPrompt && pendingImages.length === 0}
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
          <div class="max-w-[75%]">
            <div
              class="px-4 py-2.5 text-sm leading-relaxed {msg.role === 'user'
                ? 'bg-gray-900 text-white'
                : 'bg-white border border-gray-200 shadow-sm text-gray-800'}"
            >
              <!-- Images & Text -->
              {#if typeof msg.content === 'string'}
                {#if msg.role === 'assistant' && !msg.content && streaming && i === messages.length - 1}
                  <span class="text-gray-400 italic">Thinking...</span>
                {:else}
                  <span class="whitespace-pre-wrap">{msg.content}</span>
                {/if}
              {:else}
                {#each msg.content as part}
                  {#if part.type === 'image_url' && part.image_url?.url}
                    <img
                      src={part.image_url.url}
                      alt=""
                      class="max-w-full max-h-64 mb-2 border {msg.role === 'user' ? 'border-gray-600' : 'border-gray-200'}"
                    />
                  {:else if part.type === 'text' && part.text}
                    <span class="whitespace-pre-wrap">{part.text}</span>
                  {/if}
                {/each}
              {/if}
            </div>
            <!-- Retry button for user messages -->
            {#if msg.role === 'user' && !streaming}
              <div class="mt-1 flex justify-end">
                <button
                  onclick={() => retryFromIndex(i)}
                  class="text-xs text-gray-400 hover:text-gray-700 flex items-center gap-1 transition-colors"
                  title="Retry from this message"
                >
                  <RotateCcw size={11} />
                  Retry
                </button>
              </div>
            {/if}
          </div>
        </div>
      {/each}
    {/if}
  </div>

  <!-- Input area -->
  <div class="border-t border-gray-200 bg-white px-4 py-3 shrink-0">
    <!-- Pending image previews -->
    {#if pendingImages.length > 0}
      <div class="flex gap-2 mb-2 flex-wrap">
        {#each pendingImages as img, i}
          <div class="relative group">
            <img
              src={img.dataUrl}
              alt={img.name}
              class="w-16 h-16 object-cover border border-gray-300"
            />
            <button
              onclick={() => removeImage(i)}
              class="absolute -top-1.5 -right-1.5 w-5 h-5 bg-gray-900 text-white flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity"
              title="Remove"
            >
              <X size={12} />
            </button>
            <div class="absolute bottom-0 left-0 right-0 bg-black/50 text-white text-[9px] px-1 truncate">
              {img.name}
            </div>
          </div>
        {/each}
      </div>
    {/if}

    <div class="flex gap-2">
      <!-- Hidden file input -->
      <input
        bind:this={fileInput}
        type="file"
        accept="image/*"
        multiple
        class="hidden"
        onchange={handleFilePick}
      />

      <!-- Image attach button -->
      <button
        onclick={() => fileInput?.click()}
        disabled={models.length === 0}
        class="px-2.5 py-2 border border-gray-300 hover:bg-gray-50 text-gray-500 hover:text-gray-700 disabled:opacity-30 disabled:hover:bg-transparent disabled:hover:text-gray-500 transition-colors"
        title="Attach image"
      >
        <ImagePlus size={14} />
      </button>

      <textarea
        bind:value={userInput}
        onkeydown={handleKeydown}
        onpaste={handlePaste}
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
          disabled={(!userInput.trim() && pendingImages.length === 0) || !selectedModel || models.length === 0}
          class="px-3 py-2 bg-gray-900 text-white hover:bg-gray-800 disabled:opacity-30 disabled:hover:bg-gray-900 flex items-center gap-1.5 transition-colors"
          title="Send"
        >
          <Send size={14} />
        </button>
      {/if}
    </div>
  </div>
</div>
