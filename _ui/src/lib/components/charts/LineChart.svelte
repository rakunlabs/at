<script lang="ts">
  import { scaleLinear, scaleTime } from 'd3-scale';
  import { line as d3Line, curveMonotoneX } from 'd3-shape';
  import { extent, max } from 'd3-array';

  interface Series {
    name: string;
    color: string;
    values: Array<{ x: Date; y: number }>;
  }

  interface Props {
    series: Series[];
    height?: number;
    yLabel?: string;
    formatY?: (v: number) => string;
  }

  let { series, height = 220, yLabel = '', formatY = (v: number) => String(v) }: Props = $props();

  // SVG dimensions
  const margin = { top: 16, right: 16, bottom: 28, left: 52 };
  let width = $state(600);
  let container: HTMLDivElement;

  $effect(() => {
    if (!container) return;
    const ro = new ResizeObserver((entries) => {
      for (const e of entries) {
        width = Math.max(200, e.contentRect.width);
      }
    });
    ro.observe(container);
    return () => ro.disconnect();
  });

  const plotWidth = $derived(Math.max(0, width - margin.left - margin.right));
  const plotHeight = $derived(Math.max(0, height - margin.top - margin.bottom));

  // Domains
  const allPoints = $derived(series.flatMap((s) => s.values));
  const xDomain = $derived(extent(allPoints, (d) => d.x) as [Date, Date] | [undefined, undefined]);
  const yMax = $derived(max(allPoints, (d) => d.y) || 1);

  const xScale = $derived(
    scaleTime()
      .domain(xDomain[0] ? (xDomain as [Date, Date]) : [new Date(), new Date()])
      .range([0, plotWidth])
  );
  const yScale = $derived(scaleLinear().domain([0, yMax * 1.1]).nice().range([plotHeight, 0]));

  const pathFor = $derived((values: Array<{ x: Date; y: number }>) =>
    d3Line<{ x: Date; y: number }>()
      .x((d) => xScale(d.x))
      .y((d) => yScale(d.y))
      .curve(curveMonotoneX)(values) || ''
  );

  // Ticks
  const xTicks = $derived(xScale.ticks(Math.max(2, Math.floor(plotWidth / 100))));
  const yTicks = $derived(yScale.ticks(4));

  function formatTick(d: Date): string {
    // Prefer short date for day buckets
    return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  }
</script>

<div bind:this={container} class="w-full">
  <svg {width} {height} class="overflow-visible">
    <!-- Y gridlines -->
    {#each yTicks as t}
      <line
        x1={margin.left}
        x2={margin.left + plotWidth}
        y1={margin.top + yScale(t)}
        y2={margin.top + yScale(t)}
        class="stroke-gray-200 dark:stroke-dark-border"
        stroke-width="1"
      />
      <text
        x={margin.left - 6}
        y={margin.top + yScale(t) + 3}
        class="text-[10px] fill-gray-500 dark:fill-dark-text-muted"
        text-anchor="end"
      >
        {formatY(t)}
      </text>
    {/each}

    <!-- X axis ticks -->
    {#each xTicks as t}
      <text
        x={margin.left + xScale(t)}
        y={height - 8}
        class="text-[10px] fill-gray-500 dark:fill-dark-text-muted"
        text-anchor="middle"
      >
        {formatTick(t)}
      </text>
    {/each}

    <!-- Series paths -->
    <g transform="translate({margin.left}, {margin.top})">
      {#each series as s}
        <path d={pathFor(s.values)} fill="none" stroke={s.color} stroke-width="1.5" />
        {#each s.values as pt}
          <circle cx={xScale(pt.x)} cy={yScale(pt.y)} r="2" fill={s.color} />
        {/each}
      {/each}
    </g>

    <!-- Y label -->
    {#if yLabel}
      <text
        x={12}
        y={margin.top + plotHeight / 2}
        class="text-[10px] fill-gray-500 dark:fill-dark-text-muted"
        transform="rotate(-90, 12, {margin.top + plotHeight / 2})"
        text-anchor="middle"
      >
        {yLabel}
      </text>
    {/if}
  </svg>

  <!-- Legend -->
  {#if series.length > 1}
    <div class="flex flex-wrap gap-3 mt-1 text-[11px] text-gray-600 dark:text-dark-text-secondary">
      {#each series as s}
        <div class="flex items-center gap-1.5">
          <span class="inline-block w-3 h-0.5" style="background: {s.color}"></span>
          <span>{s.name}</span>
        </div>
      {/each}
    </div>
  {/if}
</div>
