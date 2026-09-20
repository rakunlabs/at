import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const source = await readFile(new URL('../src/pages/Chat.svelte', import.meta.url), 'utf8');
const scrollCode = source.slice(source.indexOf('  let followLatest = true;'), source.indexOf('  // ─── Image handling'));

function harness() {
  const frames = [];
  const container = { scrollHeight: 1000, scrollTop: 600, clientHeight: 400 };
  const controls = new Function('chatContainer', 'requestAnimationFrame', `${scrollCode}; return { handleChatScroll, scrollToBottom };`)(container, fn => frames.push(fn));
  return { container, ...controls, flush() { frames.splice(0).forEach(fn => fn()); } };
}

test('streaming follows the bottom but leaves a reader of older messages in place', () => {
  const h = harness();
  h.handleChatScroll();
  h.container.scrollHeight = 1200;
  h.scrollToBottom();
  h.flush();
  assert.equal(h.container.scrollTop, 1200);

  h.container.scrollTop = 300;
  h.handleChatScroll();
  h.container.scrollHeight = 1400;
  h.scrollToBottom();
  h.flush();
  assert.equal(h.container.scrollTop, 300);

  h.container.scrollTop = 1000;
  h.handleChatScroll();
  h.container.scrollHeight = 1600;
  h.scrollToBottom();
  h.flush();
  assert.equal(h.container.scrollTop, 1600);
});

test('scrolling up cancels a queued follow; opening a conversation resets it', () => {
  const h = harness();
  h.scrollToBottom();
  h.container.scrollTop = 100;
  h.handleChatScroll();
  h.flush();
  assert.equal(h.container.scrollTop, 100);
  h.scrollToBottom(true);
  h.flush();
  assert.equal(h.container.scrollTop, 1000);
});
