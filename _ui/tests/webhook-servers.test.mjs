import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/api/webhook-servers.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source.replace("import axios from 'axios';", 'const axios = { create: () => ({}) };'), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const { webhookServerBaseUrl, webhookServerUrl } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

const here = { protocol: 'https:', hostname: 'at.example' };
const server = (extra = {}) => ({ public_url: '', port: 5050, base_path: '', tls_cert: '', bind_host: '', ...extra });

test('a listener on all interfaces is reached on the current host at its own port', () => {
  assert.equal(webhookServerBaseUrl(server(), here), 'http://at.example:5050');
  assert.equal(webhookServerBaseUrl(server({ bind_host: '0.0.0.0' }), here), 'http://at.example:5050');
  assert.equal(webhookServerUrl(server({ base_path: '/hooks' }), '/github/push', here), 'http://at.example:5050/hooks/github/push');
});

test('TLS, explicit bind addresses and public URLs shape the URL', () => {
  // Members receive '***' instead of the certificate; it still means HTTPS.
  assert.equal(webhookServerBaseUrl(server({ tls_cert: '***' }), here), 'https://at.example:5050');
  assert.equal(webhookServerBaseUrl(server({ bind_host: '10.0.0.5' }), here), 'http://10.0.0.5:5050');
  assert.equal(webhookServerBaseUrl(server({ bind_host: 'fd00::1' }), here), 'http://[fd00::1]:5050');
  assert.equal(webhookServerUrl(server({ public_url: 'https://hooks.example.com/', base_path: '/ignored' }), 'orders', here), 'https://hooks.example.com/orders');
});
