<script lang="ts">
  import { untrack } from 'svelte';
  import { Loader2, Save, X } from 'lucide-svelte';
  import Markdown from '@/lib/components/Markdown.svelte';
  import type { GuideInput } from '@/lib/api/guides';
  import { iconFor, iconNames } from './guide-icons';
  import type { DisplayGuide } from './builtin-guides';

  interface Props {
    mode: 'new' | 'edit';
    /** Seed values when editing; ignored for `new`. */
    guide?: DisplayGuide | null;
    saving?: boolean;
    onsave: (input: GuideInput) => void;
    oncancel: () => void;
  }

  let { mode, guide = null, saving = false, onsave, oncancel }: Props = $props();

  const NEW_TEMPLATE = '# New guide\n\nStart writing your guide in markdown here.\n';

  // Seeded once on mount — the parent remounts this component per editing
  // session (see the {#key} in Docs.svelte), so props must not keep
  // overwriting the draft while the user types.
  const seed = untrack(() => ({
    title: mode === 'edit' ? (guide?.title ?? '') : '',
    description: mode === 'edit' ? (guide?.description ?? '') : '',
    icon: mode === 'edit' ? (guide?.iconName ?? 'BookOpen') : 'BookOpen',
    content: mode === 'edit' ? (guide?.content ?? '') : NEW_TEMPLATE,
  }));

  let title = $state(seed.title);
  let description = $state(seed.description);
  let icon = $state(seed.icon);
  let content = $state(seed.content);

  const Icon = $derived(iconFor(icon));
  const canSave = $derived(title.trim().length > 0 && !saving);

  function submit(e: Event) {
    e.preventDefault();
    if (!canSave) return;
    onsave({
      title: title.trim(),
      description: description.trim(),
      icon,
      content,
    });
  }

  const field =
    'w-full border border-gray-300 bg-white px-2 py-1.5 text-sm text-gray-900 placeholder:text-gray-600 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-secondary dark:focus-visible:outline-accent';
  const label = 'block text-xs font-medium text-gray-700 dark:text-dark-text-secondary';
  const paneLabel =
    'shrink-0 border-b border-gray-200 bg-gray-50 px-3 py-1.5 text-[11px] font-medium uppercase tracking-wider text-gray-600 dark:border-dark-border dark:bg-dark-elevated dark:text-dark-text-secondary';
</script>

<form onsubmit={submit} class="flex h-full min-h-0 flex-col">
  <div
    class="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-gray-200 px-4 py-2 dark:border-dark-border"
  >
    <h1 class="flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-dark-text">
      <Icon size={15} aria-hidden="true" />
      {mode === 'new' ? 'New guide' : `Edit: ${title || '(untitled)'}`}
    </h1>
    <div class="flex items-center gap-2">
      <button
        type="button"
        onclick={oncancel}
        disabled={saving}
        class="inline-flex items-center gap-1.5 border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 transition-colors hover:bg-gray-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 motion-reduce:transition-none disabled:opacity-50 dark:border-dark-border-subtle dark:text-dark-text-secondary dark:hover:bg-dark-elevated dark:focus-visible:outline-accent"
      >
        <X size={12} aria-hidden="true" />
        Cancel
      </button>
      <button
        type="submit"
        disabled={!canSave}
        class="inline-flex items-center gap-1.5 bg-gray-900 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-gray-800 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 motion-reduce:transition-none disabled:opacity-50 dark:bg-accent dark:text-gray-950 dark:hover:bg-accent-hover dark:focus-visible:outline-accent"
      >
        {#if saving}
          <Loader2 size={12} class="animate-spin motion-reduce:animate-none" aria-hidden="true" />
        {:else}
          <Save size={12} aria-hidden="true" />
        {/if}
        Save
      </button>
    </div>
  </div>

  <div
    class="grid shrink-0 gap-3 border-b border-gray-200 px-4 py-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_10rem] dark:border-dark-border"
  >
    <div>
      <label for="guide-title" class={label}>Title</label>
      <input
        id="guide-title"
        type="text"
        bind:value={title}
        required
        placeholder="My guide"
        class={[field, 'mt-1']}
      />
    </div>
    <div>
      <label for="guide-description" class={label}>Description</label>
      <input
        id="guide-description"
        type="text"
        bind:value={description}
        placeholder="One-line summary"
        class={[field, 'mt-1']}
      />
    </div>
    <div>
      <label for="guide-icon" class={label}>Icon</label>
      <select id="guide-icon" bind:value={icon} class={[field, 'mt-1']}>
        {#each iconNames as name (name)}
          <option value={name}>{name}</option>
        {/each}
      </select>
    </div>
  </div>

  <div class="flex min-h-0 flex-1 flex-col lg:flex-row">
    <div
      class="flex min-h-[16rem] flex-1 flex-col border-b border-gray-200 lg:min-h-0 lg:border-b-0 lg:border-r dark:border-dark-border"
    >
      <div class={paneLabel} id="guide-markdown-label">Markdown</div>
      <textarea
        bind:value={content}
        aria-labelledby="guide-markdown-label"
        placeholder="# Start writing your guide…"
        spellcheck="false"
        class="min-h-0 flex-1 resize-none bg-white p-4 font-mono text-[12px] leading-relaxed text-gray-900 placeholder:text-gray-600 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-gray-900 dark:bg-dark-surface dark:text-dark-text dark:placeholder:text-dark-text-secondary dark:focus-visible:outline-accent"
      ></textarea>
    </div>

    <div class="flex min-h-[16rem] flex-1 flex-col lg:min-h-0">
      <div class={paneLabel}>Preview</div>
      <div class="min-h-0 flex-1 overflow-y-auto bg-gray-50 px-5 py-4 dark:bg-dark-base">
        {#if content.trim()}
          <Markdown
            source={content}
            as="article"
            class="max-w-none text-[13.5px] leading-[1.7]"
            enhance
          />
        {:else}
          <p class="text-sm text-gray-600 dark:text-dark-text-secondary">
            The preview appears here as you type.
          </p>
        {/if}
      </div>
    </div>
  </div>
</form>
