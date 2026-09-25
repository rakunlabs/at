import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/helper/skill-files.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const { codeLanguage, isMarkdownPath, resolveSkillLink, splitFrontmatter } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

test('flat SKILL.md frontmatter becomes fields and is removed from the body', () => {
  const { frontmatter, body } = splitFrontmatter('---\nname: pdf-tools\ndescription: "Extract text: fast"\n---\n# PDF tools\n');
  assert.deepEqual(frontmatter.fields, [['name', 'pdf-tools'], ['description', 'Extract text: fast']]);
  assert.equal(body, '# PDF tools\n');
});

test('nested or block-scalar frontmatter is kept raw instead of flattened', () => {
  for (const yaml of ['metadata:\n  version: 2', 'description: >\n  folded text', 'tags:\n- a\n- b']) {
    const { frontmatter } = splitFrontmatter(`---\n${yaml}\n---\nbody`);
    assert.equal(frontmatter.fields, null, yaml);
    assert.equal(frontmatter.raw, yaml);
  }
});

test('documents without a leading fence are left untouched', () => {
  const text = '# Title\n\n---\nname: not frontmatter\n---\n';
  assert.deepEqual(splitFrontmatter(text), { frontmatter: null, body: text });
  assert.equal(splitFrontmatter('---\nunterminated: yes\n').frontmatter, null);
});

test('relative links resolve inside the skill folder only', () => {
  assert.equal(resolveSkillLink('references/api.md', 'SKILL.md'), 'references/api.md');
  assert.equal(resolveSkillLink('./forms.md#fields', 'docs/guide.md'), 'docs/forms.md');
  assert.equal(resolveSkillLink('../scripts/run%20me.sh', 'docs/guide.md'), 'scripts/run me.sh');
  assert.equal(resolveSkillLink('/SKILL.md', 'docs/deep/page.md'), 'SKILL.md');
  assert.equal(resolveSkillLink('../../etc/passwd', 'docs/guide.md'), null);
  for (const external of ['https://example.com/a.md', 'mailto:a@b.c', 'javascript:alert(1)', '//evil.test/x', '#section']) {
    assert.equal(resolveSkillLink(external, 'SKILL.md'), null, external);
  }
});

test('file kinds map to preview modes and highlight languages', () => {
  assert.equal(isMarkdownPath('references/API.MD'), true);
  assert.equal(isMarkdownPath('scripts/run.py'), false);
  assert.equal(codeLanguage('scripts/run.py'), 'python');
  assert.equal(codeLanguage('config/settings.yml'), 'yaml');
  assert.equal(codeLanguage('Dockerfile'), 'dockerfile');
  assert.equal(codeLanguage('LICENSE'), '');
});
