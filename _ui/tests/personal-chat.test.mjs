import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/api/personal-chat.ts', import.meta.url), 'utf8');
const compile = text => ts.transpileModule(text, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const load = text => import(`data:text/javascript;base64,${Buffer.from(text).toString('base64')}`);
const api = await load(compile(source).replace("import { authFetch } from './transport';", 'const authFetch = (...args) => globalThis.personalFetch(...args);'));
const message = (overrides = {}) => ({ id: '02', sequence: 2, conversation_id: 'c1', request_id: 'r1', role: 'assistant', content: '', status: 'pending', provider_key: 'p', model: 'm', finish_reason: '', error: '', usage: { prompt_tokens: 0, completion_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0, reasoning_tokens: 0, total_tokens: 0 }, created_at: '2026-09-07T00:00:00Z', ...overrides });
const frame = (event, data) => `event: ${event}\r\ndata: ${JSON.stringify(data)}\r\n\r\n`;
const body = text => new ReadableStream({ start(controller) { for (const byte of new TextEncoder().encode(text)) controller.enqueue(Uint8Array.of(byte)); controller.close(); } });

test('fragmented UTF-8 SSE accepted, delta, heartbeat and authoritative done', async () => {
  let assistant;
  let user;
  const events = [];
  const terminal = message({ content: 'Saved answer', status: 'completed', finish_reason: 'stop' });
  const complete = await api.readPersonalEvents(body(
    frame('accepted', { user: message({ id: '01', sequence: 1, role: 'user', status: 'completed', content: 'Hello' }), assistant: message(), replay: false }) +
    frame('delta', { assistant_message_id: '02', offset: 0, content: 'é🙂' }) +
    frame('delta', { assistant_message_id: '02', offset: 6, content: '世界' }) +
    frame('heartbeat', { assistant_message_id: '02' }) + frame('future', { ignored: true }) + frame('done', terminal)
  ), (event, data) => {
    events.push(event);
    if (event === 'accepted') { assistant = data.assistant; user = data.user; }
    if (event === 'delta') { assistant = api.appendPersonalDelta(assistant, data); assert.ok(assistant); }
    if (event === 'done') assistant = data;
  });
  assert.equal(complete, true);
  assert.equal(user.id, '01');
  assert.equal(user.sequence, 1);
  assert.equal(assistant.sequence, 2);
  assert.deepEqual(assistant, terminal);
  assert.deepEqual(events, ['accepted', 'delta', 'delta', 'heartbeat', 'done']);
});

test('byte offsets and message IDs must match, never UTF-16 string length', () => {
  const current = message({ content: 'é🙂' });
  assert.equal(api.utf8Length(current.content), 6);
  assert.equal(api.appendPersonalDelta(current, { assistant_message_id: '02', offset: 3, content: 'bad' }), null);
  assert.equal(api.appendPersonalDelta(current, { assistant_message_id: 'other', offset: 6, content: 'bad' }), null);
  assert.equal(api.appendPersonalDelta(current, { assistant_message_id: '02', offset: 6, content: 'ok' }).content, 'é🙂ok');
});

test('active replay EOF remains active; missing terminal and incomplete frames are not success', async () => {
  const snapshot = message({ content: 'checkpoint', status: 'streaming' });
  let current;
  const complete = await api.readPersonalEvents(body(frame('accepted', { user: message({ role: 'user' }), assistant: snapshot, replay: true }) + frame('snapshot', snapshot)), (event, data) => { current = event === 'accepted' ? data.assistant : data; });
  assert.equal(complete, false);
  assert.equal(api.isActive(current), true);
  assert.equal(await api.readPersonalEvents(body('event: done\ndata: {"status":"completed"}'), () => assert.fail('incomplete frame')), false);
});

test('error terminals preserve durable failure and cancelled partial content', async () => {
  for (const status of ['failed', 'cancelled']) {
    const snapshot = message({ status, content: 'saved partial', error: 'generation_interrupted' });
    let current;
    assert.equal(await api.readPersonalEvents(body(frame('error', snapshot)), (_, data) => { current = data; }), true);
    assert.deepEqual(current, snapshot);
    assert.equal(api.isActive(current), false);
  }
});

test('history pages sort by database sequence despite clock-skewed IDs and replace snapshots', () => {
  const result = api.mergeMessages([message({ id: '01', sequence: 4 }), message({ id: '03', sequence: 3 })], [message({ id: '02', sequence: 2 }), message({ id: '04', sequence: 1 }), message({ id: '03', sequence: 3, status: 'completed' })]);
  assert.deepEqual(result.map(item => item.id), ['04', '02', '03', '01']);
  assert.equal(result[2].status, 'completed');
});

test('owner-neutral relative URLs, bounded cursor pagination and exact write bodies', async () => {
  const calls = [];
  globalThis.personalFetch = async (url, init) => {
    calls.push({ url, ...init });
    if (url.endsWith('/messages') && init.method === 'POST') return new Response(body(frame('snapshot', message())), { headers: { 'Content-Type': 'text/event-stream' } });
    return new Response(init.method === 'DELETE' ? null : JSON.stringify({ items: [], next_before: '' }), { status: init.method === 'DELETE' ? 204 : 200 });
  };
  const signal = new AbortController().signal;
  await api.listPersonalModels(signal);
  await api.listConversations(signal, 'older /?');
  await api.listPersonalMessages('c/1', signal, '02');
  await api.getConversation('c/1', signal);
  await api.getPersonalMessage('c/1', 'm/2', signal);
  await api.createConversation({ title: 'Title', provider_key: 'p', model: 'm', system_prompt: '', owner_user_id: 'forbidden', tools: [] }, signal);
  await api.updateConversation('c/1', { title: 'Rename', owner_user_id: 'forbidden' }, signal);
  await api.sendPersonalMessage('c/1', { content: 'exact \n', request_id: 'uuid', messages: [], model: 'no', owner_user_id: 'no' }, signal, () => {});
  await api.cancelPersonalMessage('c/1', 'assistant/2', signal);
  await api.deleteConversation('c/1', signal);
  assert.equal(calls[0].url, 'api/v1/conversations/models');
  assert.equal(calls[1].url, 'api/v1/conversations?limit=50&before=older+%2F%3F');
  assert.equal(calls[2].url, 'api/v1/conversations/c%2F1/messages?limit=50&before=02');
  assert.equal(calls[4].url, 'api/v1/conversations/c%2F1/messages/m%2F2');
  assert.deepEqual(JSON.parse(calls[5].body), { title: 'Title', provider_key: 'p', model: 'm', system_prompt: '' });
  assert.deepEqual(JSON.parse(calls[6].body), { title: 'Rename' });
  assert.deepEqual(JSON.parse(calls[7].body), { content: 'exact \n', request_id: 'uuid' });
  assert.equal(calls[8].url, 'api/v1/conversations/c%2F1/messages/assistant%2F2/cancel');
  assert.equal(calls[8].body, undefined);
  for (const call of calls) { assert.equal(call.credentials, 'same-origin'); assert.equal(call.cache, 'no-store'); assert.equal(call.signal, signal); }
  assert.equal(api.conversationRoute('c/1'), '/chat/c%2F1');
});

test('unknown POST outcome is not retried; explicit replay retains exact content and ID', async () => {
  const requests = [];
  globalThis.personalFetch = async (_, init) => { requests.push(JSON.parse(init.body)); throw new TypeError('connection lost'); };
  const request = { content: 'exact bytes é ', request_id: 'stable-id' };
  await assert.rejects(api.sendPersonalMessage('c1', request, new AbortController().signal, () => {}));
  assert.equal(requests.length, 1);
  await assert.rejects(api.sendPersonalMessage('c1', request, new AbortController().signal, () => {}));
  assert.deepEqual(requests, [request, request]);
  globalThis.personalFetch = async () => new Response(JSON.stringify({ message: 'busy' }), { status: 409 });
  await assert.rejects(api.updateConversation('c1', { title: 'name' }, new AbortController().signal), error => error.status === 409 && error.message === 'busy');
});

test('safe Markdown escapes HTML, rejects unsafe links and never loads images or Mermaid', async () => {
  const text = await readFile(new URL('../src/lib/helper/safe-markdown.ts', import.meta.url), 'utf8');
  const { safeMarkdown } = await load(compile(text).replace("from 'marked'", `from '${import.meta.resolve('marked')}'`));
  const rendered = safeMarkdown('<img src=x onerror=alert(1)>\n\n[bad](javascript:alert%281%29) ![tracking](https://x.example/pixel) [ok](https://example.org)\n\n```mermaid\nflowchart TD\n```');
  assert.ok(rendered.includes('&lt;img'));
  assert.ok(!rendered.includes('<img'));
  assert.ok(!rendered.includes('href="javascript:'));
  assert.ok(rendered.includes('rel="noopener noreferrer"'));
  assert.ok(rendered.includes('<code class="language-mermaid">'));
  assert.ok(!rendered.includes('mermaid-pending'));
});

test('routes preserve Playground and Sessions; keyed native-owner wrapper isolates conversation state', async () => {
  const routes = await readFile(new URL('../src/routes.ts', import.meta.url), 'utf8');
  const wrapper = await readFile(new URL('../src/pages/ChatRoute.svelte', import.meta.url), 'utf8');
  const page = await readFile(new URL('../src/pages/PersonalChat.svelte', import.meta.url), 'utf8');
  assert.match(routes, /'\/playground': guarded\(Chat,/);
  assert.match(routes, /'\/chat': guarded\(ChatRoute,/);
  assert.match(routes, /'\/chat\/:id': guarded\(ChatRoute,/);
  assert.match(routes, /'\/sessions': guarded\(ChatSessions,/);
  assert.match(wrapper, /#if isNativeAdmin\(\)/);
  assert.match(wrapper, /#key.*storeAuth.identity\?\.subject.*params.id/);
  assert.match(wrapper, /!storeAuth.identity && !params.id/);
  assert.match(page, /crypto.randomUUID\(\)/);
  assert.match(page, /lifetime.abort\(\)/);
  assert.match(page, /stream\?\.abort\(\)/);
  assert.match(page, /window.clearInterval\(timer\)/);
  assert.match(source, /import \{ authFetch \} from '.\/transport'/);
  assert.doesNotMatch(page, /getInfo|listProviders|organization_id|regenerate/);
});

const pageSource = await readFile(new URL('../src/pages/PersonalChat.svelte', import.meta.url), 'utf8');
let script = pageSource.match(/<script lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^\s*import[\s\S]*?;/gm, '');
// Execute the real component functions, evaluating derived values on access in
// this rune-free harness (the browser smoke covers actual Svelte reactivity).
const derivedNames = [];
script = script.replace(/const (\w+) = \$derived\((.*)\);/g, (_, name, expression) => {
  derivedNames.push(name);
  return `function read_${name}() { return (${expression}); }`;
});
for (const name of derivedNames) script = script.replace(new RegExp(`\\b${name}\\b`, 'g'), `read_${name}()`);
const pageCode = compile(script);
const settle = () => new Promise(resolve => setImmediate(resolve));
function refreshHarness() {
  let rows = [message({ id: 'z-first', sequence: 1, role: 'user', status: 'completed' }), message({ id: 'y-second', sequence: 2 })];
  let failSnapshot = false;
  let holdOlder;
  const reads = [];
  const storage = new Map();
  let mount, timer;
  const args = {
    ...api,
    $props: () => ({ id: 'c1', owner: 'owner' }), $state: value => value,
    tick: async () => {}, onMount: fn => { mount = fn; },
    sessionStorage: { getItem: key => storage.get(key), setItem: (key, value) => storage.set(key, value), removeItem: key => storage.delete(key) },
    window: { setInterval: fn => { timer = fn; return 1; }, clearInterval() {}, addEventListener() {}, removeEventListener() {} },
    document: { hidden: false, addEventListener() {}, removeEventListener() {} },
    getConversation: async () => ({ id: 'c1', title: 'Conversation' }),
    listConversations: async () => ({ items: [], next_before: '' }), listPersonalModels: async () => [],
    listPersonalMessages: async (_id, _signal, before = '') => {
      reads.push(['page', before]);
      if (before && holdOlder) await holdOlder;
      const sequence = before ? rows.find(row => row.id === before).sequence : Infinity;
      const items = rows.filter(row => row.sequence < sequence).sort((a, b) => b.sequence - a.sequence).slice(0, 50);
      return { items, next_before: items.length === 50 ? items.at(-1).id : '' };
    },
    getPersonalMessage: async (_id, messageID) => {
      reads.push(['snapshot', messageID]);
      if (failSnapshot) throw new Error('snapshot unavailable');
      return rows.find(row => row.id === messageID);
    },
  };
  const page = new Function(...Object.keys(args), `${pageCode}\nreturn { refresh, loadOlder, retainPending, setDraft(value) { draft = value; }, get messages() { return messages; }, get cursor() { return messageCursor; }, get pending() { return pending; }, get draft() { return draft; }, get active() { return read_active(); }, get error() { return error; } };`)(...Object.values(args));
  const cleanup = mount();
  return {
    page, reads, storage, cleanup,
    poll: () => timer(),
    addOtherTurns() {
      rows[1] = { ...rows[1], status: 'completed' };
      rows.push(...Array.from({ length: 52 }, (_, index) => message({ id: `clock-skew-${String(99 - index).padStart(2, '0')}`, sequence: index + 3, request_id: `other-${index}`, role: index % 2 ? 'assistant' : 'user', status: 'completed' })));
    },
    failSnapshot(value) { failSnapshot = value; },
    holdOlder(value) { holdOlder = value; },
  };
}

test('refresh after another client adds 26 turns resets latest window/cursor and all 54 messages remain reachable', async () => {
  const h = refreshHarness();
  await settle();
  assert.equal(h.page.messages.length, 2);
  assert.equal(h.page.cursor, '');
  h.page.setDraft('Keep this unsent draft');
  h.addOtherTurns();
  await h.page.refresh();
  assert.equal(h.page.messages.length, 50);
  assert.equal(h.page.messages[0].sequence, 5);
  assert.ok(h.page.cursor);
  assert.equal(h.page.draft, 'Keep this unsent draft');
  assert.ok(h.reads.some(([kind, id]) => kind === 'snapshot' && id === 'y-second'));
  assert.equal(h.page.active, undefined);
  const readCount = h.reads.length;
  h.poll(); await settle();
  assert.equal(h.reads.length, readCount, 'terminal old assistant stops active polling');
  await h.page.loadOlder();
  assert.deepEqual(h.page.messages.map(row => row.sequence), Array.from({ length: 54 }, (_, index) => index + 1));
  assert.equal(h.page.cursor, '');
  assert.equal(h.page.messages[1].status, 'completed');
  await h.page.refresh();
  assert.equal(h.page.messages.length, 50, 'refresh resets already loaded older pages too');
  assert.ok(h.page.cursor);
  h.cleanup();
});

test('refresh preserves unknown request evidence and reconciles known pending outside latest page by individual GET', async () => {
  for (const known of [false, true]) {
    const h = refreshHarness(); await settle();
    const pending = { request_id: known ? 'r1' : 'uncertain-uuid', content: 'Exact request bytes é ', ...(known ? { assistant_id: 'y-second' } : {}) };
    h.page.retainPending(pending);
    h.page.setDraft('Different unsent draft');
    h.addOtherTurns();
    await h.page.refresh();
    assert.equal(h.page.draft, 'Different unsent draft');
    if (known) {
      assert.equal(h.page.pending, null);
      assert.equal(h.storage.size, 0);
    } else {
      assert.deepEqual(h.page.pending, pending);
      assert.deepEqual(JSON.parse([...h.storage.values()][0]), pending);
    }
    h.cleanup();
  }
});

test('failed reconciliation retains active/pending evidence and original cursor until a successful refresh', async () => {
  const h = refreshHarness(); await settle();
  const pending = { request_id: 'r1', content: 'Exact content', assistant_id: 'y-second' };
  h.page.retainPending(pending);
  h.addOtherTurns(); h.failSnapshot(true);
  await h.page.refresh();
  assert.equal(h.page.messages.length, 2);
  assert.equal(h.page.cursor, '');
  assert.ok(h.page.active);
  assert.deepEqual(h.page.pending, pending);
  assert.match(h.page.error, /snapshot unavailable/);
  h.failSnapshot(false);
  await h.page.refresh();
  assert.equal(h.page.active, undefined);
  assert.equal(h.page.pending, null);
  h.cleanup();
});

test('refresh cannot reset a cursor while an older page request is in flight', async () => {
  const h = refreshHarness(); await settle(); h.addOtherTurns(); await h.page.refresh();
  let release;
  h.holdOlder(new Promise(resolve => { release = resolve; }));
  const older = h.page.loadOlder();
  const count = h.reads.length;
  await h.page.refresh();
  assert.equal(h.reads.length, count);
  release(); await older;
  assert.equal(h.page.messages.length, 54);
  assert.equal(h.page.cursor, '');
  h.cleanup();
});
