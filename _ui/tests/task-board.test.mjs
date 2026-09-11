import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { beforeEach, test } from 'node:test';
import ts from 'typescript';

// The harness compiles a single `.ts` module to a data URL, so every module
// under test must be free of runtime relative imports. `import type` is elided
// by the compiler and is therefore allowed.

const RELATIVE_RUNTIME_IMPORT = /^(?:import|export)\s+(?!type\b)[^;]*from\s+'\.\.?\//m;

async function load(path, replace = source => source) {
  const source = await readFile(new URL(path, import.meta.url), 'utf8');
  assert.doesNotMatch(source, RELATIVE_RUNTIME_IMPORT, `${path} must stay free of runtime relative imports`);
  const code = ts.transpileModule(replace(source), {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
  }).outputText;
  return import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
}

let calls = [];
let response;
let failure;
globalThis.taskBoardAxiosMock = {
  create(config) {
    assert.deepEqual(config, { baseURL: 'api/v1' });
    return Object.fromEntries(['get', 'post', 'put', 'delete'].map(method => [method, async (...args) => {
      calls.push([method, ...args]);
      if (failure) throw failure;
      return { data: response };
    }]));
  },
};

const apiSource = await readFile(new URL('../src/lib/api/task-board.ts', import.meta.url), 'utf8');
assert.ok(apiSource.includes("import axios from 'axios';"));

const api = await load('../src/lib/api/task-board.ts', source =>
  source.replace("import axios from 'axios';", 'const axios = globalThis.taskBoardAxiosMock;'));
const helper = await load('../src/lib/helper/task-board.ts');
// `tasks.ts` owns the vocabulary the rest of the UI imports; the helper keeps a
// copy only because this harness forbids it a runtime import. Loading both
// turns that duplication into a checked invariant instead of a latent drift.
const tasks = await load('../src/lib/api/tasks.ts', source =>
  source.replace("import axios from 'axios';", 'const axios = globalThis.taskBoardAxiosMock;'));
delete globalThis.taskBoardAxiosMock;

beforeEach(() => { calls = []; response = undefined; failure = undefined; });

const column = (overrides = {}) => ({ id: 'todo', label: 'To Do', color: 'blue', statuses: ['backlog', 'todo'], ...overrides });
const board = (overrides = {}) => ({
  workspace_id: 'w1', version: 3, columns: helper.defaultTaskBoardColumns(),
  updated_at: '2026-09-10T00:00:00Z', updated_by: 'a@example.com', default: false,
  available_statuses: [...helper.TASK_BOARD_STATUSES], ...overrides,
});
const error = (status, message) => ({ response: { status, ...(message === undefined ? {} : { data: { message } }) } });

// ─── API layer ───

test('the three endpoints use the documented method and path', async () => {
  response = board();
  assert.deepEqual(await api.getTaskBoard(), response);
  assert.deepEqual(await api.resetTaskBoard(), response);
  await api.saveTaskBoard(3, helper.defaultTaskBoardColumns());
  assert.deepEqual(calls.map(([method, path]) => [method, path]), [
    ['get', '/task-board'],
    ['delete', '/task-board'],
    ['put', '/task-board'],
  ]);
});

test('PUT sends exactly {version, columns} and never invents a field', async () => {
  await api.saveTaskBoard(7, [
    column(),
    { id: 'done', label: 'Done', color: 'green', statuses: ['done'], uncovered_statuses: ['x'], extra: 1 },
  ]);
  const [, , sent] = calls[0];
  assert.deepEqual(Object.keys(sent).sort(), ['columns', 'version']);
  assert.equal(sent.version, 7);
  for (const body of sent.columns) {
    for (const key of Object.keys(body)) assert.ok(['id', 'label', 'color', 'statuses'].includes(key), key);
  }
  assert.deepEqual(sent.columns, [
    { label: 'To Do', statuses: ['backlog', 'todo'], id: 'todo', color: 'blue' },
    { label: 'Done', statuses: ['done'], id: 'done', color: 'green' },
  ]);
});

test('a blank id or colour is omitted, not sent empty', () => {
  // An omitted `id` is the wire signal for "derive it from the label", so a
  // brand new column must not carry `id: ""`.
  const body = api.taskBoardBody(0, [{ id: '  ', label: '  Review  ', color: '', statuses: [' in_review ', '', 'done'] }]);
  assert.deepEqual(body, { version: 0, columns: [{ label: 'Review', statuses: ['in_review', 'done'] }] });
  assert.equal('id' in body.columns[0], false);
  assert.equal('color' in body.columns[0], false);
  // Missing or wrongly typed values normalise instead of serialising as null.
  assert.deepEqual(api.taskBoardBody(undefined, undefined), { version: 0, columns: [] });
  assert.deepEqual(api.taskBoardBody(Number.NaN, [{}]), { version: 0, columns: [{ label: '', statuses: [] }] });
});

test('a 409 is named as a concurrent edit and offers a reload', () => {
  const upstream = 'the board was changed by someone else; reload and reapply your edit';
  assert.equal(api.isTaskBoardConflict(error(409)), true);
  for (const status of [400, 403, 404, 500]) assert.equal(api.isTaskBoardConflict(error(status)), false);
  // The server already says it well; repeat it verbatim rather than paraphrase.
  assert.equal(api.taskBoardErrorMessage(error(409, upstream)), upstream);
  // With no body, the fallback still says what happened and what to do.
  assert.match(api.taskBoardErrorMessage(error(409)), /changed by someone else/i);
  assert.match(api.taskBoardErrorMessage(error(409)), /reload/i);
});

test('403 is about permission, 400 names the column, everything else falls back', () => {
  assert.equal(api.isTaskBoardForbidden(error(403)), true);
  assert.equal(api.isTaskBoardForbidden(error(401)), true);
  for (const status of [400, 409, 500]) assert.equal(api.isTaskBoardForbidden(error(status)), false);
  // A 403 body is generic; the capability the reader is missing is not.
  assert.match(api.taskBoardErrorMessage(error(403, 'forbidden')), /tasks\.write/);
  // A 400 names the offending column, so it wins over any phrasing of ours.
  const invalid = 'invalid task board: status "done" is in both "Done" and "Archive"; a task can only be in one column';
  assert.equal(api.taskBoardErrorMessage(error(400, invalid)), invalid);
  assert.match(api.taskBoardErrorMessage(error(400)), /not valid/i);
  assert.match(api.taskBoardErrorMessage(error(503)), /unavailable/i);
  assert.equal(api.taskBoardErrorMessage(error(500)), 'Could not save the board columns.');
  assert.equal(api.taskBoardErrorMessage(new Error('network'), 'custom'), 'custom');
  assert.equal(api.taskBoardErrorStatus(error(418)), 418);
  for (const value of [new Error('x'), undefined, null, {}, { response: {} }]) {
    assert.equal(api.taskBoardErrorStatus(value), 0);
  }
});

test('every endpoint propagates failures once, without retrying', async () => {
  failure = error(503, 'task board storage unavailable');
  const operations = [
    () => api.getTaskBoard(),
    () => api.saveTaskBoard(0, []),
    () => api.resetTaskBoard(),
  ];
  for (const operation of operations) {
    const before = calls.length;
    await assert.rejects(operation, err => err === failure);
    assert.equal(calls.length, before + 1);
  }
});

// ─── Vocabulary ───

test('the helper copy of the status vocabulary matches the one the UI imports', () => {
  assert.deepEqual(helper.TASK_BOARD_STATUSES, [...tasks.TASK_STATUSES]);
  assert.deepEqual(helper.TASK_BOARD_STATUS_LABELS, tasks.TASK_STATUS_LABELS);
  // The shipped board covers every status, so a fresh workspace hides nothing.
  assert.deepEqual(helper.uncoveredStatuses(helper.defaultTaskBoardColumns()), []);
  assert.equal(helper.defaultTaskBoard().default, true);
  assert.equal(helper.defaultTaskBoard().version, 0);
});

// ─── status → column lookup ───

test('the lookup maps a status to the one column that collects it', () => {
  const columns = helper.defaultTaskBoardColumns();
  assert.deepEqual(helper.statusColumnLookup(columns), {
    backlog: 'todo', todo: 'todo', in_progress: 'in_progress', in_review: 'in_progress',
    blocked: 'blocked', done: 'done', cancelled: 'done',
  });
  assert.equal(helper.columnIdForStatus(columns, 'in_review'), 'in_progress');
  // A status no column collects resolves to '' rather than to a default
  // column: putting the card somewhere arbitrary would misreport the work.
  assert.equal(helper.columnIdForStatus(columns, 'archived'), '');
  assert.equal(helper.columnIdForStatus(columns, ''), '');
  assert.deepEqual(helper.statusColumnLookup(undefined), {});
  // A duplicate that slipped past validation resolves to the first claim.
  assert.equal(helper.columnIdForStatus([column(), column({ id: 'b', label: 'B', statuses: ['todo'] })], 'todo'), 'todo');
});

test('a drop applies the first status of the column', () => {
  assert.equal(helper.dropStatus(column({ statuses: ['in_review', 'in_progress'] })), 'in_review');
  assert.equal(helper.dropStatus(column({ statuses: [] })), '');
  assert.equal(helper.dropStatus(undefined), '');
  assert.equal(helper.dropStatus(null), '');
});

test('statusOwnerIndex finds the column holding a status, ignoring one', () => {
  const columns = helper.defaultTaskBoardColumns();
  assert.equal(helper.statusOwnerIndex(columns, 'blocked'), 2);
  assert.equal(helper.statusOwnerIndex(columns, 'blocked', 2), -1);
  assert.equal(helper.statusOwnerIndex(columns, 'nope'), -1);
});

// ─── duplicate detection ───

test('a status claimed by two columns is reported with both owners', () => {
  const columns = [
    column({ id: 'a', label: 'Active', statuses: ['todo', 'in_progress'] }),
    column({ id: 'b', label: 'Review', statuses: ['in_progress', 'in_review'] }),
    column({ id: 'c', label: 'Closed', statuses: ['done', 'in_progress'] }),
  ];
  assert.deepEqual(helper.duplicateStatuses(columns), [
    { status: 'in_progress', columns: ['Active', 'Review', 'Closed'] },
  ]);
  // A repeat inside one column is dropped by the server, not a clash.
  assert.deepEqual(helper.duplicateStatuses([column({ statuses: ['todo', 'todo'] })]), []);
  assert.deepEqual(helper.duplicateStatuses(helper.defaultTaskBoardColumns()), []);
  assert.deepEqual(helper.duplicateStatuses(undefined), []);
});

// ─── uncovered statuses and hidden work ───

test('uncoveredStatuses lists what the layout leaves out, in vocabulary order', () => {
  const columns = [column({ statuses: ['todo'] }), column({ id: 'd', label: 'Done', statuses: ['done'] })];
  assert.deepEqual(helper.uncoveredStatuses(columns), ['backlog', 'in_progress', 'in_review', 'blocked', 'cancelled']);
  // The server's own vocabulary wins when it is supplied.
  assert.deepEqual(helper.uncoveredStatuses(columns, ['todo', 'done', 'archived']), ['archived']);
  assert.deepEqual(helper.uncoveredStatuses(columns, []), []);
});

test('hidden work is counted from the tasks loaded, not from the vocabulary', () => {
  const columns = [column({ statuses: ['todo', 'backlog'] })];
  const tasksIn = [
    { status: 'todo' }, { status: 'done' }, { status: 'done' },
    { status: 'blocked' }, { status: 'legacy_open' }, { status: 'backlog' },
  ];
  // Three statuses are uncovered but only the ones with tasks are named.
  assert.deepEqual(helper.hiddenTaskSummary(tasksIn, columns), {
    count: 4, statuses: ['blocked', 'done', 'legacy_open'],
  });
  // A status nobody is in is not worth a warning.
  assert.deepEqual(helper.hiddenTaskSummary([{ status: 'todo' }], columns), { count: 0, statuses: [] });
  assert.deepEqual(helper.hiddenTaskSummary(undefined, columns), { count: 0, statuses: [] });
  // No columns at all means nothing is drawn, and it says so.
  assert.equal(helper.hiddenTaskSummary(tasksIn, []).count, 6);
});

// ─── validation, mirrored from the server ───

test('validation refuses exactly what the server refuses', () => {
  assert.equal(helper.validateTaskBoardColumns(helper.defaultTaskBoardColumns()), '');
  assert.match(helper.validateTaskBoardColumns([]), /at least one column/);
  assert.match(helper.validateTaskBoardColumns(undefined), /at least one column/);

  const many = Array.from({ length: 13 }, (_, i) => column({ id: `c${i}`, label: `C${i}`, statuses: [] }));
  assert.match(helper.validateTaskBoardColumns(many), /maximum of 12/);

  assert.match(helper.validateTaskBoardColumns([column({ label: '   ' })]), /Column 1: This column needs a name/);
  assert.match(helper.validateTaskBoardColumns([column({ label: 'x'.repeat(41) })]), /41 characters.*maximum is 40/);
  assert.equal(helper.validateTaskBoardColumns([column({ label: 'x'.repeat(40) })]), '');
  assert.match(helper.validateTaskBoardColumns([column({ statuses: [] })]), /at least one status/);
  // Two new columns whose labels slug to the same id are a duplicate-id 400.
  assert.match(helper.validateTaskBoardColumns([
    { id: '', label: 'To Do', statuses: ['todo'] },
    { id: '', label: 'to do!', statuses: ['done'] },
  ]), /Column 1: Column 2 resolves to the same name/);

  // The rule that matters most: one status, one column.
  const clash = [
    column({ id: 'a', label: 'Active', statuses: ['todo'] }),
    column({ id: 'b', label: 'Later', statuses: ['todo'] }),
  ];
  assert.match(helper.validateTaskBoardColumns(clash), /"To Do" is also in "Later"/);
  assert.match(helper.columnProblem(clash, 1), /"To Do" is also in "Active"/);
  assert.equal(helper.columnProblem(helper.defaultTaskBoardColumns(), 0), '');
  assert.equal(helper.columnProblem([], 0), '');
});

test('column ids are derived from labels the same way the server derives them', () => {
  assert.equal(helper.slugifyColumnLabel('In Review', 0), 'in-review');
  assert.equal(helper.slugifyColumnLabel('  Waiting / Blocked!  ', 0), 'waiting-blocked');
  assert.equal(helper.slugifyColumnLabel('Sprint 2', 0), 'sprint-2');
  assert.equal(helper.slugifyColumnLabel('???', 4), 'column-5');
  assert.equal(helper.slugifyColumnLabel('', 0), 'column-1');
});

// ─── ordering and presentation ───

test('moveItem reorders without mutating, and refuses an out of range move', () => {
  const list = ['a', 'b', 'c'];
  assert.deepEqual(helper.moveItem(list, 0, 2), ['b', 'c', 'a']);
  assert.deepEqual(helper.moveItem(list, 2, 0), ['c', 'a', 'b']);
  assert.deepEqual(list, ['a', 'b', 'c']);
  for (const [from, to] of [[0, 0], [-1, 1], [0, 3], [3, 0]]) {
    assert.deepEqual(helper.moveItem(list, from, to), list);
  }
  assert.deepEqual(helper.moveItem(undefined, 0, 1), []);
});

test('colours fall back to gray and every palette token renders', () => {
  for (const color of helper.TASK_BOARD_COLORS) {
    assert.ok(helper.columnDotClass(color).includes(color === 'green' ? 'green' : color));
    assert.ok(helper.TASK_BOARD_COLOR_LABELS[color]);
  }
  assert.equal(helper.columnDotClass('chartreuse'), helper.columnDotClass('gray'));
  assert.equal(helper.columnDotClass(undefined), helper.columnDotClass('gray'));
  assert.equal(helper.columnDotClass(''), helper.columnDotClass('gray'));
});

test('a retired status still reads as words rather than as a key', () => {
  assert.equal(helper.taskStatusText('in_progress'), 'In Progress');
  assert.equal(helper.taskStatusText('legacy_open'), 'legacy open');
  assert.equal(helper.taskStatusText(''), 'unknown');
  assert.equal(helper.taskStatusText(undefined), 'unknown');
});

test('cloneColumns detaches the draft so cancel is a genuine no-op', () => {
  const original = helper.defaultTaskBoardColumns();
  const copy = helper.cloneColumns(original);
  copy[0].label = 'Changed';
  copy[0].statuses.push('done');
  assert.equal(original[0].label, 'To Do');
  assert.deepEqual(original[0].statuses, ['backlog', 'todo']);
  assert.deepEqual(helper.cloneColumns(undefined), []);
  assert.deepEqual(helper.cloneColumns([{}]), [{ id: '', label: '', color: '', statuses: [] }]);
});
