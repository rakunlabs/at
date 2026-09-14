import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { fromStore, writable } from 'svelte/store';
import ts from 'typescript';

async function load(path, replacements = []) {
  let source = await readFile(new URL(path, import.meta.url), 'utf8');
  for (const [before, after] of replacements) source = source.replace(before, after);
  const code = ts.transpileModule(source, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
  }).outputText;
  return import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
}

const { updateRouteQuery } = await load('../src/lib/helper/route-query.ts');
globalThis.routeQueryTest = { fromStore, querystring: writable(''), updateRouteQuery };
const { routeChoice } = await load('../src/lib/helper/route-choice.svelte.ts', [
  ["import { fromStore } from 'svelte/store';", 'const { fromStore } = globalThis.routeQueryTest;'],
  ["import { querystring } from 'svelte-spa-router';", 'const { querystring } = globalThis.routeQueryTest;'],
  ["import { updateRouteQuery } from './route-query';", 'const { updateRouteQuery } = globalThis.routeQueryTest;'],
]);

// Model browser history and hashchange delivery. No backend or DOM is required.
function browser(initial) {
  const history = [initial];
  let index = 0;
  let notifications = 0;
  function notify() {
    notifications++;
    globalThis.routeQueryTest.querystring.set(history[index].split('?')[1] || '');
  }
  globalThis.window = {
    location: {
      get hash() { return history[index]; },
      set hash(value) {
        history.splice(++index);
        history.push(value);
        notify();
      },
    },
    history: {
      state: { retained: true },
      replaceState(state, _, hash) {
        assert.deepEqual(state, { retained: true });
        history[index] = hash;
      },
    },
    dispatchEvent(event) { assert.equal(event.type, 'hashchange'); notify(); },
  };
  notify();
  return {
    history,
    back() { if (index > 0) { index--; notify(); } },
    forward() { if (index < history.length - 1) { index++; notify(); } },
    get notifications() { return notifications; },
  };
}

test('folder navigation preserves other parameters and round-trips special filenames', () => {
  const b = browser('#/files?filter=keep');
  const path = 'assets/Türkçe & 日本語/#1 + 100%/a?b';
  updateRouteQuery({ path });
  const query = new URLSearchParams(window.location.hash.split('?')[1]);
  assert.equal(query.get('path'), path);
  assert.equal(query.get('filter'), 'keep');
  updateRouteQuery({ path: 'assets/uploads' });
  b.back();
  assert.equal(new URLSearchParams(window.location.hash.split('?')[1]).get('path'), path);
  b.forward();
  assert.equal(new URLSearchParams(window.location.hash.split('?')[1]).get('path'), 'assets/uploads');
  updateRouteQuery({ path: null });
  assert.equal(window.location.hash, '#/files?filter=keep');
});

test('tab selections restore direct links and follow back/forward without extra entries', () => {
  const b = browser('#/skills?tab=community');
  const tab = routeChoice('tab', ['my-skills', 'store', 'community'], 'my-skills');
  assert.equal(tab.value, 'community');
  tab.value = 'store';
  assert.equal(tab.value, 'store');
  tab.value = 'store';
  assert.equal(b.history.length, 2);
  b.back();
  assert.equal(tab.value, 'community');
  b.forward();
  assert.equal(tab.value, 'store');
  tab.value = 'my-skills';
  assert.equal(window.location.hash, '#/skills');
  assert.equal(tab.value, 'my-skills');
});

test('invalid tab links use the default without rewriting unrelated parameters', () => {
  browser('#/tasks/task-1?tab=unknown&task_ids=A%2CB');
  const tab = routeChoice('tab', ['activity', 'events'], 'activity');
  assert.equal(tab.value, 'activity');
  tab.value = 'events';
  assert.equal(window.location.hash, '#/tasks/task-1?tab=events&task_ids=A%2CB');
});

test('deleting the selected session replaces its history entry and notifies the router', () => {
  const b = browser('#/sessions');
  updateRouteQuery({ session: 'one' });
  updateRouteQuery({ session: 'two' });
  const before = b.notifications;
  updateRouteQuery({ session: null }, true);
  assert.equal(b.history.length, 3);
  assert.equal(b.notifications, before + 1);
  assert.equal(window.location.hash, '#/sessions');
  b.back();
  assert.equal(window.location.hash, '#/sessions?session=one');
});
