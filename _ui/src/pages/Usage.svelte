<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    getUsageSummary,
    getUsageTimeSeries,
    getUsageGrouped,
    getBudgetUtilization,
    presetRange,
    type UsageFilter,
    type UsageSummary,
    type UsageTimeSeriesPoint,
    type BudgetUtilization,
    type Bucket,
  } from '@/lib/api/usage';
  import { listProviders } from '@/lib/api/providers';
  import { listAgents } from '@/lib/api/agents';
  import { listOrganizations } from '@/lib/api/organizations';
  import LineChart from '@/lib/components/charts/LineChart.svelte';
  import DonutChart from '@/lib/components/charts/DonutChart.svelte';
  import HorizontalBarChart from '@/lib/components/charts/HorizontalBarChart.svelte';
  import DateRangePicker from '@/lib/components/usage/DateRangePicker.svelte';
  import MultiSelect from '@/lib/components/usage/MultiSelect.svelte';
  import {
    BarChart3,
    RefreshCw,
    Activity,
    Zap,
    AlertCircle,
    Clock,
  } from 'lucide-svelte';

  storeNavbar.title = 'Usage';

  // ─── State ───

  const initial = presetRange('7d');
  let from = $state(initial.from);
  let to = $state(initial.to);
  // The currently-active preset. When set to something other than "custom",
  // the Refresh button re-evaluates the preset against `now` so "Last 24h"
  // / "Last 7d" actually slide forward instead of staying frozen at the
  // moment the page was first opened.
  let preset = $state<'24h' | '7d' | '30d' | 'mtd' | 'custom'>('7d');
  let providers = $state<string[]>([]);
  let models = $state<string[]>([]);
  let agentIds = $state<string[]>([]);
  let orgIds = $state<string[]>([]);

  let bucket = $state<Bucket>('day');

  let summary = $state<UsageSummary | null>(null);
  let timeseries = $state<UsageTimeSeriesPoint[]>([]);
  let byProvider = $state<UsageSummary[]>([]);
  let byModel = $state<UsageSummary[]>([]);
  let byAgent = $state<UsageSummary[]>([]);
  let byOrg = $state<UsageSummary[]>([]);
  let byBillingCode = $state<UsageSummary[]>([]);
  let byStatus = $state<UsageSummary[]>([]);
  let budgets = $state<BudgetUtilization[]>([]);

  let availableProviders = $state<string[]>([]);
  let availableModels = $state<string[]>([]);
  let availableAgents = $state<Array<{ value: string; label: string }>>([]);
  let availableOrgs = $state<Array<{ value: string; label: string }>>([]);
  // Agent/org name lookup for pretty-printing the top-N tables.
  let agentNameById = $state<Record<string, string>>({});
  let orgNameById = $state<Record<string, string>>({});

  let loading = $state(false);

  // ─── Palette ───
  // Small stable color cycle; order matters so repeated renders pick the same colors.
  const palette = [
    '#2563eb', // blue
    '#16a34a', // green
    '#ea580c', // orange
    '#9333ea', // purple
    '#dc2626', // red
    '#0891b2', // cyan
    '#ca8a04', // yellow
    '#db2777', // pink
    '#4b5563', // gray
    '#059669', // emerald
  ];
  function colorFor(i: number) {
    return palette[i % palette.length];
  }

  // ─── Loaders ───

  const filter: () => UsageFilter = () => ({
    from,
    to,
    provider: providers.length ? providers : undefined,
    model: models.length ? models : undefined,
    agent_id: agentIds.length ? agentIds : undefined,
    org_id: orgIds.length ? orgIds : undefined,
  });

  async function loadAll() {
    loading = true;
    try {
      const [sum, ts, byP, byM, byA, byO, byBC, byS, bud] = await Promise.all([
        getUsageSummary(filter()),
        getUsageTimeSeries(filter(), bucket),
        getUsageGrouped(filter(), 'provider'),
        getUsageGrouped(filter(), 'model', 10),
        getUsageGrouped(filter(), 'agent', 10),
        getUsageGrouped(filter(), 'org', 10),
        getUsageGrouped(filter(), 'billing_code', 10),
        getUsageGrouped(filter(), 'status'),
        getBudgetUtilization(),
      ]);
      summary = sum;
      timeseries = ts;
      byProvider = byP;
      byModel = byM;
      byAgent = byA;
      byOrg = byO;
      byBillingCode = byBC;
      byStatus = byS;
      budgets = bud;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load usage data', 'alert');
    } finally {
      loading = false;
    }
  }

  async function loadFilterOptions() {
    // Load providers/models/agents/orgs in parallel so the filter bar is
    // usable as soon as possible. All failures are non-fatal.
    const [providersRes, agentsRes, orgsRes] = await Promise.allSettled([
      listProviders({ _limit: 100 }),
      listAgents({ _limit: 200 }),
      listOrganizations({ _limit: 100 }),
    ]);

    if (providersRes.status === 'fulfilled') {
      const data = providersRes.value.data || [];
      availableProviders = data.map((p) => p.key).sort();
      const modelSet = new Set<string>();
      for (const p of data) {
        for (const m of p.config.models || []) modelSet.add(m);
        if (p.config.model) modelSet.add(p.config.model);
      }
      availableModels = [...modelSet].sort();
    }

    if (agentsRes.status === 'fulfilled') {
      const data = agentsRes.value.data || [];
      availableAgents = data
        .map((a) => ({ value: a.id, label: a.name || a.id.slice(0, 12) }))
        .sort((a, b) => a.label.localeCompare(b.label));
      agentNameById = Object.fromEntries(data.map((a) => [a.id, a.name || a.id]));
    }

    if (orgsRes.status === 'fulfilled') {
      const data = orgsRes.value.data || [];
      availableOrgs = data
        .map((o) => ({ value: o.id, label: o.name || o.id.slice(0, 12) }))
        .sort((a, b) => a.label.localeCompare(b.label));
      orgNameById = Object.fromEntries(data.map((o) => [o.id, o.name || o.id]));
    }
  }

  function handleRangeChange(r: { from: string; to: string; preset: string }) {
    from = r.from;
    to = r.to;
    preset = r.preset as typeof preset;
    // Auto-select a sensible bucket based on range length.
    const diffMs = new Date(r.to).getTime() - new Date(r.from).getTime();
    bucket = diffMs <= 48 * 60 * 60 * 1000 ? 'hour' : 'day';
    loadAll();
  }

  function handleFilterChange() {
    loadAll();
  }

  // Refresh re-evaluates the active preset against the current clock so
  // sliding-window presets (24h / 7d / 30d / mtd) actually advance. For
  // "custom" ranges we leave the user's explicit from/to alone.
  function refresh() {
    if (preset !== 'custom') {
      const r = presetRange(preset);
      from = r.from;
      to = r.to;
    }
    loadAll();
  }

  loadFilterOptions();
  loadAll();

  // ─── Derived chart data ───

  const timeseriesRequests = $derived(
    timeseries.map((p) => ({ x: new Date(p.bucket), y: p.request_count })),
  );
  const timeseriesErrors = $derived(
    timeseries.map((p) => ({ x: new Date(p.bucket), y: p.error_count })),
  );
  const timeseriesInputTokens = $derived(
    timeseries.map((p) => ({ x: new Date(p.bucket), y: p.input_tokens })),
  );
  const timeseriesOutputTokens = $derived(
    timeseries.map((p) => ({ x: new Date(p.bucket), y: p.output_tokens })),
  );
  const timeseriesLatency = $derived(
    timeseries.map((p) => ({ x: new Date(p.bucket), y: Math.round(p.avg_latency_ms) })),
  );

  const providerSlices = $derived(
    byProvider.map((r, i) => ({
      label: r.key || '(none)',
      value: r.total_tokens,
      color: colorFor(i),
    })),
  );

  const modelRows = $derived(
    byModel.map((r, i) => ({
      label: r.key || '(none)',
      value: r.total_tokens,
      color: colorFor(i),
    })),
  );

  const agentRows = $derived(
    byAgent.map((r, i) => ({
      // Prefer the agent's human name if we have it; fall back to a short ID.
      label: r.key ? (agentNameById[r.key] || r.key.slice(0, 14)) : '(none)',
      value: r.request_count,
      color: colorFor(i),
    })),
  );

  const orgRows = $derived(
    byOrg.map((r, i) => ({
      label: r.key ? (orgNameById[r.key] || r.key.slice(0, 14)) : '(none)',
      value: r.total_tokens,
      color: colorFor(i),
    })),
  );

  const billingRows = $derived(
    byBillingCode.map((r, i) => ({
      label: r.key || '(none)',
      value: r.total_tokens,
      color: colorFor(i),
    })),
  );

  // ─── Formatters ───

  function fmtNum(n: number): string {
    if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(2)}B`;
    if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
    if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
    return String(n);
  }
  function fmtCost(cents: number): string {
    return `$${(cents / 100).toFixed(4)}`;
  }
  function fmtLatency(ms: number): string {
    if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`;
    return `${Math.round(ms)}ms`;
  }
  function fmtPct(pct: number): string {
    return `${pct.toFixed(1)}%`;
  }
  function fmtInt(n: number): string {
    return String(Math.round(n));
  }

  const errorRate = $derived(
    summary && summary.request_count > 0
      ? (summary.error_count / summary.request_count) * 100
      : 0,
  );
</script>

<svelte:head>
  <title>AT | Usage</title>
</svelte:head>

<div class="p-6 max-w-7xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-4">
    <div class="flex items-center gap-2">
      <BarChart3 size={16} class="text-gray-500 dark:text-dark-text-muted" />
      <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Usage</h2>
      {#if summary}
        <span class="text-xs text-gray-400 dark:text-dark-text-muted">
          ({fmtNum(summary.request_count)} calls)
        </span>
      {/if}
    </div>
    <button
      onclick={refresh}
      disabled={loading}
      class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors disabled:opacity-50"
      title="Refresh"
    >
      <RefreshCw size={14} class={loading ? 'animate-spin' : ''} />
    </button>
  </div>

  <!-- Filters -->
  <div class="flex flex-wrap items-center gap-2 mb-4 pb-3 border-b border-gray-200 dark:border-dark-border">
    <DateRangePicker bind:from bind:to bind:preset onchange={handleRangeChange} />
    <MultiSelect
      label="Provider"
      options={availableProviders}
      bind:selected={providers}
      onchange={handleFilterChange}
    />
    <MultiSelect
      label="Model"
      options={availableModels}
      bind:selected={models}
      onchange={handleFilterChange}
    />
    <MultiSelect
      label="Agent"
      options={availableAgents}
      bind:selected={agentIds}
      onchange={handleFilterChange}
    />
    <MultiSelect
      label="Organization"
      options={availableOrgs}
      bind:selected={orgIds}
      onchange={handleFilterChange}
    />
    <div class="ml-auto flex items-center gap-1 text-xs">
      <span class="text-gray-500 dark:text-dark-text-muted">Bucket:</span>
      <button
        onclick={() => { bucket = 'hour'; loadAll(); }}
        class={[
          'px-2 py-1 border',
          bucket === 'hour'
            ? 'bg-gray-900 text-white border-gray-900 dark:bg-accent dark:border-accent'
            : 'border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary',
        ]}
      >Hour</button>
      <button
        onclick={() => { bucket = 'day'; loadAll(); }}
        class={[
          'px-2 py-1 border',
          bucket === 'day'
            ? 'bg-gray-900 text-white border-gray-900 dark:bg-accent dark:border-accent'
            : 'border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary',
        ]}
      >Day</button>
    </div>
  </div>

  <!-- KPI Cards -->
  {#if summary}
    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3 mb-4">
      <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
        <div class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-text-muted mb-1">
          <Activity size={12} /> Requests
        </div>
        <div class="text-xl font-semibold text-gray-900 dark:text-dark-text tabular-nums">
          {fmtNum(summary.request_count)}
        </div>
        <div class="text-[11px] text-gray-400 dark:text-dark-text-muted mt-0.5">
          {fmtCost(summary.cost_cents)} total cost
        </div>
      </div>

      <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
        <div class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-text-muted mb-1">
          <Zap size={12} /> Tokens
        </div>
        <div class="text-xl font-semibold text-gray-900 dark:text-dark-text tabular-nums">
          {fmtNum(summary.total_tokens)}
        </div>
        <div class="text-[11px] text-gray-400 dark:text-dark-text-muted mt-0.5">
          in {fmtNum(summary.input_tokens)} / out {fmtNum(summary.output_tokens)}
        </div>
      </div>

      <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
        <div class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-text-muted mb-1">
          <AlertCircle size={12} /> Error rate
        </div>
        <div
          class="text-xl font-semibold tabular-nums"
          class:text-red-600={errorRate > 5}
          class:text-gray-900={errorRate <= 5}
          class:dark:text-red-400={errorRate > 5}
          class:dark:text-dark-text={errorRate <= 5}
        >
          {fmtPct(errorRate)}
        </div>
        <div class="text-[11px] text-gray-400 dark:text-dark-text-muted mt-0.5">
          {fmtNum(summary.error_count)} failed calls
        </div>
      </div>

      <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
        <div class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-text-muted mb-1">
          <Clock size={12} /> Avg latency
        </div>
        <div class="text-xl font-semibold text-gray-900 dark:text-dark-text tabular-nums">
          {fmtLatency(summary.avg_latency_ms)}
        </div>
        <div class="text-[11px] text-gray-400 dark:text-dark-text-muted mt-0.5">
          max {fmtLatency(summary.max_latency_ms)}
        </div>
      </div>
    </div>
  {/if}

  <!-- Time-series charts -->
  <div class="grid grid-cols-1 lg:grid-cols-2 gap-3 mb-4">
    <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
      <div class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-2">
        Requests &amp; errors over time
      </div>
      <LineChart
        series={[
          { name: 'Requests', color: '#2563eb', values: timeseriesRequests },
          { name: 'Errors', color: '#dc2626', values: timeseriesErrors },
        ]}
        formatY={fmtInt}
      />
    </div>

    <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
      <div class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-2">
        Tokens over time
      </div>
      <LineChart
        series={[
          { name: 'Input', color: '#16a34a', values: timeseriesInputTokens },
          { name: 'Output', color: '#ea580c', values: timeseriesOutputTokens },
        ]}
        formatY={fmtNum}
      />
    </div>

    <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface lg:col-span-2">
      <div class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-2">
        Average latency over time
      </div>
      <LineChart
        series={[{ name: 'Avg latency (ms)', color: '#9333ea', values: timeseriesLatency }]}
        formatY={fmtInt}
      />
    </div>
  </div>

  <!-- Group-by charts -->
  <div class="grid grid-cols-1 lg:grid-cols-2 gap-3 mb-4">
    <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
      <div class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-3">
        Tokens by provider
      </div>
      <DonutChart slices={providerSlices} formatValue={fmtNum} />
    </div>

    <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
      <div class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-3">
        Top models (by tokens)
      </div>
      <HorizontalBarChart rows={modelRows} formatValue={fmtNum} />
    </div>

    <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
      <div class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-3">
        Top agents (by requests)
      </div>
      <HorizontalBarChart rows={agentRows} formatValue={fmtInt} />
    </div>

    <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
      <div class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-3">
        Top organizations (by tokens)
      </div>
      <HorizontalBarChart rows={orgRows} formatValue={fmtNum} />
    </div>

    <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface lg:col-span-2">
      <div class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-3">
        Top billing codes (by tokens)
      </div>
      <HorizontalBarChart rows={billingRows} formatValue={fmtNum} />
    </div>
  </div>

  <!-- Error breakdown (by status) -->
  {#if byStatus.length > 0}
    <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface mb-4">
      <div class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-3">
        Requests by status
      </div>
      <table class="w-full text-xs">
        <thead class="text-left text-gray-500 dark:text-dark-text-muted">
          <tr>
            <th class="py-1.5 font-medium">Status</th>
            <th class="py-1.5 font-medium text-right">Requests</th>
            <th class="py-1.5 font-medium text-right">Tokens</th>
            <th class="py-1.5 font-medium text-right">Avg latency</th>
            <th class="py-1.5 font-medium text-right">Cost</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-border">
          {#each byStatus as s}
            <tr>
              <td class="py-1.5 font-mono" class:text-red-600={s.key === 'error'} class:dark:text-red-400={s.key === 'error'}>
                {s.key || 'ok'}
              </td>
              <td class="py-1.5 text-right tabular-nums">{fmtNum(s.request_count)}</td>
              <td class="py-1.5 text-right tabular-nums">{fmtNum(s.total_tokens)}</td>
              <td class="py-1.5 text-right tabular-nums">{fmtLatency(s.avg_latency_ms)}</td>
              <td class="py-1.5 text-right tabular-nums">{fmtCost(s.cost_cents)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}

  <!-- Budget utilization -->
  {#if budgets.length > 0}
    <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface mb-4">
      <div class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-3">
        Budget utilization
      </div>
      <div class="flex flex-col gap-2">
        {#each budgets as b}
          {@const pct = Math.min(100, b.usage_percent)}
          {@const over = b.usage_percent > 100}
          <div class="flex items-center gap-2 text-xs">
            <div class="w-40 truncate" title={b.agent_id}>
              <span class="font-medium text-gray-900 dark:text-dark-text">{b.agent_name || b.agent_id}</span>
            </div>
            <div class="flex-1 h-4 relative bg-gray-100 dark:bg-dark-elevated rounded-sm overflow-hidden">
              <div
                class="h-full transition-all"
                class:bg-blue-500={!over && pct < 80}
                class:bg-yellow-500={!over && pct >= 80}
                class:bg-red-500={over}
                style="width: {pct}%"
              ></div>
            </div>
            <div class="w-28 text-right font-mono tabular-nums" class:text-red-600={over} class:dark:text-red-400={over}>
              ${b.current_spend.toFixed(2)} / ${b.monthly_limit.toFixed(2)}
            </div>
            <div class="w-12 text-right font-mono text-gray-500 dark:text-dark-text-muted">
              {fmtPct(b.usage_percent)}
            </div>
          </div>
        {/each}
      </div>
    </div>
  {/if}

  {#if !loading && summary && summary.request_count === 0}
    <div class="p-8 text-center text-sm text-gray-400 dark:text-dark-text-muted border border-dashed border-gray-300 dark:border-dark-border">
      No usage data in this range. Usage is recorded on every LLM call through the gateway or via
      agent/workflow execution.
    </div>
  {/if}
</div>
