<script lang="ts">
  import { max } from 'd3-array';
  import { scaleBand, scaleLinear } from 'd3-scale';

  interface Value {
    x: Date;
    y: number | null;
  }

  interface Series {
    name: string;
    color: string;
    values: Value[];
  }

  interface Props {
    series: Series[];
    height?: number;
    formatY?: (value: number) => string;
  }

  let { series, height = 220, formatY = (value: number) => String(value) }: Props = $props();

  const margin = { top: 16, right: 16, bottom: 28, left: 52 };
  let width = $state(600);
  let container: HTMLDivElement;

  $effect(() => {
    if (!container) return;
    const observer = new ResizeObserver(entries => {
      for (const entry of entries) width = Math.max(200, entry.contentRect.width);
    });
    observer.observe(container);
    return () => observer.disconnect();
  });

  const plotWidth = $derived(Math.max(0, width - margin.left - margin.right));
  const plotHeight = $derived(Math.max(0, height - margin.top - margin.bottom));
  const buckets = $derived(
    [...new Map(series.flatMap(item => item.values).map(value => [value.x.getTime(), value.x])).values()]
      .sort((a, b) => a.getTime() - b.getTime()),
  );
  const bucketKeys = $derived(buckets.map(value => String(value.getTime())));
  const xScale = $derived(scaleBand<string>().domain(bucketKeys).range([0, plotWidth]).paddingInner(0.18).paddingOuter(0.08));
  const seriesScale = $derived(scaleBand<string>().domain(series.map(item => item.name)).range([0, xScale.bandwidth()]).padding(0.08));
  const yMax = $derived(max(series.flatMap(item => item.values), value => value.y ?? undefined) || 1);
  const yScale = $derived(scaleLinear().domain([0, yMax * 1.1]).nice().range([plotHeight, 0]));
  const yTicks = $derived(yScale.ticks(4));
  const tickStep = $derived(Math.max(1, Math.ceil(buckets.length / Math.max(2, Math.floor(plotWidth / 90)))));
  const span = $derived((buckets.at(-1)?.getTime() || 0) - (buckets[0]?.getTime() || 0));

  function formatTick(value: Date): string {
    if (span <= 2 * 24 * 60 * 60 * 1000) {
      return value.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
    }
    return value.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  }
</script>

<div bind:this={container} class="w-full">
  <svg {width} {height} class="overflow-visible">
    {#each yTicks as tick}
      <line x1={margin.left} x2={margin.left + plotWidth} y1={margin.top + yScale(tick)} y2={margin.top + yScale(tick)} class="stroke-gray-200 dark:stroke-dark-border" stroke-width="1" />
      <text x={margin.left - 6} y={margin.top + yScale(tick) + 3} class="fill-gray-500 text-[10px] dark:fill-dark-text-muted" text-anchor="end">{formatY(tick)}</text>
    {/each}

    {#each buckets as value, index}
      {#if index % tickStep === 0 || index === buckets.length - 1}
        <text x={margin.left + (xScale(String(value.getTime())) || 0) + xScale.bandwidth() / 2} y={height - 8} class="fill-gray-500 text-[10px] dark:fill-dark-text-muted" text-anchor="middle">{formatTick(value)}</text>
      {/if}
    {/each}

    <g transform={`translate(${margin.left}, ${margin.top})`}>
      {#each series as item}
        {#each item.values as value}
          {#if value.y !== null}
            {@const x = (xScale(String(value.x.getTime())) || 0) + (seriesScale(item.name) || 0)}
            {@const barWidth = Math.max(1, seriesScale.bandwidth())}
            {@const barHeight = Math.max(0, plotHeight - yScale(value.y))}
            <rect x={x} y={yScale(value.y)} width={barWidth} height={barHeight} fill={item.color}>
              <title>{item.name} · {value.x.toLocaleString()} · {formatY(value.y)}</title>
            </rect>
          {/if}
        {/each}
      {/each}
    </g>
  </svg>

  {#if series.length > 1}
    <div class="mt-1 flex flex-wrap gap-3 text-[11px] text-gray-600 dark:text-dark-text-secondary">
      {#each series as item}
        <div class="flex items-center gap-1.5"><span class="inline-block h-2.5 w-2.5" style={`background:${item.color}`}></span><span>{item.name}</span></div>
      {/each}
    </div>
  {/if}
</div>
