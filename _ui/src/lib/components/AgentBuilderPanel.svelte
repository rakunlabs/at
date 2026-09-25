<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { Bot, Send, Square, X } from 'lucide-svelte';
  import { getInfo, type InfoProvider } from '../api/gateway';
  import { authErrorMessage } from '../api/auth';
  import { getTextContent, type ChatMessage } from '../helper/chat';
  import { type AgentDraft, type AgentBuilderCatalog } from '../helper/agent-builder';
  import { runAgentBuilderTurn } from '../helper/agent-builder-run';

  interface Props {
    getDraft: () => AgentDraft;
    getCatalog: () => AgentBuilderCatalog;
    applyPatch: (patch: unknown) => string[];
    onclose: () => void;
    busy?: boolean;
    contextLoading?: boolean;
  }
  let { getDraft, getCatalog, applyPatch, onclose, busy = $bindable(false), contextLoading = false }: Props = $props();
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
  let models = $derived([...new Set(providers.flatMap(p => (p.models?.length ? p.models : p.default_model ? [p.default_model] : []).map(m => `${p.reference || p.key}/${m}`)))]);

  async function loadModels() {
    loading = true; error = '';
    try {
      const info = await getInfo();
      if (disposed) return;
      providers = info.providers;
      const draft = getDraft();
      const preferred = `${draft.provider}/${draft.model}`;
      model = models.includes(preferred) ? preferred : models[0] || '';
    } catch (e) { if (!disposed) error = authErrorMessage(e, 'Could not load AI models. Retry to continue.'); }
    finally { if (!disposed) loading = false; }
  }
  onMount(() => { void loadModels(); });
  onDestroy(() => { disposed = true; controller?.abort(); });
  async function scroll() { await tick(); if (chat && !disposed) chat.scrollTop = chat.scrollHeight; }

  async function send() {
    if (!input.trim() || !model || busy || contextLoading) return;
    messages = [...messages, { role: 'user', content: input.trim() }];
    input = ''; error = ''; updates = []; busy = true;
    const run = new AbortController(); controller = run;
    try {
      await runAgentBuilderTurn({
        model, messages, signal: run.signal, getDraft, getCatalog, applyPatch,
        onMessages: value => { if (!disposed) { messages = value; void scroll(); } },
        onUpdates: value => { if (!disposed) updates = value; },
      });
    } catch (e) {
      if (!run.signal.aborted && !disposed) error = authErrorMessage(e, 'AI request failed. Send another message to retry.');
    } finally {
      if (!disposed) { busy = false; controller = undefined; void scroll(); }
    }
  }
  function stop() { controller?.abort(); }
</script>

<aside id="agent-ai-builder" class="settings-form w-80 max-w-[85vw] shrink-0 min-h-0 flex flex-col border-l border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface" aria-label="Agent Builder AI">
  <header class="flex items-center justify-between gap-3 px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
    <h3 class="flex items-center gap-2 text-sm font-medium"><Bot size={16} />Agent Builder AI</h3>
    <button type="button" class="settings-button min-h-11 sm:min-h-0" aria-label="Close AI builder" onclick={onclose}><X size={14} /></button>
  </header>
  <div class="px-4 py-3 space-y-2 border-b border-gray-200 dark:border-dark-border">
    <label>Assistant model<select bind:value={model} disabled={loading || busy || !models.length}>{#if !models.length}<option value="">{loading ? 'Loading models…' : 'No models available'}</option>{/if}{#each models as name}<option value={name}>{name}</option>{/each}</select></label>
    <p class="settings-note">Describe your agent. Changes appear in the form; use Create or Update to save.</p>
    {#if contextLoading}<p role="status" class="settings-note">Loading available form resources…</p>{/if}
    {#if !loading && (!models.length || error)}<button type="button" class="settings-button" disabled={busy} onclick={loadModels}>Reload models</button>{/if}
  </div>
  <div bind:this={chat} class="flex-1 min-h-0 overflow-y-auto p-4 space-y-4" role="log" aria-label="Agent builder conversation">
    {#if !messages.length}
      <p class="text-sm">What should this agent do?</p>
      <p class="settings-note">Describe its job, preferred language and the tools it needs. You can refine any part through conversation.</p>
      <button type="button" class="settings-button text-left" onclick={() => input = 'Create a code review agent that checks correctness, explains risks, and suggests focused fixes.'}>Start with a code reviewer</button>
      <button type="button" class="settings-button text-left" onclick={() => input = 'Improve the current agent’s system prompt. Keep its other settings.'}>Improve the current prompt</button>
    {/if}
    {#each messages as message}
      {#if message.role === 'user' || message.role === 'assistant'}
        <div class="space-y-1"><p class="text-xs font-medium text-gray-500 dark:text-dark-text-muted">{message.role === 'user' ? 'You' : 'Assistant'}</p><p class="text-sm whitespace-pre-wrap break-words">{getTextContent(message.content)}</p>
          {#if message.tool_calls?.length}<p class="settings-note">{message.tool_calls.some(c => c.function.name === 'update_agent_form') ? 'Processing form changes' : 'Reading form context'}</p>{/if}
        </div>
      {/if}
    {/each}
    {#if busy}<p role="status" class="settings-note">Working…</p>{/if}
  </div>
  <div class="px-4 py-3 border-t border-gray-200 dark:border-dark-border space-y-2">
    {#if updates.length}<p role="status" class="settings-note">Updated: {updates.join(', ')}. Not saved yet.</p>{/if}
    {#if error}<p role="alert" class="settings-error">{error}</p>{/if}
    <label>Message<textarea rows="3" bind:value={input} placeholder="Describe an agent or ask for a change…" onkeydown={e => { if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); void send(); } }}></textarea></label>
    <div class="flex items-center justify-between gap-2">
      <button type="button" class="settings-button" disabled={busy || !messages.length} onclick={() => { messages = []; updates = []; error = ''; }}>Clear chat</button>
      {#if busy}<button type="button" class="settings-button flex items-center gap-2 min-h-11 sm:min-h-0" onclick={stop}><Square size={14} />Stop</button>
      {:else}<button type="button" class="settings-primary flex items-center gap-2 min-h-11 sm:min-h-0" disabled={!input.trim() || !model || loading || contextLoading} onclick={send}><Send size={14} />Send</button>{/if}
    </div>
  </div>
</aside>
