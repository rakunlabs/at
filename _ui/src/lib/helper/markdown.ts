import { Marked } from 'marked';
import hljs from 'highlight.js/lib/core';
import katex from 'katex';
import mermaid from 'mermaid';

// ─── Register highlight.js languages ───

import bash from 'highlight.js/lib/languages/bash';
import css from 'highlight.js/lib/languages/css';
import diff from 'highlight.js/lib/languages/diff';
import dockerfile from 'highlight.js/lib/languages/dockerfile';
import go from 'highlight.js/lib/languages/go';
import graphql from 'highlight.js/lib/languages/graphql';
import ini from 'highlight.js/lib/languages/ini';
import java from 'highlight.js/lib/languages/java';
import javascript from 'highlight.js/lib/languages/javascript';
import json from 'highlight.js/lib/languages/json';
import kotlin from 'highlight.js/lib/languages/kotlin';
import makefile from 'highlight.js/lib/languages/makefile';
import markdown from 'highlight.js/lib/languages/markdown';
import nginx from 'highlight.js/lib/languages/nginx';
import php from 'highlight.js/lib/languages/php';
import plaintext from 'highlight.js/lib/languages/plaintext';
import python from 'highlight.js/lib/languages/python';
import ruby from 'highlight.js/lib/languages/ruby';
import rust from 'highlight.js/lib/languages/rust';
import scss from 'highlight.js/lib/languages/scss';
import shell from 'highlight.js/lib/languages/shell';
import sql from 'highlight.js/lib/languages/sql';
import swift from 'highlight.js/lib/languages/swift';
import typescript from 'highlight.js/lib/languages/typescript';
import xml from 'highlight.js/lib/languages/xml';
import yaml from 'highlight.js/lib/languages/yaml';

hljs.registerLanguage('bash', bash);
hljs.registerLanguage('css', css);
hljs.registerLanguage('diff', diff);
hljs.registerLanguage('dockerfile', dockerfile);
hljs.registerLanguage('go', go);
hljs.registerLanguage('graphql', graphql);
hljs.registerLanguage('ini', ini);
hljs.registerLanguage('java', java);
hljs.registerLanguage('javascript', javascript);
hljs.registerLanguage('json', json);
hljs.registerLanguage('kotlin', kotlin);
hljs.registerLanguage('makefile', makefile);
hljs.registerLanguage('markdown', markdown);
hljs.registerLanguage('nginx', nginx);
hljs.registerLanguage('php', php);
hljs.registerLanguage('plaintext', plaintext);
hljs.registerLanguage('python', python);
hljs.registerLanguage('ruby', ruby);
hljs.registerLanguage('rust', rust);
hljs.registerLanguage('scss', scss);
hljs.registerLanguage('shell', shell);
hljs.registerLanguage('sql', sql);
hljs.registerLanguage('swift', swift);
hljs.registerLanguage('typescript', typescript);
hljs.registerLanguage('xml', xml);
hljs.registerLanguage('yaml', yaml);

// Common aliases
hljs.registerAliases(['sh', 'zsh'], { languageName: 'bash' });
hljs.registerAliases(['js', 'jsx'], { languageName: 'javascript' });
hljs.registerAliases(['ts', 'tsx'], { languageName: 'typescript' });
hljs.registerAliases(['py'], { languageName: 'python' });
hljs.registerAliases(['rb'], { languageName: 'ruby' });
hljs.registerAliases(['rs'], { languageName: 'rust' });
hljs.registerAliases(['html', 'svelte', 'vue'], { languageName: 'xml' });
hljs.registerAliases(['yml'], { languageName: 'yaml' });
hljs.registerAliases(['toml', 'env', 'properties', 'cfg'], { languageName: 'ini' });
hljs.registerAliases(['text', 'txt'], { languageName: 'plaintext' });
hljs.registerAliases(['jsonc', 'json5'], { languageName: 'json' });
hljs.registerAliases(['golang'], { languageName: 'go' });

// ─── Mermaid Init ───

let mermaidInitialized = false;

function ensureMermaidInit() {
  if (mermaidInitialized) return;
  mermaidInitialized = true;
  mermaid.initialize({
    startOnLoad: false,
    theme: 'default',
    securityLevel: 'loose',
    fontFamily: 'inherit',
  });
}

// ─── KaTeX Helpers ───

/**
 * Render a LaTeX string to HTML via KaTeX.
 * Returns the original string wrapped in a <code> on failure.
 */
function renderKatex(tex: string, displayMode: boolean): string {
  try {
    return katex.renderToString(tex, {
      displayMode,
      throwOnError: false,
      output: 'html',
    });
  } catch {
    const escaped = tex.replace(/</g, '&lt;').replace(/>/g, '&gt;');
    return displayMode
      ? `<pre><code>${escaped}</code></pre>`
      : `<code>${escaped}</code>`;
  }
}

// ─── Marked Extensions ───

/**
 * Extension: block-level math ($$...$$).
 * Must be registered before inline math so $$ is consumed first.
 */
const blockMathExtension = {
  name: 'blockMath',
  level: 'block' as const,
  start(src: string) {
    return src.indexOf('$$');
  },
  tokenizer(src: string) {
    const match = src.match(/^\$\$([\s\S]+?)\$\$/);
    if (match) {
      return {
        type: 'blockMath',
        raw: match[0],
        text: match[1].trim(),
      };
    }
  },
  renderer(token: { text: string }) {
    return `<div class="katex-display">${renderKatex(token.text, true)}</div>\n`;
  },
};

/**
 * Extension: inline math ($...$).
 * Avoids matching $$ (handled by block math) and currency like $10.
 */
const inlineMathExtension = {
  name: 'inlineMath',
  level: 'inline' as const,
  start(src: string) {
    const idx = src.indexOf('$');
    // Skip if it's $$ (block math) or followed by a digit (currency)
    if (idx >= 0 && src[idx + 1] !== '$' && !/\d/.test(src[idx + 1] || '')) {
      return idx;
    }
    return -1;
  },
  tokenizer(src: string) {
    // Match $...$ but not $$, and not $ followed by space or digit at start
    const match = src.match(/^\$([^\s$](?:[^$]*?[^\s$])?)\$/);
    if (match) {
      return {
        type: 'inlineMath',
        raw: match[0],
        text: match[1],
      };
    }
  },
  renderer(token: { text: string }) {
    return renderKatex(token.text, false);
  },
};

// ─── Marked Instance ───

const marked = new Marked();

marked.use({
  extensions: [blockMathExtension, inlineMathExtension],
  renderer: {
    code({ text, lang }) {
      // Mermaid blocks: wrap in a special container for post-processing
      if (lang === 'mermaid') {
        const escaped = text.replace(/</g, '&lt;').replace(/>/g, '&gt;');
        return `<pre class="mermaid-pending" data-mermaid-source="${encodeURIComponent(text)}"><code class="language-mermaid">${escaped}</code></pre>\n`;
      }

      // Syntax highlighting via highlight.js
      if (lang && hljs.getLanguage(lang)) {
        const highlighted = hljs.highlight(text, { language: lang, ignoreIllegals: true }).value;
        return `<pre><code class="hljs language-${lang}">${highlighted}</code></pre>\n`;
      }

      // Auto-detect fallback
      const escaped = text.replace(/</g, '&lt;').replace(/>/g, '&gt;');
      return `<pre><code>${escaped}</code></pre>\n`;
    },
  },
  gfm: true,
  breaks: false,
});

// ─── Public API ───

/**
 * Render markdown string to HTML.
 * Synchronous — mermaid diagrams are left as placeholders
 * and rendered in the DOM by the `renderMarkdown` Svelte action.
 */
export function md(source: string): string {
  if (!source) return '';
  return marked.parse(source, { async: false }) as string;
}

// ─── Mermaid Post-Processing (Svelte Action) ───

let renderCounter = 0;

/** Process all pending mermaid blocks inside a container element. */
async function processMermaidBlocks(node: HTMLElement) {
  ensureMermaidInit();

  const blocks = node.querySelectorAll<HTMLPreElement>('pre.mermaid-pending');

  for (const pre of blocks) {
    // Skip already-rendered blocks
    if (pre.dataset.mermaidRendered === 'true') continue;

    const source = decodeURIComponent(pre.dataset.mermaidSource || '').trim();
    if (!source) continue;

    // Validate before rendering — skip incomplete diagrams (still streaming)
    try {
      await mermaid.parse(source);
    } catch {
      continue;
    }

    try {
      const id = `mermaid-${++renderCounter}`;
      const { svg } = await mermaid.render(id, source);

      const wrapper = document.createElement('div');
      wrapper.className = 'mermaid-diagram my-2 overflow-x-auto';
      wrapper.innerHTML = svg;
      wrapper.dataset.mermaidRendered = 'true';
      wrapper.dataset.mermaidSource = source;

      pre.replaceWith(wrapper);
    } catch {
      pre.dataset.mermaidRendered = 'error';
    }
  }
}

/**
 * Svelte action that post-processes rendered markdown HTML:
 * - Renders mermaid diagram placeholders into SVGs
 * - Watches for DOM mutations (streaming content) and re-processes
 *
 * Usage:
 *   <div class="markdown-body" use:renderMarkdown>{@html md(text)}</div>
 */
export function renderMarkdown(node: HTMLElement) {
  let timer: ReturnType<typeof setTimeout> | null = null;

  function scheduleProcess() {
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => {
      timer = null;
      processMermaidBlocks(node);
    }, 300);
  }

  // Initial render
  scheduleProcess();

  // Watch for content changes (streaming appends text incrementally)
  const observer = new MutationObserver(() => {
    scheduleProcess();
  });

  observer.observe(node, {
    childList: true,
    subtree: true,
    characterData: true,
  });

  return {
    destroy() {
      observer.disconnect();
      if (timer) clearTimeout(timer);
    },
  };
}
