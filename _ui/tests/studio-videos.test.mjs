import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { beforeEach, test } from 'node:test';
import ts from 'typescript';

const files = new Map();
const readErrors = new Map();
let entries;
let browseError;
let writes;
globalThis.videoFilesMock = {
  browseFiles: async (path) => {
    assert.equal(path, '/assets/videos');
    if (browseError) throw browseError;
    return { entries };
  },
  fetchFileText: async (path) => {
    if (readErrors.has(path)) throw readErrors.get(path);
    if (!files.has(path)) throw { response: { status: 404 } };
    return files.get(path);
  },
  uploadFile: async (file, dir, name) => {
    const text = await file.text();
    writes.push({ dir, name, text });
    files.set(`${dir}/${name}`, text);
  },
};
const source = await readFile(new URL('../src/lib/api/studio-videos.ts', import.meta.url), 'utf8');
const dependency = "import { browseFiles, fetchFileText, uploadFile } from './files';";
assert.ok(source.includes(dependency), 'File API import must be replaced by the in-memory mock');
const code = ts.transpileModule(source.replace(dependency,
  'const { browseFiles, fetchFileText, uploadFile } = globalThis.videoFilesMock;'), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const api = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
delete globalThis.videoFilesMock;
const brief = { ...api.createVideoBrief(), id: 'video-1', title: 'Healthy' };
const dir = '/assets/videos/video-1';

beforeEach(() => {
  files.clear();
  readErrors.clear();
  entries = [{ name: brief.id, is_dir: true }];
  browseError = undefined;
  writes = [];
});

test('brief updates validate types, limits, duration, and aspect ratio', () => {
  for (const patch of [null, [], 'text', { unknown: 'x' }, { title: 3 }, { title: null },
    { duration_minutes: '5' }, { duration_minutes: NaN }, { duration_minutes: Infinity },
    { duration_minutes: 0 }, { duration_minutes: 61 }, { aspect_ratio: '4:3' },
    JSON.parse('{"__proto__": {"polluted": true}}')]) {
    assert.throws(() => api.applyVideoBriefUpdate(brief, patch));
  }
  for (const [field, limit] of Object.entries(api.VIDEO_BRIEF_LIMITS)) {
    if (field === 'aspect_ratio') continue;
    assert.equal(api.applyVideoBriefUpdate(brief, { [field]: 'x'.repeat(limit) })[field].length, limit);
    assert.throws(() => api.applyVideoBriefUpdate(brief, { [field]: 'x'.repeat(limit + 1) }));
  }
  for (const duration_minutes of [1, 1.5, 60]) {
    assert.equal(api.applyVideoBriefUpdate(brief, { duration_minutes }).duration_minutes, duration_minutes);
  }
  for (const aspect_ratio of ['16:9', '9:16', '1:1']) {
    assert.equal(api.applyVideoBriefUpdate(brief, { aspect_ratio }).aspect_ratio, aspect_ratio);
  }
});

test('patches are atomic, return new objects, and cannot edit IDs', () => {
  const current = structuredClone(brief);
  const updated = api.applyVideoBriefUpdate(current, { title: 'New title' });
  assert.equal(updated.title, 'New title');
  assert.equal(updated.id, current.id);
  assert.notEqual(updated, current);
  assert.deepEqual(api.applyVideoBriefUpdate(current, {}), current);
  for (const id of ['changed', current.id]) {
    assert.throws(() => api.applyVideoBriefUpdate(current, { title: 'Must not stick', id }), /immutable/);
  }
  assert.throws(() => api.applyVideoBriefUpdate(current, { title: 'Must not stick', duration_minutes: -1 }));
  assert.deepEqual(current, brief);
});

test('complete briefs and project paths are validated', () => {
  assert.deepEqual(api.validateVideoBrief(brief), brief);
  for (const id of ['', '../bad', '/absolute', 'x'.repeat(129)]) {
    assert.throws(() => api.validateVideoBrief({ ...brief, id }), /ID/);
    assert.throws(() => api.videoProjectDir('/assets', id), /ID/);
  }
  for (const key of [...Object.keys(api.VIDEO_BRIEF_LIMITS), 'duration_minutes']) {
    const incomplete = { ...brief };
    delete incomplete[key];
    assert.throws(() => api.validateVideoBrief(incomplete), /Missing brief field/);
  }
  assert.throws(() => api.videoProjectDir('relative', brief.id), /absolute/);
  assert.equal(api.videoProjectDir('/assets///', brief.id), dir);
});

test('in-progress output preserves null final_video and duration_s', async () => {
  files.set(`${dir}/brief.json`, JSON.stringify(brief));
  const output = { status: 'generating', final_video: null, duration_s: null };
  files.set(`${dir}/video.json`, JSON.stringify(output));
  const warnings = [];
  assert.deepEqual(await api.loadVideoProjects('/assets', (message) => warnings.push(message)),
    [{ brief, dir, task_id: undefined, output }]);
  assert.deepEqual(warnings, []);
});

test('corrupted projects are reported individually without hiding healthy projects', async () => {
  files.set(`${dir}/brief.json`, JSON.stringify(brief));
  const broken = [
    ['../unsafe', undefined, undefined, /Invalid video brief ID/],
    ['bad-json', 'brief.json', '{broken', /Could not read/],
    ['bad-brief', 'brief.json', '{}', /Invalid video brief ID/],
    ['mismatch', 'brief.json', JSON.stringify(brief), /does not match/],
    ['bad-submission-json', 'submission.json', '{broken', /Could not read/],
    ['bad-submission', 'submission.json', '{}', /Invalid submission/],
    ['bad-output-json', 'video.json', '{broken', /Could not read/],
    ['bad-output', 'video.json', '[]', /Invalid video.json/],
    ['bad-duration', 'video.json', '{"duration_s":-3}', /Invalid output duration_s/],
    ['bad-final', 'video.json', '{"final_video":42}', /Invalid output final_video/],
    ['denied', 'brief.json', undefined, /Forbidden/],
  ];
  entries = broken.map(([name]) => ({ name, is_dir: true }));
  for (const [id, name, text] of broken) {
    const path = `/assets/videos/${id}`;
    files.set(`${path}/brief.json`, JSON.stringify({ ...brief, id }));
    if (name && text !== undefined) files.set(`${path}/${name}`, text);
  }
  readErrors.set('/assets/videos/denied/brief.json', { response: { status: 403, data: 'Forbidden' } });
  entries.push({ name: 'missing', is_dir: true }, { name: 'ignore.txt', is_dir: false },
    { name: brief.id, is_dir: true }, { name: 'another', is_dir: true });
  files.set('/assets/videos/another/brief.json', JSON.stringify({ ...brief, id: 'another', title: 'A first' }));
  const warnings = [];
  const projects = await api.loadVideoProjects('/assets', (message) => warnings.push(message));
  assert.deepEqual(projects.map((project) => project.brief.id), ['another', brief.id]);
  assert.equal(warnings.length, broken.length);
  broken.forEach(([id, , , reason], index) => {
    assert.ok(warnings[index].startsWith(`Could not load /assets/videos/${id}: `));
    assert.match(warnings[index], reason);
  });
  assert.deepEqual(await api.loadVideoProjects('/assets'), projects);
  assert.deepEqual(writes, []);
});

test('missing roots are empty, other root failures propagate without project warnings', async () => {
  const warnings = [];
  const onError = (message) => warnings.push(message);
  assert.deepEqual(await api.loadVideoProjects('', onError), []);
  assert.deepEqual(await api.loadVideoProjects('/assets', onError), []);
  browseError = { response: { status: 404 } };
  assert.deepEqual(await api.loadVideoProjects('/assets', onError), []);
  for (const error of [new Error('Network failure'), ...[401, 403, 500].map((status) => ({ response: { status } }))]) {
    browseError = error;
    await assert.rejects(api.loadVideoProjects('/assets', onError), (caught) => caught === error);
  }
  assert.deepEqual(warnings, []);
});

test('draft and submission writes stay separate from agent-owned output', async () => {
  assert.deepEqual(await api.saveVideoBrief('/assets', brief), { brief, dir });
  assert.deepEqual(writes.map(({ name }) => name), ['brief.json']);
  assert.deepEqual(JSON.parse(files.get(`${dir}/brief.json`)), brief);
  const output = JSON.stringify({ status: 'assembled', final_video: 'final.mp4', duration_s: 300 });
  files.set(`${dir}/video.json`, output);
  await api.saveVideoSubmission(dir, 'task-123');
  assert.deepEqual(writes.map(({ name }) => name), ['brief.json', 'submission.json']);
  assert.deepEqual(JSON.parse(files.get(`${dir}/submission.json`)), { task_id: 'task-123' });
  assert.equal(files.get(`${dir}/video.json`), output);
  const [project] = await api.loadVideoProjects('/assets');
  assert.equal(project.task_id, 'task-123');
  assert.deepEqual(project.output, JSON.parse(output));
  await assert.rejects(api.saveVideoBrief('/assets', brief), /read-only/);
  files.delete(`${dir}/submission.json`);
  await assert.rejects(api.saveVideoBrief('/assets', brief), /read-only/);
  files.delete(`${dir}/video.json`);
  files.set(`${dir}/submission.json`, '{"task_id":"task-123"}');
  await assert.rejects(api.saveVideoBrief('/assets', brief), /read-only/);
  await assert.rejects(api.saveVideoSubmission(dir, '  '), /task ID/);
  await assert.rejects(api.saveVideoBrief('/assets', { ...brief, duration_minutes: -1 }));
  assert.equal(writes.length, 2);
});
