import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';
import { moduleURL } from './typescript-module.mjs';

const { createAdaptivePoll } = await import(await moduleURL(new URL('../src/lib/helper/adaptive-poll.ts', import.meta.url)));
const flush = async () => { for (let i = 0; i < 10; i++) await Promise.resolve(); };

test('polling backs off unchanged data, resets on changes, and pauses offline', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  let calls = 0, active = true, changed = false;
  const poll = createAdaptivePoll({ minimum: 10, maximum: 40, active: () => active, poll: async () => { calls++; return changed; } });
  t.mock.timers.tick(10); await flush(); assert.equal(calls, 1);
  t.mock.timers.tick(19); await flush(); assert.equal(calls, 1);
  t.mock.timers.tick(1); await flush(); assert.equal(calls, 2);
  active = false;
  t.mock.timers.tick(40); await flush(); assert.equal(calls, 2);
  active = true; changed = true;
  poll.wake(); await flush(); assert.equal(calls, 3);
  t.mock.timers.tick(10); await flush(); assert.equal(calls, 4);
  poll.stop(); t.mock.timers.tick(100); await flush(); assert.equal(calls, 4);
});

test('waking or stopping during a slow poll cannot overlap or restart it', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  let resolve, calls = 0;
  const pending = new Promise(done => { resolve = done; });
  const poll = createAdaptivePoll({ minimum: 10, active: () => true, poll: async () => { calls++; await pending; return false; } });
  poll.wake(); poll.wake(); t.mock.timers.tick(100); await flush();
  assert.equal(calls, 1);
  poll.stop(); resolve(); await flush(); t.mock.timers.tick(100); await flush();
  assert.equal(calls, 1);
});

test('the actual shell keeps a ready chat mounted after network failure, but not after auth denial', async () => {
  const source = await readFile(new URL('../src/App.svelte', import.meta.url), 'utf8');
  const script = source.slice(source.indexOf('>') + 1, source.indexOf('</script>'));
  const ast = ts.createSourceFile('App.ts', script, ts.ScriptTarget.Latest, true);
  const fn = ast.statements.find(n => ts.isFunctionDeclaration(n) && n.name?.text === 'checkSession').getText(ast);
  const code = ts.transpileModule(`export async function run(state, failure) {
    let checking = false, revision = 0, ticket = '', error = '', notice = '';
    let authState = state;
    const storeAuth = { securityHold: false, identity: { subject: 'user' } };
    class ReauthenticationRequired extends Error {}
    const authSession = { checkSession: async () => { if (failure === 'auth') throw new ReauthenticationRequired(); throw new Error('offline'); } };
    const isSetupRequired = () => false, isAuthUnauthorized = () => false;
    ${fn}
    await checkSession(); return { authState, error, identity: storeAuth.identity };
  }`, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
  const { run } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
  assert.equal((await run('ready', 'network')).authState, 'ready');
  assert.equal((await run('loading', 'network')).authState, 'error');
  assert.equal((await run('ready', 'auth')).authState, 'login');
  assert.equal((await run('ready', 'auth')).identity, null);
});
