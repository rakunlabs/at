import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

async function load(path) {
  const source = await readFile(new URL(path, import.meta.url), 'utf8');
  const code = ts.transpileModule(source, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
  }).outputText;

  return import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
}

const helper = await load('../src/lib/helper/local-mcp.ts');
const client = await load('../src/lib/helper/local-mcp-client.ts');

// The host rule is what keeps a browser-dialled MCP from becoming a way around
// workspace execution policy: a public endpoint routed through the page would
// run with no execution check and no server-side trace.
test('only local addresses are accepted', () => {
  for (const ok of [
    'http://127.0.0.1:3000/mcp',
    'http://localhost:8787/mcp',
    'http://tools.localhost:8787/mcp',
    'http://[::1]:3000/mcp',
    'http://192.168.1.20:3000/mcp',
    'http://10.1.2.3/mcp',
    'http://172.16.0.9/mcp',
    'http://100.101.102.103:3000/mcp',
    'http://169.254.4.5/mcp',
    'http://studio.local:3000/mcp',
    'https://192.168.1.20:8443/mcp',
  ]) {
    assert.equal(helper.localMCPUrlProblem(ok), '', ok);
  }

  for (const bad of [
    'https://mcp.example.com/mcp',
    'http://8.8.8.8/mcp',
    'http://172.32.0.1/mcp',
    'http://host.docker.internal:3000/mcp',
    'ws://127.0.0.1:3000/mcp',
    'http://user:pass@127.0.0.1:3000/mcp',
    '',
  ]) {
    assert.notEqual(helper.localMCPUrlProblem(bad), '', bad);
  }
});

// The endpoint path is the user's; nothing appends /mcp to it.
test('a nested endpoint path is accepted unchanged', () => {
  assert.equal(helper.localMCPUrlProblem('http://127.0.0.1:3000/mcp/api'), '');
  assert.equal(helper.localMCPUrlProblem('http://127.0.0.1:3000/sse'), '');
});

test('a local tool never displaces an existing tool name', () => {
  const taken = new Set(['search', 'laptop__search']);
  const has = name => taken.has(name);
  assert.equal(helper.localMCPToolName('notes', 'laptop', has), 'notes');
  assert.equal(helper.localMCPToolName('search', 'laptop', has), 'laptop2__search');
  assert.equal(helper.localMCPToolName('search', 'My Desk!', has), 'my_desk__search');
});

test('results are bounded with a notice that names the original size', () => {
  const big = 'x'.repeat(helper.LOCAL_TOOL_RESULT_MAX + 10);
  const clipped = helper.clipLocalToolResult(big);
  assert.ok(clipped.length < big.length + 200);
  assert.match(clipped, /truncated: the tool returned \d+ characters/);
  assert.equal(helper.clipLocalToolResult('short'), 'short');
});

// Approval is per device because the same loopback URL is a different program
// on each machine; a synced approval would authorize something never inspected.
test('approval is stored per device and reports tools added since', () => {
  const store = new Map();
  const storage = {
    getItem: k => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => store.set(k, v),
  };

  assert.equal(helper.approvalFor('srv-1', storage), null);
  helper.approveLocalMCP('srv-1', ['a', 'b'], storage);
  const approval = helper.approvalFor('srv-1', storage);
  assert.deepEqual(approval.tools, ['a', 'b']);
  assert.deepEqual(helper.toolsAddedSinceApproval(approval, ['a', 'b', 'c']), ['c']);
  assert.deepEqual(helper.toolsAddedSinceApproval(null, ['a']), [], 'an unapproved server has nothing to compare');

  helper.revokeLocalMCP('srv-1', storage);
  assert.equal(helper.approvalFor('srv-1', storage), null);

  // A second device has its own storage and starts unapproved.
  assert.equal(helper.approvalFor('srv-1', undefined), null);
});

function stubResponse({ status = 200, body = '', headers = {} } = {}) {
  const map = new Map(Object.entries(headers).map(([k, v]) => [k.toLowerCase(), v]));

  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: k => map.get(String(k).toLowerCase()) ?? null },
    text: async () => body,
  };
}

function rpc(id, result) {
  return JSON.stringify({ jsonrpc: '2.0', id, result });
}

test('the configured path is dialled and JSON results are read', async () => {
  const seen = [];
  const fetchImpl = async (url, init) => {
    const req = JSON.parse(init.body);
    seen.push({ url, method: req.method, headers: init.headers });
    if (req.method === 'initialize') {
      return stubResponse({
        body: rpc(req.id, { protocolVersion: '2025-03-26' }),
        headers: { 'Content-Type': 'application/json', 'Mcp-Session-Id': 'sess-1' },
      });
    }
    if (req.method === 'tools/list') {
      return stubResponse({ body: rpc(req.id, { tools: [{ name: 't1', description: 'd' }] }), headers: { 'Content-Type': 'application/json' } });
    }
    if (req.method === 'tools/call') {
      return stubResponse({ body: rpc(req.id, { content: [{ type: 'text', text: 'done' }] }), headers: { 'Content-Type': 'application/json' } });
    }

    return stubResponse({ status: 202 });
  };

  const c = new client.LocalMCPClient('http://127.0.0.1:3000/mcp/api', { fetchImpl, headers: { Authorization: 'Bearer x' } });
  const tools = await c.listTools();
  assert.deepEqual(tools.map(t => t.name), ['t1']);
  assert.equal(await c.callTool('t1', {}), 'done');

  assert.ok(seen.every(s => s.url === 'http://127.0.0.1:3000/mcp/api'), 'the configured path must not be rewritten');
  assert.equal(seen[0].headers.Authorization, 'Bearer x');
  const listCall = seen.find(s => s.method === 'tools/list');
  assert.equal(listCall.headers['Mcp-Session-Id'], 'sess-1', 'an exposed session id is used on later requests');
  assert.equal(listCall.headers['MCP-Protocol-Version'], '2025-03-26');
});

// Without Access-Control-Expose-Headers the session cannot be read. Failing
// there would look like a protocol fault; proceeding statelessly is what the
// server asked for by not exposing it.
test('a server that exposes no session id is used statelessly', async () => {
  const seen = [];
  const fetchImpl = async (_url, init) => {
    const req = JSON.parse(init.body);
    seen.push(init.headers);
    if (req.method === 'initialize') {
      return stubResponse({ body: rpc(req.id, {}), headers: { 'Content-Type': 'application/json' } });
    }

    return stubResponse({ body: rpc(req.id, { tools: [] }), headers: { 'Content-Type': 'application/json' } });
  };

  const c = new client.LocalMCPClient('http://127.0.0.1:3000/mcp', { fetchImpl });
  assert.deepEqual(await c.listTools(), []);
  assert.ok(seen.every(h => !('Mcp-Session-Id' in h)));
});

test('an SSE response body is parsed', async () => {
  const fetchImpl = async (_url, init) => {
    const req = JSON.parse(init.body);
    if (req.method === 'initialize') {
      return stubResponse({ body: rpc(req.id, {}), headers: { 'Content-Type': 'application/json' } });
    }

    return stubResponse({
      body: `event: message\ndata: ${rpc(req.id, { tools: [{ name: 'sse-tool' }] })}\n\n`,
      headers: { 'Content-Type': 'text/event-stream' },
    });
  };

  const c = new client.LocalMCPClient('http://127.0.0.1:3000/mcp', { fetchImpl });
  assert.deepEqual((await c.listTools()).map(t => t.name), ['sse-tool']);
  assert.equal(client.parseSSE('data: {"result":{"ok":1}}\n\n').result.ok, 1);
  assert.equal(client.parseSSE(': keep-alive\n\n'), null);
});

// "Failed to fetch" is an error the user cannot act on. A browser cannot tell
// a refused connection from a refused cross-origin request, so the hint names
// both rather than pretending to know.
test('a refused request reports the headers the MCP server must send', async () => {
  const fetchImpl = async () => { throw new TypeError('Failed to fetch'); };
  const c = new client.LocalMCPClient('http://127.0.0.1:3000/mcp', { fetchImpl });
  await assert.rejects(() => c.listTools(), err => {
    assert.equal(err.kind, 'blocked');
    assert.match(err.hint, /Access-Control-Allow-Origin/);
    assert.match(err.hint, /Access-Control-Expose-Headers/);
    assert.match(err.hint, /Access-Control-Allow-Private-Network/);

    return true;
  });
});

test('an MCP error is a tool failure, not a transport failure', async () => {
  const fetchImpl = async (_url, init) => {
    const req = JSON.parse(init.body);
    if (req.method === 'initialize') {
      return stubResponse({ body: rpc(req.id, {}), headers: { 'Content-Type': 'application/json' } });
    }

    return stubResponse({
      body: JSON.stringify({ jsonrpc: '2.0', id: req.id, error: { code: -32000, message: 'no such tool' } }),
      headers: { 'Content-Type': 'application/json' },
    });
  };

  const c = new client.LocalMCPClient('http://127.0.0.1:3000/mcp', { fetchImpl });
  await assert.rejects(() => c.callTool('nope', {}), err => err.kind === 'tool' && /no such tool/.test(err.message));
});

test('a cancelled turn aborts the call', async () => {
  const controller = new AbortController();
  const fetchImpl = async (_url, init) => new Promise((_resolve, reject) => {
    init.signal.addEventListener('abort', () => reject(Object.assign(new Error('aborted'), { name: 'AbortError' })));
  });
  const c = new client.LocalMCPClient('http://127.0.0.1:3000/mcp', { fetchImpl, signal: controller.signal });
  const pending = c.listTools();
  controller.abort();
  await assert.rejects(() => pending, err => err.message === 'cancelled');
});
