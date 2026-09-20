import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test, beforeEach } from 'node:test';
import ts from 'typescript';

let enabled = true;
let prepare = async () => {};
globalThis.pageLoadFeatures = { isFeatureEnabled: () => enabled, loadFeatures: () => prepare() };
const source = (await readFile(new URL('../src/lib/helper/page-load.svelte.ts', import.meta.url), 'utf8'))
  .replace("import { isFeatureEnabled, loadFeatures } from '../store/features.svelte';", 'const { isFeatureEnabled, loadFeatures } = globalThis.pageLoadFeatures; const $state = value => value;')
  .replace("import { authErrorMessage } from '../api/auth';", 'const authErrorMessage = (error, fallback) => error.message || fallback;');
const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const { createPageLoader } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
delete globalThis.pageLoadFeatures;
beforeEach(() => { enabled = true; prepare = async () => {}; });

test('an optional failure does not erase or block the primary collection', async () => {
  const loader = createPageLoader();
  let agents = ['previous'];
  let skills = ['previous-skill'];
  await Promise.all([
    loader.load('Agents', async () => [], value => { agents = value; }),
    loader.load('Skills', async () => { throw new Error('offline'); }, value => { skills = value; }),
  ]);
  assert.deepEqual(agents, []);
  assert.deepEqual(skills, ['previous-skill']);
  assert.equal(loader.error('Agents'), '');
  assert.match(loader.error('Skills'), /offline/);
});

test('a disabled feature skips the request and exposes an unavailable state', async () => {
  enabled = false;
  const loader = createPageLoader();
  let called = false;
  await loader.load('Skills', async () => { called = true; return []; }, () => assert.fail('must not overwrite data'), 'skills');
  assert.equal(called, false);
  assert.equal(loader.issues[0].disabled, true);
});

test('failed feature discovery defers to the endpoint rather than inventing an empty list', async () => {
  prepare = async () => { throw new Error('catalog offline'); };
  const loader = createPageLoader();
  let value;
  await loader.load('Agents', async () => ['one'], result => { value = result; }, 'agents');
  assert.deepEqual(value, ['one']);
});

test('404, 403 and feature-disabled responses stay distinct from a successful empty list', async () => {
  for (const [status, data, pattern, disabled] of [
    [404, {}, /not an empty list/, false],
    [403, {}, /do not have access/, false],
    [404, { code: 'feature_disabled' }, /feature is disabled/, true],
  ]) {
    const loader = createPageLoader();
    await loader.load('Agents', async () => { throw { response: { status, data } }; }, () => assert.fail('failed request applied'));
    assert.match(loader.error('Agents'), pattern);
    assert.equal(loader.issues[0].disabled, disabled);
  }
});

test('a late response from an earlier load cannot overwrite refreshed state', async () => {
  const loader = createPageLoader();
  let resolve;
  let value;
  const pending = loader.load('Agents', () => new Promise(done => { resolve = done; }), result => { value = result; });
  loader.reset();
  await loader.load('Agents', async () => ['new'], result => { value = result; });
  resolve(['old']);
  await pending;
  assert.deepEqual(value, ['new']);
});

test('a successful retry clears the error without requiring a whole-page reset', async () => {
  const loader = createPageLoader();
  await loader.load('Agents', async () => { throw new Error('offline'); }, () => {});
  assert.equal(loader.issues.length, 1);
  await loader.load('Agents', async () => [], () => {});
  assert.equal(loader.issues.length, 0);
});

test('primary loading finishes while an independent optional request is still pending', async () => {
  const loader = createPageLoader();
  let resolve;
  const optional = loader.load('Skills', () => new Promise(done => { resolve = done; }), () => {});
  assert.equal(loader.loading('Skills'), true);
  await loader.load('Agents', async () => [], () => {});
  assert.equal(loader.loading('Agents'), false);
  assert.equal(loader.loading('Skills'), true);
  resolve([]);
  await optional;
  assert.equal(loader.loading('Skills'), false);
});
