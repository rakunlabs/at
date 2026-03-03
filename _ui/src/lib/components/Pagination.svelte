<script lang="ts">
  interface Props {
    total: number;
    limit: number;
    offset: number;
    onchange?: (newOffset: number) => void;
    class?: string;
  }

  let { 
    total, 
    limit, 
    offset = $bindable(), 
    onchange,
    class: className = ''
  }: Props = $props();

  function next() {
    if (offset + limit < total) {
      offset += limit;
      onchange?.(offset);
    }
  }

  function prev() {
    if (offset - limit >= 0) {
      offset -= limit;
      onchange?.(offset);
    }
  }
</script>

<div class={`flex items-center justify-between px-4 py-3 border-t border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base ${className}`}>
  <div class="text-xs text-gray-500 dark:text-dark-text-muted">
    Showing {offset + 1} to {Math.min(offset + limit, total)} of {total} results
  </div>
  <div class="flex items-center gap-2">
    <button
      onclick={prev}
      disabled={offset === 0}
      class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated hover:bg-gray-50 dark:hover:bg-dark-surface disabled:opacity-50 disabled:cursor-not-allowed text-gray-700 dark:text-dark-text-secondary transition-colors"
    >
      Previous
    </button>
    <button
      onclick={next}
      disabled={offset + limit >= total}
      class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated hover:bg-gray-50 dark:hover:bg-dark-surface disabled:opacity-50 disabled:cursor-not-allowed text-gray-700 dark:text-dark-text-secondary transition-colors"
    >
      Next
    </button>
  </div>
</div>
