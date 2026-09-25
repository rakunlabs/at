import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const calls = [];
globalThis.usageAxios = { create: () => ({ get: async path => { calls.push(path); return { data: { data: [] } }; } }) };
const source = await readFile(new URL('../src/lib/api/usage.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source.replace("import axios from 'axios';", 'const axios = globalThis.usageAxios;'), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const api = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
delete globalThis.usageAxios;

test('dashboard filters reach all aggregates, including explicitly unattributed calls', async () => {
  const filter = { user_id: ['one', 'two', ''], source: ['chats', 'sessions', ''], provider: ['p'], billing_code: ['team-a'], status: 'error', from: '2026-09-01T00:00:00Z' };
  await api.getUsageSummary(filter);
  await api.getUsageTimeSeries(filter, 'hour');
  await api.getUsageGrouped(filter, 'user', 20);
  await api.getUsageGrouped(filter, 'source');
  assert.equal(calls.length, 4);
  for (const path of calls) {
    const url = new URL(path, 'https://example.test');
    assert.deepEqual(url.searchParams.getAll('user_id'), ['one', 'two', '']);
    assert.deepEqual(url.searchParams.getAll('source'), ['chats', 'sessions', '']);
    assert.equal(url.searchParams.get('from'), filter.from);
    assert.equal(url.searchParams.get('provider'), 'p');
    assert.equal(url.searchParams.get('billing_code'), 'team-a');
    assert.equal(url.searchParams.get('status'), 'error');
  }
  assert.equal(new URL(calls[2], 'https://example.test').searchParams.get('group_by'), 'user');
  assert.equal(new URL(calls[3], 'https://example.test').searchParams.get('group_by'), 'source');
});

test('error code grouping is available for the failure taxonomy', async () => {
  await api.getUsageGrouped({ status: 'error' }, 'error_code');
  const url = new URL(calls.at(-1), 'https://example.test');
  assert.equal(url.searchParams.get('status'), 'error');
  assert.equal(url.searchParams.get('group_by'), 'error_code');
});
