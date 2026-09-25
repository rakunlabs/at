import { Marked } from 'marked';

import { highlightCode } from '@/lib/helper/markdown';
import { resolveSkillLink } from '@/lib/helper/skill-files';

// Skill files arrive from marketplaces, imports and other workspace members,
// so they are rendered like safe-markdown.ts — raw HTML escaped, no images,
// no mermaid, only http(s)/mailto links — rather than through md(), which
// passes HTML through. On top of that it keeps syntax highlighting and turns
// relative links to other files in the folder into in-dialog navigation.

const escape = (value: string) => value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');

let context: { currentPath: string; hasFile: (path: string) => boolean } = { currentPath: '', hasFile: () => false };

const renderer = new Marked({ gfm: true, breaks: false, renderer: {
  html: ({ text }) => escape(text),
  image: ({ href, text }) => `<span class="skill-md-image">[image: ${escape(text || href)}]</span>`,
  code({ text, lang }) {
    const language = /^[\w-]+$/.test(lang ?? '') ? lang! : '';
    return `<pre><code class="hljs${language ? ` language-${language}` : ''}">${highlightCode(text, language)}</code></pre>\n`;
  },
  link({ href, tokens }) {
    const label = this.parser.parseInline(tokens);
    if (/[\u0000- \u007f]/.test(href)) return label;
    if (/^(https?:\/\/|mailto:)/i.test(href)) {
      return `<a href="${escape(href)}" target="_blank" rel="noopener noreferrer" referrerpolicy="no-referrer">${label}</a>`;
    }
    const path = resolveSkillLink(href, context.currentPath);
    if (path && context.hasFile(path)) {
      return `<a href="#" data-skill-path="${escape(path)}" title="Open ${escape(path)}">${label}</a>`;
    }
    return `<span class="skill-md-dead-link" title="${escape(href)} is not in this skill folder">${label}</span>`;
  },
} });

export function renderSkillMarkdown(source: string, currentPath: string, hasFile: (path: string) => boolean): string {
  context = { currentPath, hasFile };
  try {
    return renderer.parse(source, { async: false }) as string;
  } finally {
    context = { currentPath: '', hasFile: () => false };
  }
}
