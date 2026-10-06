<script lang="ts">
  import { onDestroy } from 'svelte';
  import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection } from '@codemirror/view';
  import { EditorState, Compartment } from '@codemirror/state';
  import { defaultKeymap, indentWithTab, history, historyKeymap } from '@codemirror/commands';
  import { syntaxHighlighting, defaultHighlightStyle, bracketMatching, indentOnInput } from '@codemirror/language';
  import { searchKeymap, highlightSelectionMatches } from '@codemirror/search';
  import { oneDark } from '@codemirror/theme-one-dark';
  import { languageFor } from '@/lib/helper/developer-space';

  interface Props {
    path: string;
    /** Initial document. Changing it replaces the document (used after a reload). */
    value: string;
    readonly?: boolean;
    onchange: (value: string) => void;
    onsave: () => void;
  }
  let { path, value, readonly = false, onchange, onsave }: Props = $props();

  let host: HTMLDivElement;
  let view: EditorView | null = null;
  const language = new Compartment();
  const editable = new Compartment();
  let applied = '';

  $effect(() => {
    if (!host || view) return;
    applied = value;
    view = new EditorView({
      parent: host,
      doc: value,
      extensions: [
        lineNumbers(),
        highlightActiveLine(),
        highlightActiveLineGutter(),
        drawSelection(),
        history(),
        bracketMatching(),
        indentOnInput(),
        syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
        highlightSelectionMatches(),
        keymap.of([
          { key: 'Mod-s', preventDefault: true, run: () => { onsave(); return true; } },
          ...defaultKeymap, ...historyKeymap, ...searchKeymap, indentWithTab,
        ]),
        EditorView.updateListener.of(update => {
          if (update.docChanged) {
            applied = update.state.doc.toString();
            onchange(applied);
          }
        }),
        EditorState.tabSize.of(4),
        EditorView.theme({
          '&': { height: '100%' },
          '.cm-scroller': { overflow: 'auto', fontFamily: 'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace', fontSize: '13px', lineHeight: '1.5' },
          '.cm-gutters': { minWidth: '40px' },
        }),
        language.of([]),
        oneDark,
        editable.of([EditorState.readOnly.of(readonly), EditorView.editable.of(!readonly)]),
      ],
    });
  });

  // Language modes are loaded on demand so opening the page does not pull
  // every grammar into the first bundle.
  $effect(() => {
    const current = path;
    if (!view) return;
    let cancelled = false;
    void languageFor(current).then(extension => {
      if (!cancelled && view) view.dispatch({ effects: language.reconfigure(extension) });
    });
    return () => { cancelled = true; };
  });


  $effect(() => {
    view?.dispatch({ effects: editable.reconfigure([EditorState.readOnly.of(readonly), EditorView.editable.of(!readonly)]) });
  });

  // An external change (reload after the agent edited the file) replaces the
  // document; typing in the editor never round-trips through here.
  $effect(() => {
    const next = value;
    if (!view || next === applied) return;
    applied = next;
    view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: next } });
  });

  onDestroy(() => { view?.destroy(); view = null; });
</script>

<div bind:this={host} class="h-full min-h-0 overflow-hidden bg-[#282c34]"></div>
