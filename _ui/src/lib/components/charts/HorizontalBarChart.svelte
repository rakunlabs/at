<script lang="ts">
  interface Row {
    label: string;
    value: number;
    color?: string;
  }

  interface Props {
    rows: Row[];
    formatValue?: (v: number) => string;
    maxRows?: number;
    barColor?: string;
  }

  let {
    rows,
    formatValue = (v: number) => String(v),
    maxRows = 10,
    barColor = '#2563eb',
  }: Props = $props();

  const visible = $derived(rows.slice(0, maxRows));
  const maxVal = $derived(Math.max(1, ...visible.map((r) => r.value)));
</script>

<div class="flex flex-col gap-1.5">
  {#each visible as row}
    <div class="flex items-center gap-2 text-xs">
      <div class="w-28 truncate text-gray-600 dark:text-dark-text-secondary font-mono" title={row.label}>
        {row.label || '(none)'}
      </div>
      <div class="flex-1 h-4 relative bg-gray-100 dark:bg-dark-elevated rounded-sm overflow-hidden">
        <div
          class="h-full"
          style="width: {(row.value / maxVal) * 100}%; background: {row.color || barColor}"
        ></div>
      </div>
      <div class="w-20 text-right font-mono text-gray-900 dark:text-dark-text tabular-nums">
        {formatValue(row.value)}
      </div>
    </div>
  {:else}
    <div class="text-xs text-gray-400 dark:text-dark-text-muted">No data</div>
  {/each}
</div>
