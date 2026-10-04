import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/workflow/chat-tool-summary.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const { summarizeWorkflowToolCall } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

test('add_node names the node, position and the fields it set', () => {
  const summary = summarizeWorkflowToolCall('add_node', JSON.stringify({
    type: 'agent_call', id: 'writer', position: { x: 400.4, y: 0 }, data: { label: 'Writer', agent_id: 'a1' },
  }));
  assert.equal(summary, 'Added agent_call "Writer" as writer at (400, 0) · set label, agent_id');
});

test('update, connect and move describe what changed', () => {
  assert.equal(summarizeWorkflowToolCall('update_node_data', '{"id":"n1","data":{"model":"x","provider":"y"}}'), 'Updated n1 · set model, provider');
  assert.equal(summarizeWorkflowToolCall('add_edge', '{"source":"a","source_handle":"output","target":"b","target_handle":"input"}'), 'Connected a.output → b.input');
  assert.equal(summarizeWorkflowToolCall('update_node_position', '{"id":"n1","position":{"x":-10,"y":5}}'), 'Moved n1 to (-10, 5)');
});

test('malformed arguments and unknown tools never throw', () => {
  assert.equal(summarizeWorkflowToolCall('remove_node', '{not json'), 'Removed node ? and its edges');
  assert.equal(summarizeWorkflowToolCall('something_else', '{}'), '');
});
