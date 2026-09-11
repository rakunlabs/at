import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import vm from 'node:vm';
const source = await readFile(new URL('../public/workspace-media.js', import.meta.url), 'utf8');
function worker() {
  const listeners = {}; const requests = []; const ids = new Map([['tab-a','workspace-a'],['tab-b','workspace-b']]);
  const context = vm.createContext({ URL, Headers, Request, Response, ReadableStream, AbortController, MessageChannel, setTimeout, clearTimeout,
    self: { registration:{scope:'https://at.example/at/'}, addEventListener:(type,fn) => listeners[type]=fn,
      clients:{ async get(id) { if (!ids.has(id)) return null; return { url:'https://at.example/at/#/files', postMessage(message,ports) { assert.equal(message.type,'at-workspace-media-context'); ports[0].postMessage({workspace_id:ids.get(id)}); } }; } },
    },
    fetch: async request => { requests.push(request); return new Response('media', {status:206,headers:{'Content-Range':'bytes 0-4/5'}}); },
  });
  vm.runInContext(source,context);
  const dispatch = (clientId, path='api/v1/files/serve?path=assets/video.mp4',headers={Range:'bytes=0-4'}) => {
    let response;
    listeners.fetch({clientId,request:new Request(new URL(path,'https://at.example/at/'),{headers}),respondWith(value){response=value;}});
    return response;
  };
  return {dispatch,requests,listeners};
}
test('media Range requests use the requesting tab scope, not a shared selection', async () => {
  const h=worker();
  const responses=await Promise.all([h.dispatch('tab-a'),h.dispatch('tab-b')]);
  assert.deepEqual(await Promise.all(responses.map(r=>r.text())),['media','media']);
  assert.deepEqual(h.requests.map(r=>r.headers.get('X-AT-Workspace-ID')).sort(),['workspace-a','workspace-b']);
  for (const request of h.requests) {assert.equal(request.headers.get('Range'),'bytes=0-4');assert.equal(request.cache,'no-store');assert.equal(request.redirect,'error');}
});
test('unknown media clients fail closed; unrelated or already scoped requests bypass the bridge', async () => {
  const h=worker(); assert.equal((await h.dispatch('unknown')).status,403);assert.equal(h.requests.length,0);
  for (const path of ['auth/me','api/v1/tasks','https://foreign.example/at/api/v1/files/serve']) assert.equal(h.dispatch('tab-a',path),undefined);
  assert.equal(h.dispatch('tab-a','api/v1/files/serve',{ 'X-AT-Workspace-ID':'workspace-a' }),undefined);
});
test('workspace switch cancels only that client’s active media', async () => {
  const h=worker();const a=await h.dispatch('tab-a');const b=await h.dispatch('tab-b');
  h.listeners.message({source:{id:'tab-a'},data:{type:'at-cancel-workspace-media'}});
  assert.equal(h.requests[0].signal.aborted,true);assert.equal(h.requests[1].signal.aborted,false);
  await a.body.cancel();assert.equal(await b.text(),'media');
});
