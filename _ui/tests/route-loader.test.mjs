import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';
import { moduleURL } from './typescript-module.mjs';
const { createRouteLoader } = await import(await moduleURL(new URL('../src/lib/helper/route-loader.ts', import.meta.url)));

test('workspace admission screen does not eagerly import the lazy settings page', async () => {
  const source = await readFile(new URL('../src/App.svelte', import.meta.url), 'utf8');
  assert.doesNotMatch(source, /^\s*import .*from ['"].*\/WorkspaceSettings\.svelte['"]/m);
  assert.match(source, /\(\) => import\('\.\/pages\/WorkspaceSettings\.svelte'\)/);
  assert.match(source, /\{#await loadWorkspaceSettings\(\)\}/);
  assert.match(source, /\(\) => \(\{ default: RouteLoadError \}\)/);
});

test('simultaneous route visits share one import and keep the same loaded component', async () => {
  let resolve, calls = 0;
  const module = { default: {} };
  const loader = createRouteLoader(() => { calls++; return new Promise(done => { resolve = done; }); }, () => { throw new Error('unexpected failure'); });
  const first = loader(), next = loader();
  assert.equal(first, next);
  await Promise.resolve(); resolve(module);
  assert.equal(await first, module);
  assert.equal(await loader(), module);
  assert.equal(calls, 1);
});

test('offline or stale chunks show a fallback and are retryable on a later visit', async () => {
  const fallback = { default: 'error' }, module = { default: 'page' };
  let attempts = 0;
  const loader = createRouteLoader(async () => { if (++attempts === 1) throw new TypeError('failed to fetch'); return module; }, () => fallback);
  assert.equal(await loader(), fallback);
  assert.equal(await loader(), module);
  assert.equal(attempts, 2);
});

test('real route table is lazy, preserves guards, redirects, and the single Chats entry', async () => {
  let imports = 0, enabled = true, admin = false;
  const redirects = [];
  const source = await readFile(new URL('../src/routes.ts', import.meta.url), 'utf8');
  assert.doesNotMatch(source, /^import .*from ['"]@\/pages\//m);
  assert.equal((source.match(/'\/chats\/:id\?':/g) ?? []).length, 1);
  let code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
  code = code.replace(/^import .*;$/gm, '').replace(/import\('(@\/pages\/[^']+)'\)/g, (_, path) => `globalThis.routeFixture.load(${JSON.stringify(path)})`);
  globalThis.routeFixture = { load: async path => { imports++; return { default: path }; } };
  const prefix = `const wrap = options => ({ component: options.asyncComponent, conditions: options.conditions ?? [], loading: options.loadingComponent });
    const { createRouteLoader, push, isNativeAdmin, isFeatureEnabled } = globalThis.routeFixture;
    const RouteLoading = {}, RouteLoadError = {};
    const routeFeature = route => route, loadFeatures = async () => {};`;
  Object.assign(globalThis.routeFixture, { createRouteLoader, push: route => redirects.push(route), isNativeAdmin: () => admin, isFeatureEnabled: () => enabled });
  const { default: routes } = await import(`data:text/javascript;base64,${Buffer.from(prefix + code).toString('base64')}`);
  assert.equal(imports, 0, 'building the registry must not import pages');
  const chats = routes['/chats/:id?'];
  for (const condition of chats.conditions) assert.equal(await condition(), true);
  assert.equal((await chats.component()).default, '@/pages/Chat.svelte');
  assert.equal(imports, 1);
  assert.equal((await chats.component()).default, '@/pages/Chat.svelte');
  assert.equal(imports, 1);
  enabled = false;
  assert.equal(await chats.conditions.at(-1)(), false);
  assert.equal(redirects.at(-1), '/');
  assert.equal(await routes['/terminal'].conditions[0](), false);
  admin = true; assert.equal(await routes['/terminal'].conditions[0](), true);
  assert.equal(await routes['/tokens'].conditions[0](), false);
  assert.equal(redirects.at(-1), '/settings/tokens');
  assert.equal(imports, 1, 'guards/redirects must not download refused pages');
  delete globalThis.routeFixture;
});
