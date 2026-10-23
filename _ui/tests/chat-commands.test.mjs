import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/helper/chat-commands.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const { parseSlashInput, slashQuery, splitArgs, expandCommandTemplate, compactionStart, compactionMessageText, compactionRequest, isContextOverflowMessage, clipForCompaction, mcpPromptCommandName, mcpPromptArguments } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

test('parses a command and its arguments; paths and prose are not commands', () => {
  assert.deepEqual(parseSlashInput('/Compact keep the API decisions'), { name: 'compact', args: 'keep the API decisions' });
  assert.deepEqual(parseSlashInput('  /review\nline two'), { name: 'review', args: 'line two' });
  assert.deepEqual(parseSlashInput('/new'), { name: 'new', args: '' });
  assert.equal(parseSlashInput('/usr/bin/env'), null);
  assert.equal(parseSlashInput('please /compact'), null);
  assert.equal(parseSlashInput('/'), null);
});

test('suggestion query exists only while the name is being typed', () => {
  assert.equal(slashQuery('/'), '');
  assert.equal(slashQuery('/Re'), 're');
  assert.equal(slashQuery('/review '), null);
  assert.equal(slashQuery('text'), null);
});

test('templates expand $ARGUMENTS, positional and quoted arguments', () => {
  assert.deepEqual(splitArgs(`a "b c" 'd e' f`), ['a', 'b c', 'd e', 'f']);
  assert.equal(expandCommandTemplate('Review $1 against $2.', 'main "feature x"'), 'Review main against feature x.');
  assert.equal(expandCommandTemplate('All: $ARGUMENTS', 'x y'), 'All: x y');
  assert.equal(expandCommandTemplate('Missing $3.', 'a'), 'Missing .');
  // No placeholder: arguments are appended rather than silently dropped.
  assert.equal(expandCommandTemplate('Summarise the release.', 'only v2'), 'Summarise the release.\n\nonly v2');
  assert.equal(expandCommandTemplate('Summarise the release.', ''), 'Summarise the release.');
});

test('the model context starts at the latest compaction summary', () => {
  assert.equal(compactionStart([]), -1);
  assert.equal(compactionStart([false, true, false, true, false]), 3);
  assert.match(compactionMessageText(' notes ', 'auth'), /focus on: auth\]\n\nnotes$/);
  assert.match(compactionRequest('auth'), /Focus the summary on: auth$/);
});

test('context overflow messages are recognised across providers', () => {
  for (const msg of ["This model's maximum context length is 128000 tokens", 'prompt is too long: 210000 tokens', 'context_length_exceeded']) {
    assert.equal(isContextOverflowMessage(msg), true, msg);
  }
  for (const msg of ['rate limit exceeded', 'request entity too large', '']) assert.equal(isContextOverflowMessage(msg), false, msg);
  const clipped = clipForCompaction('a'.repeat(100) + 'b'.repeat(100), 60);
  assert.ok(clipped.startsWith('a'.repeat(40)) && clipped.endsWith('b'.repeat(20)) && clipped.includes('140 characters omitted'));
});

test('MCP prompts are /server:prompt commands with mapped arguments', () => {
  assert.deepEqual(parseSlashInput('/github:review-pr 42 please be strict'), { name: 'github:review-pr', args: '42 please be strict' });
  assert.equal(slashQuery('/github:re'), 'github:re');
  assert.equal(parseSlashInput('/a:b:c'), null);
  assert.equal(mcpPromptCommandName({ server: 'My Server', name: 'Review PR' }), 'my-server:review-pr');
  const spec = [{ name: 'pr', required: true }, { name: 'focus' }];
  assert.deepEqual(mcpPromptArguments(spec, '42 security and tests'), { values: { pr: '42', focus: 'security and tests' }, missing: [] });
  assert.deepEqual(mcpPromptArguments(spec, ''), { values: {}, missing: ['pr'] });
  assert.deepEqual(mcpPromptArguments(undefined, 'ignored'), { values: {}, missing: [] });
});
