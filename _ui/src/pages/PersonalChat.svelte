<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { push } from 'svelte-spa-router';
  import { Plus, Send, Square, Settings2, History, Trash2, X } from 'lucide-svelte';
  import Markdown from '@/lib/components/Markdown.svelte';
  import {
    listPersonalModels, listConversations, getConversation, createConversation, updateConversation,
    deleteConversation, listPersonalMessages, getPersonalMessage, cancelPersonalMessage, sendPersonalMessage,
    conversationRoute, mergeMessages, appendPersonalDelta, isActive, utf8Length, PersonalChatError,
    type Conversation, type PersonalModel, type PersonalMessage, type PendingTurn, type AcceptedTurn,
  } from '@/lib/api/personal-chat';

  let { id, owner }: { id: string; owner: string } = $props();
  const lifetime = new AbortController();
  let stream: AbortController | undefined;
  let conversation = $state<Conversation | null>(null);
  let history = $state<Conversation[]>([]);
  let historyCursor = $state('');
  let messageCursor = $state('');
  let messages = $state<PersonalMessage[]>([]);
  let models = $state<PersonalModel[]>([]);
  let pending = $state<PendingTurn | null>(null);
  let draft = $state('');
  let loading = $state(true);
  let refreshing = $state(false);
  let historyLoading = $state(false);
  let olderLoading = $state(false);
  let modelsLoading = $state(false);
  let sending = $state(false);
  let saving = $state(false);
  let cancelling = $state(false);
  let error = $state('');
  let historyError = $state('');
  let showHistory = $state(false);
  let settings = $state(false);
  let confirmingDelete = $state(false);
  let title = $state('New conversation');
  let modelKey = $state('');
  let systemPrompt = $state('');
  let scroller = $state<HTMLDivElement>();
  let composer = $state<HTMLTextAreaElement>();
  let follow = true;
  const active = $derived(messages.find(isActive));
  const selected = $derived(models.find(model => JSON.stringify([model.provider_key, model.model]) === modelKey));
  const draftBytes = $derived(utf8Length(draft));
  const configValid = $derived(title.trim() !== '' && utf8Length(title) <= 256 && utf8Length(systemPrompt) <= 32768 && !`${title}${systemPrompt}`.includes('\0'));
  const storageKey = $derived(`at-personal-pending:${encodeURIComponent(owner)}:${encodeURIComponent(id)}`);
  const alive = () => !lifetime.signal.aborted;

  function retainPending(value: PendingTurn | null) {
    pending = value;
    // Only an unresolved turn is retained, scoped to this native owner and conversation.
    // Never persist a transcript or model/provider configuration in browser storage.
    try {
      if (value) sessionStorage.setItem(storageKey, JSON.stringify(value));
      else sessionStorage.removeItem(storageKey);
    } catch { /* In-memory recovery remains available when storage is blocked. */ }
  }

  function report(e: unknown) {
    if (!alive()) return;
    error = e instanceof PersonalChatError && e.status === 409
      ? 'A turn is active. Stop it before changing settings, deleting, or sending another message.'
      : e instanceof PersonalChatError && e.status === 404
        ? 'Conversation unavailable. It may have been deleted or belong to another account.'
        : e instanceof Error ? e.message : 'Could not reach the server. Refresh to check the saved state.';
  }

  function adopt(incoming: PersonalMessage[], replace = false) {
    if (!alive()) return;
    messages = mergeMessages(replace ? [] : messages, incoming);
    const match = pending && incoming.find(message => message.request_id === pending!.request_id && message.role === 'assistant');
    if (match) {
      if (draft === pending?.content) draft = '';
      retainPending(isActive(match) ? { ...pending!, assistant_id: match.id } : null);
    }
    if (follow) void tick().then(() => { if (alive() && scroller) scroller.scrollTop = scroller.scrollHeight; });
  }

  function editSettings() {
    if (conversation) {
      title = conversation.title;
      modelKey = JSON.stringify([conversation.provider_key, conversation.model]);
      systemPrompt = conversation.system_prompt;
    }
    settings = true;
    confirmingDelete = false;
  }

  async function loadHistory(older = false) {
    if (historyLoading || !alive()) return;
    historyLoading = true;
    historyError = '';
    try {
      const page = await listConversations(lifetime.signal, older ? historyCursor : '');
      if (!alive()) return;
      history = older ? [...history, ...page.items.filter(item => !history.some(old => old.id === item.id))] : page.items;
      historyCursor = page.next_before;
    } catch { if (alive()) historyError = 'Could not load conversations. Try again.'; }
    finally { if (alive()) historyLoading = false; }
  }

  async function loadModels() {
    if (modelsLoading || !alive()) return;
    modelsLoading = true;
    try {
      const items = await listPersonalModels(lifetime.signal);
      if (!alive()) return;
      models = items;
      if (!id && !modelKey && items.length) modelKey = JSON.stringify([items[0].provider_key, items[0].model]);
    } catch (e) { report(e); }
    finally { if (alive()) modelsLoading = false; }
  }

  async function refresh() {
    if (!id || refreshing || olderLoading || sending || !alive()) return;
    refreshing = true;
    try {
      const [record, page] = await Promise.all([getConversation(id, lifetime.signal), listPersonalMessages(id, lifetime.signal)]);
      if (!alive()) return;
      const outsideActiveIDs = messages.filter(message => isActive(message) && !page.items.some(item => item.id === message.id)).map(message => message.id);
      const reconcileIDs = new Set([...outsideActiveIDs, ...(pending?.assistant_id ? [pending.assistant_id] : [])]);
      const snapshots = await Promise.all([...reconcileIDs].map(messageID => getPersonalMessage(id, messageID, lifetime.signal)));
      if (!alive()) return;
      conversation = record;
      // Reconcile evidence before dropping older pages. Failed reads leave the old
      // window and pending request intact; a refresh never assumes an absent turn ended.
      adopt(snapshots);
      messageCursor = page.next_before;
      const latest = mergeMessages(page.items, snapshots.filter(message => page.items.some(item => item.id === message.id) || isActive(message)));
      adopt(latest, true);
    } catch (e) { report(e); }
    finally { if (alive()) refreshing = false; }
  }

  async function loadOlder() {
    if (olderLoading || refreshing || !messageCursor || !alive()) return;
    olderLoading = true;
    const height = scroller?.scrollHeight || 0;
    const top = scroller?.scrollTop || 0;
    try {
      const page = await listPersonalMessages(id, lifetime.signal, messageCursor);
      if (!alive()) return;
      follow = false;
      adopt(page.items);
      messageCursor = page.next_before;
      await tick();
      if (alive() && scroller) scroller.scrollTop = top + scroller.scrollHeight - height;
    } catch (e) { report(e); }
    finally { if (alive()) olderLoading = false; }
  }

  async function save() {
    if (saving || active || pending || sending || !configValid) return;
    if (!selected && !conversation) return;
    saving = true;
    error = '';
    try {
      if (!id) {
        const created = await createConversation({ title, system_prompt: systemPrompt, ...selected! }, lifetime.signal);
        if (alive()) push(conversationRoute(created.id));
      } else {
        // Send model fields together only when changed; an unavailable historical model
        // must not prevent a title-only edit or silently switch to the catalog default.
        const modelChanged = selected && (selected.model !== conversation!.model || selected.provider_key !== conversation!.provider_key);
        const updated = await updateConversation(id, { title, system_prompt: systemPrompt, ...(modelChanged ? selected : {}) }, lifetime.signal);
        if (!alive()) return;
        conversation = updated;
        settings = false;
        await loadHistory();
      }
    } catch (e) {
      report(e);
      if (!id) { error += ' Check conversation history before creating again.'; void loadHistory(); }
      else await refresh();
    } finally { if (alive()) saving = false; }
  }

  async function remove() {
    if (saving || active || pending || sending) return;
    saving = true;
    error = '';
    try {
      await deleteConversation(id, lifetime.signal);
      if (alive()) { retainPending(null); push('/chat'); }
    } catch (e) { report(e); await refresh(); }
    finally { if (alive()) saving = false; }
  }

  async function send(replay = false) {
    if (sending || refreshing || saving || cancelling || active || !conversation) return;
    if (replay ? !pending || !!pending.assistant_id : !!pending || !draft.trim() || draftBytes > 32768 || draft.includes('\0')) return;
    const request = replay ? { ...pending! } : { content: draft, request_id: crypto.randomUUID() };
    retainPending(request);
    sending = true;
    error = '';
    follow = true;
    stream = new AbortController();
    try {
      await sendPersonalMessage(id, request, stream.signal, async (event, data) => {
        if (!alive() || stream?.signal.aborted) return;
        if (event === 'accepted') {
          const accepted = data as AcceptedTurn;
          adopt([accepted.user, accepted.assistant]);
        } else if (event === 'delta') {
          const message = messages.find(item => item.id === data.assistant_message_id);
          const next = message && appendPersonalDelta(message, data);
          if (next) {
            // Provisional text is not a durable status update.
            messages = mergeMessages(messages, [next]);
            if (follow) void tick().then(() => { if (alive() && scroller) scroller.scrollTop = scroller.scrollHeight; });
          } else if (pending?.assistant_id) {
            adopt([await getPersonalMessage(id, pending.assistant_id, lifetime.signal)]);
          }
        } else if (event === 'snapshot' || event === 'done' || event === 'error') {
          adopt([data]);
        }
      });
    } catch (e) {
      if (e instanceof PersonalChatError && [400, 401, 403, 404, 409, 413, 415].includes(e.status) && !pending?.assistant_id) retainPending(null);
      if (!stream.signal.aborted) report(e);
    } finally {
      if (alive()) {
        sending = false;
        // EOF and disconnect are not success, including active idempotent replays.
        await refresh();
        void loadHistory();
        if (alive()) composer?.focus();
      }
    }
  }

  async function cancel() {
    const assistantID = active?.id || pending?.assistant_id;
    if (!assistantID || cancelling) return;
    cancelling = true;
    error = '';
    try {
      const snapshot = await cancelPersonalMessage(id, assistantID, lifetime.signal);
      if (!alive()) return;
      stream?.abort();
      adopt([snapshot]);
    } catch (e) { report(e); }
    finally {
      if (alive()) { cancelling = false; await refresh(); }
    }
  }

  onMount(() => {
    try {
      const saved = JSON.parse(sessionStorage.getItem(storageKey) || 'null');
      if (saved && typeof saved.request_id === 'string' && typeof saved.content === 'string') {
        pending = saved;
        draft = saved.content;
      }
    } catch { /* No recoverable pending request. */ }
    void (async () => {
      await Promise.all([
        loadHistory(), refresh(), loadModels(),
      ]);
      if (alive()) loading = false;
    })();
    const recheck = () => { if (!document.hidden) { void refresh(); void loadHistory(); } };
    window.addEventListener('focus', recheck);
    document.addEventListener('visibilitychange', recheck);
    const timer = window.setInterval(() => { if (active || pending) void refresh(); }, 2000);
    return () => {
      lifetime.abort();
      stream?.abort();
      window.clearInterval(timer);
      window.removeEventListener('focus', recheck);
      document.removeEventListener('visibilitychange', recheck);
    };
  });
</script>

<div class="flex h-full min-h-0 min-w-0 flex-col bg-white dark:bg-dark-surface">
  <header class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 px-4 py-3 dark:border-dark-border">
    <div class="flex min-w-0 items-center gap-3">
      <div class="md:hidden"><button class="control" aria-label="Toggle conversation history" aria-expanded={showHistory} onclick={() => showHistory = !showHistory}><History size={18} /></button></div>
      <h1 class="text-lg font-semibold">Chat</h1>
      <span class="text-sm text-gray-600 dark:text-dark-text-secondary">Your conversations</span>
    </div>
    <p class="text-xs text-gray-600 dark:text-dark-text-secondary">Text only. No tools or images. <a href="#/playground" class="underline underline-offset-4">Use Playground</a></p>
  </header>
  <div class="flex min-h-0 flex-1 flex-col md:flex-row">
    <aside aria-label="Conversation history" class={[showHistory ? 'flex' : 'hidden', 'max-h-64 shrink-0 flex-col border-b border-gray-200 bg-gray-50 md:flex md:max-h-none md:w-56 md:border-r md:border-b-0 dark:border-dark-border dark:bg-dark-base']}>
      <div class="flex items-center justify-between gap-2 p-3">
        <a href="#/chat" class="control flex-1 justify-center"><Plus size={16} />New conversation</a>
      </div>
      <nav class="min-h-0 flex-1 overflow-y-auto px-2 pb-3" aria-label="Saved conversations">
        {#each history as item (item.id)}
          <a href={`#${conversationRoute(item.id)}`} aria-current={id === item.id ? 'page' : undefined}
            class={['mb-1 block rounded-md px-3 py-2.5 text-sm hover:bg-gray-100 dark:hover:bg-dark-elevated', id === item.id ? 'bg-gray-200 font-medium dark:bg-dark-elevated' : '']}
            title={item.title}>
            <span class="block truncate">{item.title}</span>
            <span class="mt-1 block truncate text-xs font-normal text-gray-600 dark:text-dark-text-secondary">{item.model}</span>
          </a>
        {/each}
        {#if !history.length && !historyLoading && !historyError}<p class="px-3 py-4 text-sm text-gray-600 dark:text-dark-text-secondary">No conversations yet.</p>{/if}
        {#if historyError}<p role="alert" class="px-3 py-2 text-sm">{historyError}</p><button class="control" onclick={() => loadHistory()}>Retry history</button>{/if}
        {#if historyCursor}<button class="control w-full justify-center" disabled={historyLoading} onclick={() => loadHistory(true)}>Load older conversations</button>{/if}
        {#if historyLoading}<p role="status" class="p-3 text-sm">Loading history...</p>{/if}
      </nav>
    </aside>

    <section aria-label="Personal conversation" class="flex min-h-0 min-w-0 flex-1 flex-col">
      {#if error}
        <div role="alert" class="flex flex-wrap items-center gap-3 border-b border-red-200 bg-red-50 px-5 py-3 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200">
          <p class="min-w-0 flex-1">{error}</p>
          <button class="control" disabled={sending || refreshing} onclick={() => { error = ''; void refresh(); }}>Refresh status</button>
        </div>
      {/if}
      {#if loading}<p role="status" class="p-6 text-sm">Loading conversation...</p>
      {:else if !id || settings}
        <div class="min-h-0 flex-1 overflow-y-auto p-5 sm:p-8">
          <form class="mx-auto max-w-xl space-y-5" onsubmit={(event) => { event.preventDefault(); void save(); }}>
            <div class="flex items-center justify-between gap-3">
              <h2 class="text-xl font-semibold">{id ? 'Conversation settings' : 'New conversation'}</h2>
              {#if id}<button type="button" class="control" aria-label="Close settings" onclick={() => settings = false}><X size={18} /></button>{/if}
            </div>
            <p class="text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">{id ? 'Changes apply to future answers. Previous answers keep their original model.' : 'Saved to your account, not shared with other users. Choose a model to start a text conversation.'}</p>
            <label class="block space-y-2 text-sm font-medium">Title<input class="field" bind:value={title} required maxlength={256} /></label>
            <label class="block space-y-2 text-sm font-medium">Model
              <select class="field" bind:value={modelKey} disabled={!models.length}>
                {#if !selected}<option value={modelKey}>{conversation ? `${conversation.provider_key} / ${conversation.model} (unavailable)` : 'No chat models configured'}</option>{/if}
                {#each models as model}<option value={JSON.stringify([model.provider_key, model.model])}>{model.provider_key} / {model.model}</option>{/each}
              </select>
            </label>
            {#if !models.length}<p class="text-sm text-gray-600 dark:text-dark-text-secondary">No configured chat models are available. Ask your administrator to configure one.</p>{/if}
            <button type="button" class="control" disabled={modelsLoading} onclick={loadModels}>{modelsLoading ? 'Refreshing models...' : 'Refresh model list'}</button>
            <label class="block space-y-2 text-sm font-medium">System prompt <span class="font-normal text-gray-600 dark:text-dark-text-secondary">(optional)</span><textarea class="field" rows="5" bind:value={systemPrompt} placeholder="Instructions for this conversation"></textarea></label>
            {#if !configValid}<p role="status" class="text-sm text-red-700 dark:text-red-300">Use a nonblank title up to 256 bytes and a system prompt up to 32 KiB. NUL characters are not supported.</p>{/if}
            {#if active || pending}<p role="status" class="text-sm">Resolve or stop the current turn before changing settings.</p>{/if}
            <div class="flex flex-wrap gap-3">
              <button type="submit" class="primary" disabled={saving || sending || !!active || !!pending || !configValid || (!id && !selected)}>{saving ? 'Saving...' : id ? 'Save changes' : 'Create conversation'}</button>
              {#if id}<button class="control" type="button" disabled={saving || sending || !!active || !!pending} onclick={() => confirmingDelete = true}><Trash2 size={16} />Delete conversation</button>{/if}
            </div>
            {#if confirmingDelete}
              <div role="group" aria-label="Confirm deletion" class="space-y-3 border-t border-gray-200 pt-5 dark:border-dark-border">
                <p class="text-sm">Delete "{conversation?.title}" and all its messages? This cannot be undone.</p>
                <div class="flex gap-3"><button type="button" class="control text-red-700 dark:text-red-300" disabled={saving || !!active || !!pending} onclick={remove}>Delete permanently</button><button type="button" class="control" onclick={() => confirmingDelete = false}>Keep conversation</button></div>
              </div>
            {/if}
          </form>
        </div>
      {:else if conversation}
        <div class="flex items-center justify-between gap-3 border-b border-gray-200 px-5 py-3 dark:border-dark-border">
          <div class="min-w-0"><h2 class="truncate font-medium" title={conversation.title}>{conversation.title}</h2><p class="truncate text-xs text-gray-600 dark:text-dark-text-secondary">{conversation.provider_key} / {conversation.model}</p></div>
          <button class="control" aria-label="Settings" onclick={editSettings}><Settings2 size={16} /><span class="hidden sm:inline">Settings</span></button>
        </div>
        <div bind:this={scroller} class="min-h-0 flex-1 overflow-y-auto overscroll-contain px-5 sm:px-8" onscroll={() => { if (scroller) follow = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 80; }}>
          <div class="mx-auto max-w-3xl py-6">
            {#if messageCursor}
              <div class="mb-6 text-center">
                <button class="control mx-auto" disabled={olderLoading || refreshing} onclick={loadOlder}>{olderLoading ? 'Loading...' : 'Load older messages'}</button>
                <p class="mt-2 text-xs text-gray-600 dark:text-dark-text-secondary">Refreshing returns to the latest 50 messages. Load older messages again to continue reading.</p>
              </div>
            {/if}
            {#if !messages.length}<div class="py-10"><h3 class="text-xl font-medium">What would you like to work on?</h3><p class="mt-3 text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">Send a message below. Your conversation will be here when you return.</p></div>{/if}
            {#each messages as message (message.id)}
              <article class={['mb-7 min-w-0', message.role === 'user' ? 'ml-auto max-w-[90%] rounded-lg bg-gray-100 p-4 dark:bg-dark-elevated' : 'py-2']}>
                <div class="mb-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-gray-600 dark:text-dark-text-secondary">
                  <span class="font-semibold text-gray-900 dark:text-dark-text">{message.role === 'user' ? 'You' : 'Assistant'}</span>
                  {#if message.role === 'assistant'}<span>{message.provider_key} / {message.model}</span>{/if}
                  <span>{message.status}</span>
                </div>
                {#if message.role === 'assistant'}<Markdown source={message.content} safe class="min-w-0 overflow-x-auto break-words text-sm leading-7" />{:else}<p class="whitespace-pre-wrap break-words text-sm leading-7">{message.content}</p>{/if}
                {#if isActive(message) && !message.content}<p class="text-sm text-gray-600 dark:text-dark-text-secondary">Waiting for an answer...</p>{/if}
                {#if message.error}<p class="mt-3 text-sm text-red-700 dark:text-red-300">{message.error.replaceAll('_', ' ')}. Partial text is saved. Send a new message to try again.</p>{/if}
                {#if message.finish_reason === 'length' || message.finish_reason === 'content_filter'}<p class="mt-3 text-xs text-gray-600 dark:text-dark-text-secondary">{message.finish_reason === 'length' ? 'Answer reached the model output limit.' : 'Answer stopped by the content filter.'}</p>{/if}
              </article>
            {/each}
          </div>
        </div>
        <div class="border-t border-gray-200 p-4 sm:px-8 dark:border-dark-border">
          <div class="mx-auto max-w-3xl">
            {#if pending && !pending.assistant_id && !sending}
              <div role="status" class="mb-3 space-y-2 text-sm">
                <p>Delivery is unconfirmed. Check the same request before sending anything new.</p>
                <button class="control" disabled={refreshing || !!active} onclick={() => send(true)}>Check / replay same request</button>
              </div>
            {/if}
            <form onsubmit={(event) => { event.preventDefault(); void send(); }}>
              <label for="personal-message" class="sr-only">Message</label>
              <textarea id="personal-message" bind:this={composer} bind:value={draft} class="field resize-y" rows="3" placeholder="Write a message..." disabled={!!pending && !pending.assistant_id}
                onkeydown={(event) => { if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) { event.preventDefault(); void send(); } }}></textarea>
              <div class="mt-2 flex flex-wrap items-center justify-between gap-2">
                <p class="text-xs text-gray-600 dark:text-dark-text-secondary">Enter to send. Shift+Enter for a new line.{draftBytes > 32768 ? ' Message exceeds 32 KiB.' : ''}</p>
                {#if active || pending?.assistant_id}
                  <button type="button" class="control" disabled={cancelling} onclick={cancel}><Square size={14} />{cancelling ? 'Stopping...' : 'Stop answer'}</button>
                {:else}
                  <button type="submit" class="primary" disabled={sending || refreshing || !!pending || !draft.trim() || draftBytes > 32768 || draft.includes('\0')}><Send size={15} />{sending ? 'Sending...' : 'Send'}</button>
                {/if}
              </div>
            </form>
          </div>
        </div>
      {/if}
    </section>
  </div>
</div>

<style>
  @reference "tailwindcss";
  .control { @apply inline-flex min-h-10 items-center gap-2 rounded-md border border-gray-300 bg-white px-3 py-2 text-sm hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-50; }
  .primary { @apply inline-flex min-h-10 items-center justify-center gap-2 rounded-md bg-gray-900 px-4 py-2 text-sm font-medium text-white hover:bg-gray-800 disabled:cursor-not-allowed disabled:opacity-50; }
  .field { @apply block w-full min-w-0 rounded-md border border-gray-300 bg-white px-3 py-2.5 text-sm font-normal; }
  :global(.dark) .control, :global(.dark) .field { border-color: var(--color-dark-border-subtle); background: var(--color-dark-elevated); color: var(--color-dark-text); }
  :global(.dark) .control:hover { background: var(--color-dark-highest); }
  :global(.dark) .primary { background: var(--color-accent); color: var(--color-gray-950); }
  button:focus-visible, a:focus-visible, input:focus-visible, select:focus-visible, textarea:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 3px; }
  textarea, input { caret-color: var(--color-accent); }
</style>
