<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { Bot, Send, Square, X } from 'lucide-svelte';
  import { getInfo, type InfoProvider } from '../api/gateway';
  import { authErrorMessage } from '../api/auth';
  import { getTextContent, type ChatMessage, type ToolDefinition } from '../helper/chat';
  import { runFormBuilderTurn, type FormBuilderToolNames } from '../helper/form-builder';
  import Markdown from './Markdown.svelte';

  interface Props {
    id: string;
    title: string;
    placeholder: string;
    suggestions: string[];
    systemPrompt: string;
    tools: ToolDefinition[];
    names: FormBuilderToolNames;
    getDraft: () => Record<string, unknown>;
    getCatalog: () => Record<string, unknown>;
    applyPatch: (patch: unknown) => string[];
    onclose: () => void;
    busy?: boolean;
    contextLoading?: boolean;
  }

  let { id, title, placeholder, suggestions, systemPrompt, tools, names, getDraft, getCatalog, applyPatch, onclose, busy = $bindable(false), contextLoading = false }: Props = $props();
  const widthKey = 'at.form-builder.width';
  const minWidth = 280;
  const maxWidth = () => Math.max(minWidth, Math.floor(window.innerWidth * 0.8));
  const clampWidth = (value: number) => Math.min(maxWidth(), Math.max(minWidth, Math.round(value)));
  let width = $state(clampWidth(Number(localStorage.getItem(widthKey)) || 320));
  let resizing = $state(false);

  function startResize(event: PointerEvent) {
    if (event.button !== 0) return;
    event.preventDefault();
    const handle = event.currentTarget as HTMLElement;
    handle.setPointerCapture(event.pointerId);
    const startX = event.clientX;
    const startWidth = width;
    resizing = true;
    const move = (e: PointerEvent) => { width = clampWidth(startWidth + startX - e.clientX); };
    const end = () => {
      resizing = false;
      handle.removeEventListener('pointermove', move);
      handle.removeEventListener('pointerup', end);
      handle.removeEventListener('pointercancel', end);
      localStorage.setItem(widthKey, String(width));
    };
    handle.addEventListener('pointermove', move);
    handle.addEventListener('pointerup', end);
    handle.addEventListener('pointercancel', end);
  }

  function resizeByKey(event: KeyboardEvent) {
    const step = event.shiftKey ? 64 : 16;
    if (event.key === 'ArrowLeft') width = clampWidth(width + step);
    else if (event.key === 'ArrowRight') width = clampWidth(width - step);
    else return;
    event.preventDefault();
    localStorage.setItem(widthKey, String(width));
  }
  let providers = $state<InfoProvider[]>([]);
  let model = $state('');
  let loading = $state(true);
  let error = $state('');
  let input = $state('');
  let messages = $state<ChatMessage[]>([]);
  let updates = $state<string[]>([]);
  let chat: HTMLDivElement | undefined = $state();
  let controller: AbortController | undefined;
  let disposed = false;
  let models = $derived([...new Set(providers.flatMap(provider => (provider.models?.length ? provider.models : provider.default_model ? [provider.default_model] : []).map(name => `${provider.reference || provider.key}/${name}`)))]);

  async function loadModels() {
    loading = true;
    error = '';
    try {
      const info = await getInfo();
      if (disposed) return;
      providers = info.providers;
      model = models[0] || '';
    } catch (e) {
      if (!disposed) error = authErrorMessage(e, 'Could not load AI models. Retry to continue.');
    } finally {
      if (!disposed) loading = false;
    }
  }

  onMount(() => { void loadModels(); });
  onDestroy(() => { disposed = true; controller?.abort(); });
  async function scroll() { await tick(); if (chat && !disposed) chat.scrollTop = chat.scrollHeight; }

  async function send() {
    if (!input.trim() || !model || busy || contextLoading) return;
    messages = [...messages, { role: 'user', content: input.trim() }];
    input = '';
    error = '';
    updates = [];
    busy = true;
    const run = new AbortController();
    controller = run;
    try {
      await runFormBuilderTurn({ model, messages, signal: run.signal, systemPrompt, tools, names, getDraft, getCatalog, applyPatch,
        onMessages: value => { if (!disposed) { messages = value; void scroll(); } },
        onUpdates: value => { if (!disposed) updates = value; },
      });
    } catch (e) {
      if (!run.signal.aborted && !disposed) error = authErrorMessage(e, 'AI request failed. Send another message to retry.');
    } finally {
      if (!disposed) { busy = false; controller = undefined; void scroll(); }
    }
  }
</script>

<aside {id} class="settings-form relative max-w-[85vw] shrink-0 min-h-0 flex flex-col border-l border-dark-border bg-dark-surface" style:width={`${width}px`} aria-label={title}>
  <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
  <div
    role="separator"
    aria-orientation="vertical"
    aria-label={`Resize ${title}`}
    aria-valuenow={width}
    aria-valuemin={minWidth}
    tabindex="0"
    class={["absolute inset-y-0 -left-1 z-10 w-2 cursor-col-resize touch-none hover:bg-dark-border focus:outline-none focus-visible:bg-dark-border-subtle/60", resizing ? "bg-dark-border" : ""]}
    onpointerdown={startResize}
    onkeydown={resizeByKey}
    ondblclick={() => { width = clampWidth(320); localStorage.setItem(widthKey, String(width)); }}
  ></div>
  <header class="flex items-center justify-between gap-3 px-3 py-2 border-b border-dark-border shrink-0">
    <h3 class="flex items-center gap-1.5 text-xs font-medium text-dark-text"><Bot size={14} class="text-dark-text-muted" />{title}</h3>
    <button type="button" class="text-dark-text-muted hover:text-dark-text" aria-label={`Close ${title}`} onclick={onclose}><X size={14} /></button>
  </header>
  <div class="px-4 py-3 space-y-2 border-b border-dark-border">
    <label>Assistant model<select bind:value={model} disabled={loading || busy || !models.length}>{#if !models.length}<option value="">{loading ? 'Loading models…' : 'No models available'}</option>{/if}{#each models as name}<option value={name}>{name}</option>{/each}</select></label>
    {#if contextLoading}<p role="status" class="settings-note">Loading available form resources…</p>{/if}
    {#if !loading && (!models.length || error)}<button type="button" class="settings-button" disabled={busy} onclick={loadModels}>Reload models</button>{/if}
  </div>
  <div bind:this={chat} class="flex-1 min-h-0 overflow-y-auto p-4 space-y-4" role="log" aria-label={`${title} conversation`}>
    {#if !messages.length}
      <p class="text-sm">What should this form contain?</p>
      <p class="settings-note">Describe the result you need. The builder updates the open form only; you review and save it.</p>
      {#each suggestions as suggestion}<button type="button" class="settings-button text-left" onclick={() => input = suggestion}>{suggestion}</button>{/each}
    {/if}
    {#each messages as message}
      {#if message.role === 'user' || message.role === 'assistant'}
        <div class="space-y-1">
          <p class="text-xs font-medium text-dark-text-muted">{message.role === 'user' ? 'You' : 'Assistant'}</p>
          {#if message.role === 'assistant'}<Markdown source={getTextContent(message.content)} class="text-sm break-words" safe />
          {:else}<p class="text-sm whitespace-pre-wrap break-words">{getTextContent(message.content)}</p>{/if}
          {#if message.tool_calls?.length}<p class="settings-note">{message.tool_calls.some(call => call.function.name === names.update) ? 'Processing form changes' : 'Reading form context'}</p>{/if}
        </div>
      {/if}
    {/each}
    {#if busy}<p role="status" class="settings-note">Working…</p>{/if}
  </div>
  <div class="px-4 py-3 border-t border-dark-border space-y-2">
    {#if updates.length}<p role="status" class="settings-note">Updated: {updates.join(', ')}. Not saved yet.</p>{/if}
    {#if error}<p role="alert" class="settings-error">{error}</p>{/if}
    <label>Message<textarea rows="3" bind:value={input} {placeholder} onkeydown={event => { if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) { event.preventDefault(); void send(); } }}></textarea></label>
    <div class="flex items-center justify-between gap-2">
      <button type="button" class="settings-button" disabled={busy || !messages.length} onclick={() => { messages = []; updates = []; error = ''; }}>Clear chat</button>
      {#if busy}<button type="button" class="settings-button flex items-center gap-2 min-h-11 sm:min-h-0" onclick={() => controller?.abort()}><Square size={14} />Stop</button>
      {:else}<button type="button" class="settings-primary flex items-center gap-2 min-h-11 sm:min-h-0" disabled={!input.trim() || !model || loading || contextLoading} onclick={send}><Send size={14} />Send</button>{/if}
    </div>
  </div>
</aside>
