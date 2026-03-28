<script lang="ts">
  import { Maximize2, Minimize2 } from 'lucide-svelte';
  import { javascript } from '@codemirror/lang-javascript';
  import { python } from '@codemirror/lang-python';
  import { json } from '@codemirror/lang-json';
  import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection } from '@codemirror/view';
  import { EditorState } from '@codemirror/state';
  import { defaultKeymap, indentWithTab, history, historyKeymap } from '@codemirror/commands';
  import { syntaxHighlighting, defaultHighlightStyle, bracketMatching, indentOnInput, StreamLanguage } from '@codemirror/language';
  import { searchKeymap, highlightSelectionMatches } from '@codemirror/search';
  import { shell } from '@codemirror/legacy-modes/mode/shell';
  import { oneDark } from '@codemirror/theme-one-dark';

  interface Props {
    value: string;
    label?: string;
    placeholder?: string;
    rows?: number;
    language?: string;
  }

  let { value = $bindable(), label = 'Code', placeholder = '', rows = 4, language = '' }: Props = $props();

  let expanded = $state(false);
  let cmContainer: HTMLDivElement | undefined = $state();
  let cmView: EditorView | undefined = $state();

  function getLangExtension() {
    if (language === 'javascript' || language === 'js') return [javascript()];
    if (language === 'python') return [python()];
    if (language === 'json') return [json()];
    if (language === 'bash' || language === 'shell' || language === 'sh') return [StreamLanguage.define(shell)];
    return [];
  }

  function openExpanded() {
    expanded = true;
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        if (cmContainer) createEditor(cmContainer);
      });
    });
  }

  function closeExpanded() {
    if (cmView) {
      value = cmView.state.doc.toString();
      cmView.destroy();
      cmView = undefined;
    }
    expanded = false;
  }

  function createEditor(container: HTMLDivElement) {
    if (cmView) {
      cmView.destroy();
      cmView = undefined;
    }

    const extensions = [
      lineNumbers(),
      highlightActiveLine(),
      highlightActiveLineGutter(),
      drawSelection(),
      history(),
      bracketMatching(),
      indentOnInput(),
      syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
      highlightSelectionMatches(),
      keymap.of([...defaultKeymap, ...historyKeymap, ...searchKeymap, indentWithTab]),
      EditorView.updateListener.of((update) => {
        if (update.docChanged) {
          value = update.state.doc.toString();
        }
      }),
      EditorState.tabSize.of(2),
      EditorView.theme({
        '&': { height: '100%' },
        '.cm-scroller': {
          overflow: 'auto',
          fontFamily: 'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace',
          fontSize: '13px',
          lineHeight: '1.5',
        },
        '.cm-content': { padding: '8px 0' },
        '.cm-gutters': { minWidth: '40px' },
      }),
      ...getLangExtension(),
      oneDark,
    ];

    cmView = new EditorView({
      doc: value,
      extensions,
      parent: container,
    });

    cmView.focus();
  }

  function handleInlineKeydown(e: KeyboardEvent) {
    if (e.key === 'Tab') {
      e.preventDefault();
      const textarea = e.target as HTMLTextAreaElement;
      const start = textarea.selectionStart;
      const end = textarea.selectionEnd;
      value = value.substring(0, start) + '  ' + value.substring(end);
      setTimeout(() => {
        textarea.selectionStart = textarea.selectionEnd = start + 2;
      }, 0);
    }
  }
</script>

<!-- Inline textarea with expand button -->
<div class="relative">
  <div class="flex items-center justify-between mb-0.5">
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">{label}</span>
    <button
      onclick={openExpanded}
      class="flex items-center gap-0.5 text-[10px] text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
      title="Expand editor"
    >
      <Maximize2 size={10} />
      Expand
    </button>
  </div>
  <textarea
    bind:value
    {rows}
    onkeydown={handleInlineKeydown}
    class="w-full px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text resize-y"
    {placeholder}
  ></textarea>
</div>

<!-- Expanded overlay with CodeMirror -->
{#if expanded}
  <div class="fixed inset-0 z-[1100] flex flex-col bg-white dark:bg-dark-surface">
    <!-- Header -->
    <div class="flex items-center justify-between px-4 py-2 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50 shrink-0">
      <div class="flex items-center gap-2">
        <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">{label}</span>
        {#if language}
          <span class="text-[10px] font-mono text-gray-400 dark:text-dark-text-muted px-1.5 py-0.5 bg-gray-100 dark:bg-dark-elevated rounded">{language}</span>
        {/if}
      </div>
      <button
        onclick={closeExpanded}
        class="flex items-center gap-1 px-3 py-1 text-xs font-medium text-gray-600 dark:text-dark-text-secondary hover:text-gray-800 dark:hover:text-dark-text hover:bg-gray-100 dark:hover:bg-dark-elevated rounded transition-colors"
      >
        <Minimize2 size={12} />
        Close
      </button>
    </div>

    <!-- CodeMirror -->
    <div bind:this={cmContainer} class="flex-1 overflow-hidden"></div>

    <!-- Footer -->
    <div class="flex items-center justify-between px-4 py-2 border-t border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50 shrink-0">
      <span class="text-[10px] text-gray-400 dark:text-dark-text-muted font-mono">{value.split('\n').length} lines · {value.length} chars</span>
      <button
        onclick={closeExpanded}
        class="px-3 py-1 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover rounded transition-colors"
      >
        Done
      </button>
    </div>
  </div>
{/if}
