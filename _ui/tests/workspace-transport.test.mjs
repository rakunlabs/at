import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';
import axios from 'axios';
const source = await readFile(new URL('../src/lib/api/workspace-transport.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, {compilerOptions: {target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022}}).outputText.replace("from 'axios'", `from '${import.meta.resolve('axios')}'`);
const {createWorkspaceTransport} = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
const deferred = () => { let resolve; const promise = new Promise(r => resolve = r); return {promise, resolve}; };
test('independent tabs select independent workspaces across axios and fetch', async () => {
  for (const selected of ['workspace-a','workspace-b']) {
    const transport = createWorkspaceTransport('https://at.example/nested/at/', selected);
    const api = axios.create({baseURL: 'api/v1', adapter: transport.wrapAdapter(async config => ({config, data: config.headers.get('X-AT-Workspace-ID'), status:200, headers:{}, statusText:'OK'}))});
    assert.equal((await api.get('tasks')).data, selected);
    assert.equal((await api.get('tasks', {headers:{'X-AT-Workspace-ID':'forged'}})).data, selected);
    const fetcher = transport.wrapFetch(async request => { assert.equal(request.headers.get('X-AT-Workspace-ID'), selected); return new Response('ok'); });
    await fetcher('api/v1/workflows/id/run', {method:'POST', body:'{}'});
  }
});
test('self, platform and foreign-origin calls do not acquire workspace headers', async () => {
  const transport = createWorkspaceTransport('https://at.example/at/', 'a');
  for (const path of ['auth/status','auth/workspaces','auth/settings','api/v1/settings/rotate-key','api/v1/features','api/v1/workspaces','https://other.example/api/v1/tasks','gateway/v1/chat/completions']) {
    const api = axios.create({adapter:transport.wrapAdapter(async config => { assert.equal(config.headers.has('X-AT-Workspace-ID'), false, path); return {data:{}, status:200, headers:{}, config, statusText:'OK'}; })});
    await api.get(path);
  }
});
test('switch aborts old streams and fences adapters that return after cancellation', async () => {
  const transport = createWorkspaceTransport('https://at.example/at/', 'a');
  const wait = deferred(); let signal;
  const api = axios.create({baseURL:'api/v1', adapter:transport.wrapAdapter(async config => { signal = config.signal; await wait.promise; return {data:'old',status:200,headers:{},config,statusText:'OK'}; })});
  const request = api.get('tasks');
  await Promise.resolve(); transport.select('b'); assert.equal(signal.aborted,true); wait.resolve();
  await assert.rejects(request, error => axios.isCancel(error));
  let streamSignal;
  const fetcher = transport.wrapFetch(async request => { streamSignal = request.signal; return new Response(new ReadableStream({ start(controller) { request.signal.addEventListener('abort', () => controller.error(new DOMException('Workspace changed','AbortError'))); } })); });
  const response = await fetcher('api/v1/chat/sessions/s/messages');
  const pending = response.body.getReader().read(); transport.select('c'); assert.equal(streamSignal.aborted,true);
  await assert.rejects(pending, error => error.name === 'AbortError');
});
test('late fetch response is discarded after selection changes', async () => {
  const transport = createWorkspaceTransport('https://at.example/', 'a'); const wait = deferred();
  const fetcher = transport.wrapFetch(async () => { await wait.promise; return new Response('old workspace'); });
  const pending = fetcher('api/v1/tasks'); transport.select('b'); wait.resolve();
  await assert.rejects(pending, error => error.name === 'AbortError');
});
