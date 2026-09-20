<script lang="ts">
  import type { LoadIssue } from '../helper/page-load.svelte';
  interface Props { issues: LoadIssue[]; retry?: () => void; loading?: boolean }
  let { issues, retry, loading = false }: Props = $props();
</script>

{#if issues.length}
  <section class="mb-4 border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base px-4 py-3 text-sm space-y-2" aria-label="Data availability">
    <ul class="space-y-1">{#each issues as issue (issue.label)}<li role={issue.disabled ? 'status' : 'alert'} class="text-gray-700 dark:text-dark-text-secondary">{issue.message}</li>{/each}</ul>
    {#if issues.some(issue => !issue.disabled)}<p class="text-xs text-gray-500 dark:text-dark-text-muted">Other sections remain available. Previously loaded data may be shown for failed sections.</p>{/if}
    {#if retry}<button type="button" class="settings-button min-h-11 sm:min-h-0" disabled={loading} onclick={retry}>Retry loading</button>{/if}
  </section>
{/if}
