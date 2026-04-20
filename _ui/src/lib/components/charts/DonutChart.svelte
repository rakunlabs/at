<script lang="ts">
  import { pie as d3Pie, arc as d3Arc } from 'd3-shape';

  interface Slice {
    label: string;
    value: number;
    color: string;
  }

  interface Props {
    slices: Slice[];
    size?: number;
    formatValue?: (v: number) => string;
  }

  let { slices, size = 160, formatValue = (v: number) => String(v) }: Props = $props();

  const radius = $derived(size / 2);
  const inner = $derived(radius * 0.6);

  const pieLayout = d3Pie<Slice>().value((d) => d.value).sort(null);
  const arcGen = $derived(
    d3Arc<ReturnType<typeof pieLayout>[number]>()
      .innerRadius(inner)
      .outerRadius(radius - 2)
  );

  const arcs = $derived(pieLayout(slices));
  const total = $derived(slices.reduce((sum, s) => sum + s.value, 0));
</script>

<div class="flex items-center gap-4">
  <svg width={size} height={size} class="overflow-visible">
    <g transform="translate({radius}, {radius})">
      {#each arcs as a}
        <path d={arcGen(a) || ''} fill={a.data.color} />
      {/each}
      <text
        y="4"
        text-anchor="middle"
        class="text-xs font-medium fill-gray-700 dark:fill-dark-text-secondary"
      >
        {formatValue(total)}
      </text>
    </g>
  </svg>
  <div class="flex flex-col gap-1 text-xs">
    {#each slices as s}
      <div class="flex items-center gap-2">
        <span class="inline-block w-3 h-3 rounded-sm" style="background: {s.color}"></span>
        <span class="font-mono text-gray-600 dark:text-dark-text-secondary truncate max-w-24" title={s.label}>
          {s.label || '(none)'}
        </span>
        <span class="ml-auto font-mono text-gray-900 dark:text-dark-text">{formatValue(s.value)}</span>
      </div>
    {:else}
      <div class="text-gray-400 dark:text-dark-text-muted">No data</div>
    {/each}
  </div>
</div>
