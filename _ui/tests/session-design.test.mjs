import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const source = await readFile(new URL('../src/pages/ChatSessions.svelte', import.meta.url), 'utf8');
const organization = await readFile(new URL('../src/pages/OrganizationDetail.svelte', import.meta.url), 'utf8');
const chart = await readFile(new URL('../src/lib/components/OrgChart.svelte', import.meta.url), 'utf8');

test('Sessions uses the Chats transcript and composer visual language', () => {
  assert.match(source, /border-l-2 border-accent bg-dark-surface/);
  assert.match(source, /sending \? 'border-oc-peach' : 'border-accent'/);
  assert.doesNotMatch(source, /bg-\[#2B2D42\]|Assistant bubble|User bubble/);
  assert.doesNotMatch(source, /leading-relaxed bg-dark-elevated border border-dark-border-subtle shadow-sm/);
  assert.match(source, /formatMessageTime\(msg\.created_at\)/);
});

test('Sessions retains responsive navigation and the sessions command opens both lists', () => {
  assert.match(source, /case '\/sessions':\s+showSessionList = true;\s+showDesktopSessionList = true;/);
  assert.match(source, /aria-controls="sessions-sidebar"/);
  assert.match(source, /showDesktopSessionList \? 'sm:flex' : 'sm:hidden'/);
  assert.match(source, /group-focus-within:opacity-100/);
});

test('Organization dashboard uses responsive panels and keyboard-accessible agent cards', () => {
  assert.match(organization, /flex-col lg:flex-row/);
  assert.match(organization, /w-full lg:w-72 bg-dark-base/);
  assert.match(organization, /aria-expanded=\{showTaskPanel\}/);
  assert.match(chart, /role="button"\s+tabindex="0"/);
  assert.match(chart, /e\.key === 'Enter' \|\| e\.key === ' '/);
  assert.match(chart, /new ResizeObserver\(\(\) => fitView\(\)\)/);
  assert.match(chart, /observer\.disconnect\(\)/);
  assert.doesNotMatch(chart, /animate-ping|org-edge-pulse/);
});
