import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { beforeEach, test } from 'node:test';
import ts from 'typescript';

let calls = [];
let failure;
const response = { settings: { version: 2 }, active: { version: 1 }, apply_error: 'drain failed' };
globalThis.systemAxiosMock = {
  create(config) {
    assert.deepEqual(config, { baseURL: 'api/v1' });
    return Object.fromEntries(['get', 'put'].map(method => [method, async (...args) => {
      calls.push([method, ...args]);
      if (failure) throw failure;
      return { data: response };
    }]));
  },
};
const source = await readFile(new URL('../src/lib/api/system-settings.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source.replace("import axios from 'axios';", 'const axios = globalThis.systemAxiosMock;'), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const api = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
delete globalThis.systemAxiosMock;
beforeEach(() => { calls = []; failure = undefined; });

test('system settings retain saved, active and failed-application states', async () => {
  assert.deepEqual(await api.getSystemSettings(), response);
  assert.deepEqual(calls, [['get', 'settings/system']]);
});

test('live writes send explicit stop and single-replica confirmations with the version', async () => {
  const settings = { version: 7, sandbox: { backend: 'docker' } };
  assert.deepEqual(await api.saveSystemSettings(settings, true, true), response);
  assert.deepEqual(calls, [['put', 'settings/system', { settings, confirm_stop: true, single_replica: true }, { timeout: 150_000 }]]);
});

test('a failed live write is never repeated automatically', async () => {
  failure = new Error('network disconnected');
  await assert.rejects(api.saveSystemSettings({ version: 7 }, false, false), /network disconnected/);
  assert.equal(calls.length, 1);
});

test('System exposes all Kubernetes fields, interrupted-work warnings and recovery', async () => {
  const page = await readFile(new URL('../src/pages/SystemSettings.svelte', import.meta.url), 'utf8');
  for (const field of ['namespace', 'deployment_id', 'kubeconfig', 'helper_image', 'storage_class', 'home_storage_class', 'home_access_mode', 'home_size', 'workspace_size', 'runtime_class', 'pod_pids_limit', 'network_policy_enforced', 'single_replica', 'blocked_cidrs']) {
    assert.ok(page.includes(field), `missing ${field}`);
  }
  assert.match(page, /Save, stop sandbox work & apply/);
  assert.match(page, /AT does not migrate projects/);
  assert.match(page, /Reload settings/);
  assert.match(page, /e\?\.response\?\.data\?\.settings/);
  assert.doesNotMatch(await readFile(new URL('../src/lib/api/admin.ts', import.meta.url), 'utf8'), /adminToken|Authorization/);
});
