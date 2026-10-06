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
  const total = $derived(rows.reduce((sum, r) => sum + r.value, 0));
  let hoverIndex = $state<number | null>(null);
</script>

<div class="flex flex-col gap-1.5">
  {#each visible as row, index}
    <div
      class={["relative flex items-center gap-2 text-xs", hoverIndex === index ? 'bg-dark-base' : '']}
      role="presentation"
      onmouseenter={() => (hoverIndex = index)}
      onmouseleave={() => (hoverIndex = null)}
    >
      <div class="w-28 truncate text-dark-text-secondary font-mono">
        {row.label || '(none)'}
      </div>
      <div class="flex-1 h-4 relative bg-dark-elevated overflow-hidden">
        <div
          class="h-full"
          style="width: {(row.value / maxVal) * 100}%; background: {row.color || barColor}; opacity: {hoverIndex === null || hoverIndex === index ? 1 : 0.45}"
        ></div>
      </div>
      <div class="w-20 text-right font-mono text-dark-text tabular-nums">
        {formatValue(row.value)}
      </div>
      {#if hoverIndex === index}
        <div class="pointer-events-none absolute bottom-full left-0 z-10 mb-1 max-w-full border px-2.5 py-1.5 text-[11px] shadow-sm border-dark-border bg-dark-surface">
          <div class="break-all font-mono text-dark-text">{row.label || '(none)'}</div>
          <div class="text-dark-text-secondary">
            <span class="font-mono tabular-nums">{formatValue(row.value)}</span>
            {#if total > 0}<span class="ml-1">· {((row.value / total) * 100).toFixed(1)}%</span>{/if}
          </div>
        </div>
      {/if}
    </div>
  {:else}
    <div class="text-xs text-dark-text-muted">No data</div>
  {/each}
</div>
