import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/workflow/connection-snap.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const { findSnapTarget, describeConnectionRejection } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

// Minimal stand-in for Kaykay's FlowState: handles keyed "<node>:<handle>".
function fakeFlow({ zoom = 1, rejected = {} } = {}) {
  const handles = {
    'a:out': { id: 'out', type: 'output', absolute_position: { x: 0, y: 0 } },
    'b:prompt': { id: 'prompt', type: 'input', absolute_position: { x: 200, y: 100 } },
    'b:context': { id: 'context', type: 'input', absolute_position: { x: 200, y: 124 } },
    'c:data': { id: 'data', type: 'input', absolute_position: { x: 600, y: 100 } },
    'a:in': { id: 'in', type: 'input', absolute_position: { x: 0, y: 30 } },
  };
  return {
    viewport: { x: 0, y: 0, zoom },
    draft_connection: { source_node_id: 'a', source_handle_id: 'out' },
    handle_registry: handles,
    handle_positions: {},
    getConnectionValidation: (_s, _sh, node, handle) => {
      const reason = rejected[`${node}:${handle}`];
      return reason ? { valid: false, reason } : { valid: true };
    },
  };
}

test('snaps to the nearest valid input within the radius', () => {
  const { candidate } = findSnapTarget(fakeFlow(), { x: 210, y: 104 }, 28);
  assert.deepEqual([candidate.nodeId, candidate.handleId], ['b', 'prompt']);
  const lower = findSnapTarget(fakeFlow(), { x: 208, y: 120 }, 28).candidate;
  assert.equal(lower.handleId, 'context');
});

test('never snaps to the source node or beyond the radius', () => {
  assert.equal(findSnapTarget(fakeFlow(), { x: 2, y: 30 }, 28).candidate, null);
  assert.equal(findSnapTarget(fakeFlow(), { x: 400, y: 100 }, 28).candidate, null);
});

test('radius is measured in screen pixels', () => {
  // 20 canvas px away at zoom 2 is 40 screen px: outside a 28px radius.
  assert.equal(findSnapTarget(fakeFlow({ zoom: 2 }), { x: 220, y: 100 }, 28).candidate, null);
  assert.ok(findSnapTarget(fakeFlow({ zoom: 0.5 }), { x: 240, y: 100 }, 28).candidate);
});

test('reports the reason when only rejected inputs are near', () => {
  const flow = fakeFlow({ rejected: { 'b:prompt': 'Connection would create a cycle', 'b:context': 'Connection would create a cycle' } });
  const result = findSnapTarget(flow, { x: 205, y: 100 }, 28);
  assert.equal(result.candidate, null);
  assert.match(describeConnectionRejection(result.reason), /loop/);
});

test('skips a rejected handle in favour of a valid neighbour', () => {
  const flow = fakeFlow({ rejected: { 'b:prompt': 'Connection already exists' } });
  assert.equal(findSnapTarget(flow, { x: 200, y: 106 }, 28).candidate.handleId, 'context');
});
