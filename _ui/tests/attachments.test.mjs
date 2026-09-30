import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/helper/attachments.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const a = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

test('text files of any kind are text, not a capability', () => {
  for (const [name, mime] of [
    ['notes.txt', 'text/plain'], ['q.sql', 'application/sql'], ['q.sql', ''], ['data.json', 'application/json'],
    ['a.csv', 'text/csv'], ['main.go', ''], ['app.tsx', ''], ['README.md', 'text/markdown'], ['Dockerfile', ''],
  ]) {
    assert.equal(a.attachmentModality(name, mime), 'text', `${name} ${mime}`);
  }
});

test('binary media map to the five modalities', () => {
  assert.equal(a.attachmentModality('doc.pdf', 'application/pdf'), 'pdf');
  assert.equal(a.attachmentModality('doc.pdf', ''), 'pdf');
  assert.equal(a.attachmentModality('shot.png', 'image/png'), 'image');
  assert.equal(a.attachmentModality('talk.mp3', 'audio/mpeg'), 'audio');
  assert.equal(a.attachmentModality('clip.mp4', 'video/mp4'), 'video');
  // SVG is script-capable markup, not an image a model reads.
  assert.equal(a.attachmentModality('x.svg', 'image/svg+xml'), null);
  // No provider parses these natively.
  assert.equal(a.attachmentModality('r.docx', 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'), null);
  assert.equal(a.attachmentModality('a.zip', 'application/zip'), null);
});

test('each modality travels in the part shape the gateway translates', () => {
  const base = { name: 'x', size: 1 };
  assert.deepEqual(a.attachmentPart({ ...base, mime: 'image/png', modality: 'image', dataUrl: 'data:image/png;base64,AA' }), { type: 'image_url', image_url: { url: 'data:image/png;base64,AA' } });
  assert.deepEqual(a.attachmentPart({ ...base, name: 'd.pdf', mime: 'application/pdf', modality: 'pdf', dataUrl: 'data:application/pdf;base64,AA' }), { type: 'file', file: { filename: 'd.pdf', file_data: 'data:application/pdf;base64,AA' } });
  assert.deepEqual(a.attachmentPart({ ...base, mime: 'audio/mpeg', modality: 'audio', dataUrl: 'data:audio/mpeg;base64,AA' }), { type: 'input_audio', input_audio: { data: 'AA', format: 'mp3' } });
  assert.deepEqual(a.attachmentPart({ ...base, mime: 'video/mp4', modality: 'video', dataUrl: 'data:video/mp4;base64,AA' }), { type: 'video_url', video_url: { url: 'data:video/mp4;base64,AA' } });
  const text = a.attachmentPart({ ...base, name: 'q.sql', mime: 'application/sql', modality: 'text', dataUrl: '', text: 'select 1' });
  assert.equal(text.type, 'text');
  assert.match(text.text, /<file name="q\.sql">\nselect 1\n<\/file>/);
});

test('contentModalities reads outgoing parts', () => {
  const parts = [
    { type: 'text', text: 'hi' },
    { type: 'file', file: { filename: 'd.pdf', file_data: 'data:application/pdf;base64,AA' } },
    { type: 'input_audio', input_audio: { data: 'AA', format: 'wav' } },
  ];
  assert.deepEqual(a.contentModalities(parts), ['pdf', 'audio']);
  assert.deepEqual(a.contentModalities('plain'), []);
});

test('refusal follows the selected model, unknown admits everything', () => {
  const pdf = { name: 'd.pdf', modality: 'pdf' };
  assert.match(a.attachmentRefusal(pdf, ['text', 'image']), /does not accept pdf input/);
  assert.equal(a.attachmentRefusal(pdf, ['text', 'image', 'pdf']), '');
  assert.equal(a.attachmentRefusal(pdf, undefined), '');
  assert.equal(a.attachmentRefusal({ name: 'q.sql', modality: 'text' }, ['text']), '');
  assert.match(a.attachmentRefusal({ name: 'r.docx', modality: null }, undefined), /no provider reads this file type/);
});

test('picker accept narrows to the model but always allows text files', () => {
  const claude = a.acceptAttribute(['text', 'image', 'pdf']);
  assert.match(claude, /image\/\*/);
  assert.match(claude, /application\/pdf/);
  assert.doesNotMatch(claude, /audio\/\*/);
  assert.match(claude, /\.sql/);
  assert.match(a.acceptAttribute(undefined), /video\/\*/);
});
