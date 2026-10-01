import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { compile } from 'svelte/compiler';
import { render } from 'svelte/server';

function serverModule(source) {
  let code = compile(source, { generate: 'server' }).js.code;
  code = code.replace(/from (['"])(svelte\/[^'"]+|lucide-svelte)\1/g, (_, quote, name) => `from '${import.meta.resolve(name)}'`);
  return `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`;
}
const markdown = serverModule('<script lang="ts">let { source }: { source: string } = $props();</script><div data-markdown>{source}</div>');
const icon = serverModule('<script lang="ts">let { size, class: className }: { size?: number; class?: string } = $props();</script><svg class={className} width={size}></svg>');
let source = await readFile(new URL('../src/lib/components/playground/MessageContent.svelte', import.meta.url), 'utf8');
source = source.replace("import { FileText, ImageOff } from 'lucide-svelte';", `import Icon from '${icon}'; const FileText = Icon, ImageOff = Icon;`)
  .replace("import { mediaImageURL } from '@/lib/api/media';", 'const mediaImageURL = (id: string, workspace: string) => `/media/${id}?workspace=${workspace}`;')
  .replace("from '@/lib/components/Markdown.svelte'", `from '${markdown}'`);
const { default: MessageContent } = await import(serverModule(source));
const html = (content, props = {}) => render(MessageContent, { props: { message: { role: 'assistant', content }, workspace: 'selected', formatSize: bytes => `${bytes} bytes`, ...props } }).body;

test('user text remains literal; assistant text uses Markdown or explicit raw view', () => {
  assert.doesNotMatch(html('hello', { message: { role: 'user', content: '**hello**' } }), /data-markdown|<pre/);
  assert.match(html('**hello**'), /data-markdown/);
  assert.match(html('**hello**', { raw: true }), /<pre/);
  assert.match(html('', { thinking: true }), /Thinking/);
  assert.doesNotMatch(html('answer', { thinking: true }), /Thinking/);
});

test('saved media stays workspace-scoped with lazy images and original download names', () => {
  const output = html([
    { type: 'image', media_id: 'image-id', name: 'portrait' },
    { type: 'file', media_id: 'pdf-id', name: 'report.pdf', mime_type: 'application/pdf', bytes: 25 },
    { type: 'file', media_id: 'audio-id', mime_type: 'audio/mpeg' },
    { type: 'file', media_id: 'video-id', mime_type: 'video/mp4' },
  ]);
  for (const id of ['image-id', 'pdf-id', 'audio-id', 'video-id']) assert.ok(output.includes(`/media/${id}?workspace=selected`));
  for (const tag of ['<img', '<iframe', '<audio', '<video']) assert.ok(output.includes(tag));
  assert.match(output, /loading="lazy"/);
  assert.match(output, /download="report.pdf"/);
  assert.match(output, /25 bytes/);
});

test('unsaved user attachments and history omissions keep their established presentation', () => {
  const output = html('', { message: { role: 'user', content: [
    { type: 'image', omitted: true, name: 'missing.png' },
    { type: 'file', omitted: true, name: 'missing.pdf' },
    { type: 'input_audio', input_audio: { data: 'AAAA', format: 'mp3' } },
    { type: 'text', text: '<file name="notes.txt">contents</file>' },
  ] } });
  assert.match(output, /missing.png.*image not saved to history/);
  assert.match(output, /missing.pdf.*attachment not saved to history/);
  assert.match(output, /data:audio\/mpeg;base64,AAAA/);
  assert.match(output, /notes.txt/);
  assert.doesNotMatch(output, /contents/);
});
