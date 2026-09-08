import { Marked } from 'marked';

const escape = (value: string) => value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
// An isolated renderer: no raw HTML, remote images, diagram execution or unsafe URLs.
const safe = new Marked({ gfm: true, renderer: {
  html: ({ text }) => escape(text),
  image: ({ text }) => escape(text),
  link({ href, tokens }) {
    const label = this.parser.parseInline(tokens);
    if (!/^https?:\/\//i.test(href) || /[\u0000-\u0020\u007f]/.test(href)) return label;
    return `<a href="${escape(href)}" target="_blank" rel="noopener noreferrer" referrerpolicy="no-referrer">${label}</a>`;
  },
} });
export const safeMarkdown = (source: string) => safe.parse(source, { async: false });
