<script lang="ts">
  // One HTTP route: method badge, absolute path and an optional summary.
  interface Props {
    method: string;
    path: string;
    baseUrl?: string;
    summary?: string;
  }

  let { method, path, baseUrl = '', summary = '' }: Props = $props();

  const tone = $derived(
    method === 'GET'
      ? 'text-accent-text'
      : method === 'POST'
        ? 'text-oc-green'
        : method === 'DELETE'
          ? 'text-oc-red'
          : 'text-oc-violet',
  );
</script>

<div class="border border-dark-border px-3 py-2">
  <div class="flex min-w-0 items-baseline gap-3">
    <span class={['w-12 shrink-0 font-mono text-[11px] font-semibold', tone]}>{method}</span>
    <code class="min-w-0 break-all font-mono text-[12.5px] text-dark-text">
      {#if baseUrl}<span class="text-dark-text-muted">{baseUrl}</span>{/if}{path}
    </code>
  </div>
  {#if summary}
    <p class="mt-1 pl-15 text-xs leading-relaxed text-dark-text-muted">{summary}</p>
  {/if}
</div>
