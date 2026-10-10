import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

async function load(file, replacements = []) {
  let source = await readFile(new URL(file, import.meta.url), 'utf8');
  for (const [from, to] of replacements) source = source.replace(from, to);
  const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
  return import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
}

const helper = await load('../src/lib/helper/developer-space.ts');
const pageSource = await readFile(new URL('../src/pages/DeveloperSpaces.svelte', import.meta.url), 'utf8');
const chatSource = await readFile(new URL('../src/lib/components/developer/SessionChat.svelte', import.meta.url), 'utf8');

test('Kubernetes stop warns about package loss and displays runtime limitations', () => {
  const stop = pageSource.match(/async function stop\(\) \{([\s\S]*?)\n  \}/)[1];
  assert.match(stop, /runtime\?\.backend === 'kubernetes'/);
  assert.match(stop, /installed system packages will be lost/);
  assert.match(pageSource, /space\.runtime\.notice/);
  assert.match(pageSource, /Use a prepared Linux image/);
});

test('developer chat detaches on destruction and resumes discovered runs without POST', () => {
  assert.match(chatSource, /controller\?\.abort\(\); \/\/ Detach only/);
  assert.match(chatSource, /if \(action === 'resume'\) await resumeDeveloperStream/);
  assert.match(chatSource, /epoch !== generation/);
  assert.match(chatSource, /cancelDeveloperSession\(id\)/);
  assert.match(pageSource, /at\.developer-space\.session:\$\{space\.id\}/);
});

test('metadata polling compares saved history revision, not parent-updated props', () => {
  assert.match(chatSource, /state\.session\.status\}` !== loadedRevision/);
  assert.doesNotMatch(chatSource, /state\.session\.updated_at !== session\.updated_at/);
});

test('unresolved durable ownership blocks new work and explains safe recovery', () => {
  assert.match(chatSource, /working = \$derived\(busy \|\| session.status === 'running' \|\| !!durableRun\)/);
  assert.match(chatSource, /durableRun = state.run \?\? null/);
  assert.match(chatSource, /durableRun\?\.interrupted/);
  assert.match(chatSource, /Stop requests cancellation but does not clear the lock/);
  assert.match(chatSource, /durableRun\?\.cancel_requested/);
  assert.doesNotMatch(chatSource, /Stop before starting new work/);
});

test('developer transcript shares compact tools and palettes with Chats', () => {
  assert.match(chatSource, /<ToolActivity compact/);
  assert.match(chatSource, /<CommandPalette/);
  assert.match(chatSource, /border-l-2 border-accent bg-dark-surface/);
  assert.doesNotMatch(chatSource, /<select/);
});

test('space suspension retains history and requires explicit control recovery', () => {
  assert.match(pageSource, /if \(starting \|\| space\?\.active_control_id\) return/);
  assert.match(pageSource, /space\?\.execution_suspended && !starting/);
  assert.match(pageSource, /Saved history remains available/);
  assert.match(pageSource, /Work is never restarted automatically/);
  for (const name of ['stop', 'reset']) {
    const action = pageSource.match(new RegExp(`async function ${name}\\(\\) \\{([\\s\\S]*?)\\n  \\}`))[1];
    assert.match(action, /space = await getDeveloperSpace\(\)/);
    assert.match(action, /if \(space.execution_suspended\)/);
  }
});

test('opening Developer Spaces does not start the container or access its files', () => {
  const boot = pageSource.match(/async function boot\(\) \{([\s\S]*?)\n  \}/)[1];
  assert.match(boot, /await getDeveloperSpace\(\)/);
  assert.match(boot, /await loadSessions\(\)/);
  assert.doesNotMatch(boot, /\bstart\(|startDeveloperSpace\(|loadRoot\(|openProject\(|newTerminal\(/);
  assert.match(pageSource, /onclick=\{start\}/);
});
const api = await load('../src/lib/api/developer-spaces.ts', [
  ["import axios from 'axios';", 'const axios = { create: () => ({}) };'],
  ["import { authFetch, workspaceTransport } from './transport';", 'const authFetch = null; const workspaceTransport = { selected: "" };'],
  ["import { resumableStream } from '../helper/resumable-stream';", 'const resumableStream = null;'],
]);

test('path helpers stay inside the given folder', () => {
  assert.equal(helper.parentPath('a/b/c.go'), 'a/b');
  assert.equal(helper.parentPath('top'), '');
  assert.equal(helper.joinPath('', 'x'), 'x');
  assert.equal(helper.joinPath('a', 'x'), 'a/x');
  assert.equal(helper.isWithin('a/b', 'a'), true);
  assert.equal(helper.isWithin('ab/c', 'a'), false, 'a sibling with a shared prefix is not inside');
  assert.equal(helper.renamedPath('a/b/c', 'a/b', 'a/z'), 'a/z/c');
  assert.equal(helper.renamedPath('a/bc', 'a/b', 'a/z'), 'a/bc');
});

test('entry names are single segments', () => {
  for (const bad of ['', '  ', '.', '..', 'a/b', 'a\\b']) assert.notEqual(helper.validEntryName(bad), null, JSON.stringify(bad));
  assert.equal(helper.validEntryName('main.go'), null);
});

test('transcript attaches tool results to their calls and merges consecutive assistant turns', () => {
  const entries = helper.buildTranscript([
    { id: '1', role: 'user', content: 'fix it', created_at: 't1' },
    { id: '2', role: 'assistant', content: [{ type: 'text', text: 'Looking' }, { type: 'tool_use', id: 'c1', name: 'read_file', input: { path: 'a.go' } }], created_at: 't2' },
    { id: '3', role: 'tool', content: [{ type: 'tool_result', tool_use_id: 'c1', content: 'package a' }], created_at: 't3' },
    { id: '4', role: 'system', content: 'internal nudge', created_at: 't4' },
    { id: '5', role: 'assistant', content: [{ type: 'tool_use', id: 'c2', name: 'run_command', input: { command: 'go', args: ['test'] } }], created_at: 't5' },
    { id: '6', role: 'tool', content: [{ type: 'tool_result', tool_use_id: 'c2', content: 'tool error: exit 1' }], created_at: 't6' },
    { id: '7', role: 'assistant', content: [{ type: 'text', text: 'Done' }], created_at: 't7' },
  ]);
  assert.equal(entries.length, 2);
  assert.equal(entries[0].text, 'fix it');
  assert.equal(entries[1].text, 'Looking\n\nDone');
  assert.deepEqual(entries[1].tools.map(t => [t.name, t.result, !!t.failed]), [['read_file', 'package a', false], ['run_command', 'tool error: exit 1', true]]);
  assert.ok(!entries.some(e => e.text.includes('internal nudge')));
});

test('tool summaries describe the call', () => {
  assert.equal(helper.toolSummary({ name: 'run_command', input: { command: 'go', args: ['test', './...'] } }), 'go test ./...');
  assert.equal(helper.toolSummary({ name: 'edit_file', input: { path: 'x.ts' } }), 'x.ts');
  assert.equal(helper.toolSummary({ name: 'search', input: { pattern: 'TODO', path: 'src' } }), 'TODO in src');
});

function stream(text, chunk = 3) {
  const bytes = new TextEncoder().encode(text);
  return new Response(new ReadableStream({ start(c) { for (let i = 0; i < bytes.length; i += chunk) c.enqueue(bytes.slice(i, i + chunk)); c.close(); } }));
}

test('event stream delivers events and completes on done', async () => {
  const events = [];
  await api.consumeDeveloperEvents(stream('data: {"type":"delta","content":"Merhaba 👋"}\r\n\r\ndata: {"type":"done","session":{"id":"s"}}\n\n'), e => events.push(e));
  assert.deepEqual(events.map(e => e.type), ['delta', 'done']);
  assert.equal(events[0].content, 'Merhaba 👋');
});

test('event stream reports errors and dropped connections', async () => {
  await assert.rejects(api.consumeDeveloperEvents(stream('data: {"type":"error","error":"quota"}\n\n'), () => {}), /quota/);
  const seen = [];
  await assert.rejects(api.consumeDeveloperEvents(stream('data: {"type":"delta","content":"partial"}\n\n'), e => seen.push(e)), /Connection ended/);
  assert.equal(seen[0].content, 'partial');
});

test('agent picker values round-trip between built-in profiles and agents', () => {
  assert.equal(helper.developerAgentValue({ mode: 'plan' }), 'builtin:plan');
  assert.equal(helper.developerAgentValue({ mode: 'build', agent_id: 'A1' }), 'agent:A1');
  assert.deepEqual(helper.developerAgentSettings('agent:A1'), { agent_id: 'A1' });
  assert.deepEqual(helper.developerAgentSettings('builtin:review'), { agent_id: '', mode: 'review' });
  assert.deepEqual(helper.developerAgentSettings('builtin:bogus'), { agent_id: '', mode: 'build' }, 'an unknown built-in falls back to Build');

  const choices = helper.developerAgentChoices([
    { id: 'z', name: 'Zed', config: {} },
    { id: 'a', name: 'Alpha', config: { group: 'Coding', description: 'Go expert' } },
  ]);
  assert.deepEqual(choices.map(c => c.value), ['builtin:build', 'builtin:plan', 'builtin:review', 'agent:z', 'agent:a']);
  assert.equal(choices.find(c => c.value === 'agent:a').group, 'Coding');
  assert.equal(choices.find(c => c.value === 'agent:a').hint, 'Go expert');
});
