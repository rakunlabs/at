import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const chat = await readFile(new URL('../src/pages/Chat.svelte', import.meta.url), 'utf8');
const navbar = await readFile(new URL('../src/lib/components/Navbar.svelte', import.meta.url), 'utf8');

test('chat panel toggles live in the shell navbar, not the scrolling title', () => {
  assert.match(navbar, /\{#if storeNavbar\.chatPanels\}/);
  for (const panel of ['Conversations', 'Session']) {
    assert.match(navbar, new RegExp(`chatPanels\\?\\.toggle${panel}\\(\\)`));
  }
  assert.match(navbar, /aria-expanded=\{storeNavbar\.chatPanels\.conversationsOpen\}/);
  assert.match(navbar, /aria-expanded=\{storeNavbar\.chatPanels\.sessionOpen\}/);
  const title = chat.slice(chat.indexOf('<!-- Title block:'), chat.indexOf('</header>', chat.indexOf('<!-- Title block:')));
  assert.doesNotMatch(title, /PanelLeft|PanelRight|showConversations|showSessionPanel/);
  assert.match(title, /conversationTitle/);
});

test('chat publishes reactive panel state and removes controls on destruction', () => {
  assert.match(chat, /\$effect\(\(\) => \{\s*storeNavbar\.chatPanels = \{\s*conversationsOpen: showConversations,\s*sessionOpen: showSessionPanel,/);
  assert.match(chat, /onDestroy\(\(\) => \{\s*storeNavbar\.chatPanels = null;/);
});
