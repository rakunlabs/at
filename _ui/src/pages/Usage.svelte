<script lang="ts">
  import { onDestroy } from 'svelte';
  import { push } from 'svelte-spa-router';
  import LoadIssues from '@/lib/components/LoadIssues.svelte';
  import { createPageLoader } from '@/lib/helper/page-load.svelte';
  const pageLoad = createPageLoader();
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
    Download,
    ExternalLink,
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
  let userIds = $state<string[]>([]);
  let sources = $state<string[]>([]);
  let billingCodes = $state<string[]>([]);
  let status = $state('');

  let bucket = $state<Bucket>('day');

  let summary = $state<UsageSummary | null>(null);
  let previousSummary = $state<UsageSummary | null>(null);
  let timeseries = $state<UsageTimeSeriesPoint[]>([]);
  let byProvider = $state<UsageSummary[]>([]);
  let byModel = $state<UsageSummary[]>([]);
  let byAgent = $state<UsageSummary[]>([]);
  let byOrg = $state<UsageSummary[]>([]);
  let byBillingCode = $state<UsageSummary[]>([]);
  let byStatus = $state<UsageSummary[]>([]);
  let byErrorCode = $state<UsageSummary[]>([]);
  let byUser = $state<UsageSummary[]>([]);
  let bySource = $state<UsageSummary[]>([]);
  let availableUsers = $state<Array<{ value: string; label: string }>>([]);
  const sourceOptions = [
    { value: 'chats', label: 'Chats' },
    { value: 'sessions', label: 'Sessions' },
    { value: 'assistant', label: 'AI assistants' },
    { value: 'gateway', label: 'API gateway' },
    { value: '', label: 'Other / historical' },
  ];
  const userLabel = (row: UsageSummary) => row.label || row.key || 'Unattributed / system';
  const sourceLabel = (row: UsageSummary) => sourceOptions.find(option => option.value === (row.key || ''))?.label || row.key;
  let budgets = $state<BudgetUtilization[]>([]);

  let availableProviders = $state<string[]>([]);
  let availableModels = $state<string[]>([]);
  let availableAgents = $state<Array<{ value: string; label: string }>>([]);
  let availableOrgs = $state<Array<{ value: string; label: string }>>([]);
  // Agent/org name lookup for pretty-printing the top-N tables.
  let agentNameById = $state<Record<string, string>>({});
  let orgNameById = $state<Record<string, string>>({});

  let loading = $state(false);
  let loadGeneration = 0;
  let filterTimer: ReturnType<typeof setTimeout> | undefined;

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
    user_id: userIds.length ? userIds : undefined,
    source: sources.length ? sources : undefined,
    billing_code: billingCodes.length ? billingCodes : undefined,
    status: status || undefined,
  });

  function previousPeriodFilter(): UsageFilter {
    const current = filter();
    const start = new Date(from).getTime();
    const end = new Date(to).getTime();
    if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) return current;
    return {
      ...current,
      from: new Date(start - (end - start)).toISOString(),
      to: new Date(start).toISOString(),
    };
  }

  async function loadAll() {
    const generation = ++loadGeneration;
    loading = true;
    pageLoad.reset();
    try {
      await Promise.all([
        pageLoad.load('Usage summary', () => getUsageSummary(filter()), value => { summary = value; }),
        pageLoad.load('Previous period', () => getUsageSummary(previousPeriodFilter()), value => { previousSummary = value; }),
        pageLoad.load('Usage timeline', () => getUsageTimeSeries(filter(), bucket), value => { timeseries = value; }),
        pageLoad.load('Provider usage', () => getUsageGrouped(filter(), 'provider'), value => { byProvider = value; }),
        pageLoad.load('Model usage', () => getUsageGrouped(filter(), 'model'), value => { byModel = value; }),
        pageLoad.load('Agent usage', () => getUsageGrouped(filter(), 'agent'), value => { byAgent = value; }),
        pageLoad.load('Organization usage', () => getUsageGrouped(filter(), 'org'), value => { byOrg = value; }),
        pageLoad.load('Billing code usage', () => getUsageGrouped(filter(), 'billing_code'), value => { byBillingCode = value; }),
        pageLoad.load('Status usage', () => getUsageGrouped(filter(), 'status'), value => { byStatus = value; }),
        pageLoad.load('Error reasons', () => status === 'ok' ? Promise.resolve([]) : getUsageGrouped({ ...filter(), status: 'error' }, 'error_code'), value => { byErrorCode = value; }),
        pageLoad.load('User usage', () => getUsageGrouped(filter(), 'user'), value => { byUser = value; }),
        pageLoad.load('Source usage', () => getUsageGrouped(filter(), 'source'), value => { bySource = value; }),
        pageLoad.load('User filter', () => getUsageGrouped({ from, to }, 'user'), value => {
          availableUsers = value.map(row => ({ value: row.key || '', label: userLabel(row) }));
        }),
        pageLoad.load('Budgets', getBudgetUtilization, value => { budgets = value; }),
      ]);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load usage data', 'alert');
    } finally {
      if (generation === loadGeneration) loading = false;
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
    clearTimeout(filterTimer);
    filterTimer = setTimeout(loadAll, 200);
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
  onDestroy(() => clearTimeout(filterTimer));

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
  const timeseriesP95Latency = $derived(
    timeseries.map((p) => ({ x: new Date(p.bucket), y: Math.round(p.p95_latency_ms) })),
  );
  const timeseriesCost = $derived(
    timeseries.map((p) => ({ x: new Date(p.bucket), y: p.cost_cents })),
  );

  const providerSlices = $derived(
    [...byProvider].sort((a, b) => b.total_tokens - a.total_tokens).map((r, i) => ({
      label: r.key || '(none)',
      value: r.total_tokens,
      color: colorFor(i),
    })),
  );

  const modelRows = $derived(
    [...byModel].sort((a, b) => b.total_tokens - a.total_tokens).map((r, i) => ({
      label: r.key || '(none)',
      value: r.total_tokens,
      color: colorFor(i),
    })),
  );

  const agentRows = $derived(
    [...byAgent].sort((a, b) => b.request_count - a.request_count).map((r, i) => ({
      // Prefer the agent's human name if we have it; fall back to a short ID.
      label: r.key ? (agentNameById[r.key] || r.key.slice(0, 14)) : '(none)',
      value: r.request_count,
      color: colorFor(i),
    })),
  );

  const orgRows = $derived(
    [...byOrg].sort((a, b) => b.total_tokens - a.total_tokens).map((r, i) => ({
      label: r.key ? (orgNameById[r.key] || r.key.slice(0, 14)) : '(none)',
      value: r.total_tokens,
      color: colorFor(i),
    })),
  );

  const billingRows = $derived(
    [...byBillingCode].sort((a, b) => b.total_tokens - a.total_tokens).map((r, i) => ({
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
  function inspectTraces(extra: Record<string, string> = {}) {
    const params = new URLSearchParams({ view: 'calls', type: 'generation', from, to, ...extra });
    if (providers.length === 1 && !params.has('provider')) params.set('provider', providers[0]);
    if (models.length === 1 && !params.has('model')) params.set('model', models[0]);
    if (sources.length === 1 && !params.has('source')) params.set('source', sources[0]);
    if (status && !params.has('status')) params.set('status', status);
    push(`/llm-calls?${params.toString()}`);
  }
  function exportCSV() {
    const groups: Array<[string, UsageSummary[]]> = [
      ['provider', byProvider], ['model', byModel], ['agent', byAgent], ['organization', byOrg],
      ['billing_code', byBillingCode], ['status', byStatus], ['error_code', byErrorCode], ['user', byUser], ['source', bySource],
    ];
    const header = ['dimension', 'key', 'label', 'calls', 'priced_calls', 'input_tokens', 'output_tokens', 'cache_read_tokens', 'cache_write_tokens', 'total_tokens', 'known_cost_cents', 'errors', 'avg_latency_ms', 'p50_latency_ms', 'p95_latency_ms', 'p99_latency_ms'];
    const quote = (value: unknown) => `"${String(value ?? '').replaceAll('"', '""')}"`;
    const rows = groups.flatMap(([dimension, values]) => values.map(row => [
      dimension, row.key || '', row.label || '', row.request_count, row.priced_request_count,
      row.input_tokens, row.output_tokens, row.cache_read_tokens, row.cache_write_tokens, row.total_tokens,
      row.cost_cents, row.error_count, row.avg_latency_ms, row.p50_latency_ms, row.p95_latency_ms, row.p99_latency_ms,
    ]));
    const blob = new Blob([[header, ...rows].map(row => row.map(quote).join(',')).join('\n')], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = `at-usage-${from.slice(0, 10)}-${to.slice(0, 10)}.csv`;
    document.body.appendChild(link);
    link.click();
    link.remove();
    URL.revokeObjectURL(url);
  }
  function projectedBudgetSpend(budget: BudgetUtilization): number | null {
    if (!budget.period_start || !budget.period_end || budget.request_count !== budget.priced_request_count) return null;
    const start = new Date(budget.period_start).getTime();
    const end = new Date(budget.period_end).getTime();
    const elapsed = Date.now() - start;
    const duration = end - start;
    if (duration <= 0 || elapsed <= 0) return null;
    return budget.current_spend / Math.min(1, elapsed / duration);
  }

  const errorRate = $derived(
    summary && summary.request_count > 0
      ? (summary.error_count / summary.request_count) * 100
      : 0,
  );
  const pricingCoverage = $derived(
    summary && summary.request_count > 0
      ? (summary.priced_request_count / summary.request_count) * 100
      : 0,
  );
  const primaryUsage = $derived.by(() => {
    if (!summary) return { label: 'Usage', value: '—', detail: '' };
    if (summary.request_count > 0 && summary.priced_request_count === summary.request_count) {
      return { label: 'Cost', value: fmtCost(summary.cost_cents), detail: `${fmtNum(summary.total_tokens)} tokens` };
    }
    if (summary.total_tokens > 0) {
      return {
        label: 'Tokens',
        value: fmtNum(summary.total_tokens),
        detail: summary.priced_request_count > 0
          ? `${fmtCost(summary.cost_cents)} known cost · ${fmtPct(pricingCoverage)} priced`
          : 'Pricing unavailable · using token count',
      };
    }
    return { label: 'Calls', value: fmtNum(summary.request_count), detail: 'Pricing and token usage unavailable' };
  });
  const cacheHitRate = $derived.by(() => {
    if (!summary) return 0;
    const input = summary.input_tokens + summary.cache_read_tokens + summary.cache_write_tokens;
    return input > 0 ? (summary.cache_read_tokens / input) * 100 : 0;
  });
  const primaryTrend = $derived.by(() => {
    if (!summary || !previousSummary) return '';
    let current = summary.request_count;
    let previous = previousSummary.request_count;
    if (primaryUsage.label === 'Cost') {
      if (previousSummary.priced_request_count !== previousSummary.request_count) return '';
      current = summary.cost_cents;
      previous = previousSummary.cost_cents;
    } else if (primaryUsage.label === 'Tokens') {
      current = summary.total_tokens;
      previous = previousSummary.total_tokens;
    }
    if (previous <= 0) return '';
    const delta = ((current - previous) / previous) * 100;
    return `${delta >= 0 ? '+' : ''}${delta.toFixed(1)}% vs previous period`;
  });
  const rankingMetric = $derived(
    summary?.request_count && summary.priced_request_count === summary.request_count
      ? 'cost'
      : summary?.total_tokens ? 'tokens' : 'calls',
  );
  const rankedUsers = $derived(
    [...byUser].sort((a, b) => {
      if (rankingMetric === 'cost') return b.cost_cents - a.cost_cents;
      if (rankingMetric === 'tokens') return b.total_tokens - a.total_tokens;
      return b.request_count - a.request_count;
    }).slice(0, 20),
  );
  const providerOptions = $derived([...new Set([...availableProviders, ...byProvider.map(row => row.key || '').filter(Boolean)])].sort());
  const modelOptions = $derived([...new Set([...availableModels, ...byModel.map(row => row.key || '').filter(Boolean)])].sort());
  const agentOptions = $derived.by(() => {
    const options = new Map(availableAgents.map(option => [option.value, option]));
    for (const row of byAgent) if (row.key && !options.has(row.key)) options.set(row.key, { value: row.key, label: row.key.slice(0, 14) });
    return [...options.values()].sort((a, b) => a.label.localeCompare(b.label));
  });
  const orgOptions = $derived.by(() => {
    const options = new Map(availableOrgs.map(option => [option.value, option]));
    for (const row of byOrg) if (row.key && !options.has(row.key)) options.set(row.key, { value: row.key, label: row.key.slice(0, 14) });
    return [...options.values()].sort((a, b) => a.label.localeCompare(b.label));
  });
  const billingCodeOptions = $derived([...new Set(byBillingCode.map(row => row.key || '').filter(Boolean))].sort());
</script>

<svelte:head>
  <title>AT | Usage</title>
</svelte:head>

<div class="p-6 max-w-7xl mx-auto">
  <LoadIssues issues={pageLoad.issues} retry={loadAll} {loading} />
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
    <div class="flex items-center gap-1">
      <button onclick={exportCSV} disabled={loading || !summary?.request_count} class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary disabled:opacity-50" title="Download usage CSV" aria-label="Download filtered usage as CSV"><Download size={14} /></button>
      <button
        onclick={refresh}
        disabled={loading}
        class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary disabled:opacity-50"
        title="Refresh"
      >
        <RefreshCw size={14} class={loading ? 'animate-spin' : ''} />
      </button>
    </div>
  </div>

  <!-- Filters -->
  <div class="flex flex-wrap items-center gap-2 mb-4 pb-3 border-b border-gray-200 dark:border-dark-border">
    <DateRangePicker bind:from bind:to bind:preset onchange={handleRangeChange} />
    <MultiSelect label="User" options={availableUsers} bind:selected={userIds} onchange={handleFilterChange} />
    <MultiSelect label="Source" options={sourceOptions} bind:selected={sources} onchange={handleFilterChange} />
    <MultiSelect
      label="Provider"
      options={providerOptions}
      bind:selected={providers}
      onchange={handleFilterChange}
    />
    <MultiSelect
      label="Model"
      options={modelOptions}
      bind:selected={models}
      onchange={handleFilterChange}
    />
    <MultiSelect
      label="Agent"
      options={agentOptions}
      bind:selected={agentIds}
      onchange={handleFilterChange}
    />
    <MultiSelect
      label="Organization"
      options={orgOptions}
      bind:selected={orgIds}
      onchange={handleFilterChange}
    />
    <MultiSelect label="Billing code" options={billingCodeOptions} bind:selected={billingCodes} onchange={handleFilterChange} />
    <select bind:value={status} onchange={handleFilterChange} aria-label="Status" class="border border-gray-300 bg-white px-2 py-1 text-xs text-gray-700 dark:border-dark-border-subtle dark:bg-dark-surface dark:text-dark-text-secondary">
      <option value="">All statuses</option>
      <option value="ok">Successful</option>
      <option value="error">Errors</option>
    </select>
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
    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-3 mb-4">
      <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
        <div class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-text-muted mb-1">
          <Zap size={12} /> {primaryUsage.label}
        </div>
        <div class="text-xl font-semibold text-gray-900 dark:text-dark-text tabular-nums">
          {primaryUsage.value}
        </div>
        <div class="text-[11px] text-gray-400 dark:text-dark-text-muted mt-0.5">
          {primaryUsage.detail}
          {#if primaryTrend}<span class="block">{primaryTrend}</span>{/if}
        </div>
      </div>

      <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
        <div class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-text-muted mb-1">
          <Activity size={12} /> Calls
        </div>
        <div class="text-xl font-semibold text-gray-900 dark:text-dark-text tabular-nums">
          {fmtNum(summary.request_count)}
        </div>
        <div class="text-[11px] text-gray-400 dark:text-dark-text-muted mt-0.5">
          {fmtNum(summary.error_count)} failed · {fmtPct(errorRate)} error rate
        </div>
      </div>

      <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
        <div class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-text-muted mb-1">
          <AlertCircle size={12} /> Cost coverage
        </div>
        <div
          class="text-xl font-semibold tabular-nums"
          class:text-amber-700={pricingCoverage < 100}
          class:text-gray-900={pricingCoverage === 100}
          class:dark:text-amber-400={pricingCoverage < 100}
          class:dark:text-dark-text={pricingCoverage === 100}
        >
          {summary.request_count ? fmtPct(pricingCoverage) : '—'}
        </div>
        <div class="text-[11px] text-gray-400 dark:text-dark-text-muted mt-0.5">
          {fmtNum(summary.priced_request_count)} of {fmtNum(summary.request_count)} calls · {fmtCost(summary.cost_cents)} known
        </div>
      </div>

      <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
        <div class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-text-muted mb-1">
          <Clock size={12} /> P95 latency
        </div>
        <div class="text-xl font-semibold text-gray-900 dark:text-dark-text tabular-nums">
          {fmtLatency(summary.p95_latency_ms)}
        </div>
        <div class="text-[11px] text-gray-400 dark:text-dark-text-muted mt-0.5">
          median {fmtLatency(summary.p50_latency_ms)} · p99 {fmtLatency(summary.p99_latency_ms)}
        </div>
      </div>

      <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
        <div class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-text-muted mb-1">
          <Zap size={12} /> Cache hit rate
        </div>
        <div class="text-xl font-semibold text-gray-900 dark:text-dark-text tabular-nums">
          {fmtPct(cacheHitRate)}
        </div>
        <div class="text-[11px] text-gray-400 dark:text-dark-text-muted mt-0.5">
          {fmtNum(summary.cache_read_tokens)} read · {fmtNum(summary.cache_write_tokens)} write
        </div>
      </div>
    </div>
  {/if}

  <p class="mb-3 text-xs text-gray-600 dark:text-dark-text-secondary">User attribution starts with this update. Older calls and system activity appear as unattributed. LLM time is the sum of model-call durations, not time spent on the page.</p>
  {#each [{ title: 'User usage', rows: rankedUsers, users: true }, { title: 'Source usage', rows: bySource, users: false }] as section}
    <section class="mb-4 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
      <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
        <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text">{section.title}{section.users ? ` · Top 20 by ${rankingMetric}` : ''}</h3>
        {#if section.users}<p class="mt-1 text-xs text-gray-600 dark:text-dark-text-secondary">Select a user to filter all charts and see their Chats / Sessions breakdown below.</p>{/if}
      </div>
      <div class="overflow-x-auto">
        <table class="w-full text-xs text-left">
          <thead class="text-gray-600 dark:text-dark-text-secondary border-b border-gray-200 dark:border-dark-border">
            <tr><th class="px-4 py-2">{section.users ? 'User' : 'Source'}</th><th class="px-3 py-2 text-right">Calls</th><th class="px-3 py-2 text-right">Input</th><th class="px-3 py-2 text-right">Output</th><th class="px-3 py-2 text-right">Cache read / write</th><th class="px-3 py-2 text-right">{pricingCoverage === 100 ? 'Cost' : 'Known cost'}</th><th class="px-3 py-2 text-right">LLM time</th><th class="px-3 py-2 text-right">Errors</th></tr>
          </thead>
          <tbody class="text-gray-800 dark:text-dark-text tabular-nums">
            {#each section.rows as row}
              <tr class="border-b last:border-b-0 border-gray-100 dark:border-dark-border">
                <td class="px-4 py-2">
                  {#if section.users}
                    <button class="max-w-64 truncate text-left underline underline-offset-2 hover:text-purple-700 dark:hover:text-purple-300 focus-visible:outline-2 focus-visible:outline-accent" title={row.key || 'No recorded user'} onclick={() => { userIds = [row.key || '']; loadAll(); }}>{userLabel(row)}</button>
                  {:else}{sourceLabel(row)}{/if}
                </td>
                <td class="px-3 py-2 text-right">{fmtNum(row.request_count)}</td>
                <td class="px-3 py-2 text-right">{fmtNum(row.input_tokens)}</td>
                <td class="px-3 py-2 text-right">{fmtNum(row.output_tokens)}</td>
                <td class="px-3 py-2 text-right whitespace-nowrap">{fmtNum(row.cache_read_tokens)} / {fmtNum(row.cache_write_tokens)}</td>
                <td class="px-3 py-2 text-right">{fmtCost(row.cost_cents)}</td>
                <td class="px-3 py-2 text-right">{fmtLatency(row.total_latency_ms)}</td>
                <td class="px-3 py-2 text-right">{fmtNum(row.error_count)}</td>
              </tr>
            {:else}
              <tr><td colspan="8" class="px-4 py-6 text-gray-600 dark:text-dark-text-secondary">{pageLoad.loading(section.title) ? 'Loading usage…' : pageLoad.error(section.title) || 'No usage in this date range for the selected filters.'}</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    </section>
  {/each}

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

    <div class={['p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface', !summary?.priced_request_count ? 'lg:col-span-2' : '']}>
      <div class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-2">
        Latency over time
      </div>
      <LineChart
        series={[
          { name: 'Average', color: '#9333ea', values: timeseriesLatency },
          { name: 'P95', color: '#dc2626', values: timeseriesP95Latency },
        ]}
        formatY={fmtInt}
      />
    </div>

    {#if summary?.priced_request_count}
      <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
        <div class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-2">
          {pricingCoverage === 100 ? 'Cost over time' : 'Known cost over time'}
        </div>
        <LineChart
          series={[{ name: 'Cost', color: '#0891b2', values: timeseriesCost }]}
          formatY={fmtCost}
        />
      </div>
    {/if}
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

  {#if byModel.length > 0}
    <section class="mb-4 border border-gray-200 bg-white dark:border-dark-border dark:bg-dark-surface">
      <div class="border-b border-gray-200 bg-gray-50 px-4 py-3 dark:border-dark-border dark:bg-dark-base">
        <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text">Model efficiency</h3>
        <p class="mt-1 text-xs text-gray-600 dark:text-dark-text-secondary">Cost efficiency is shown only when every call in that model row has calculable usage and pricing.</p>
      </div>
      <div class="overflow-x-auto">
        <table class="w-full text-left text-xs">
          <thead class="border-b border-gray-200 text-gray-600 dark:border-dark-border dark:text-dark-text-secondary">
            <tr>
              <th class="px-4 py-2">Model</th>
              <th class="px-3 py-2 text-right">Calls</th>
              <th class="px-3 py-2 text-right">Tokens</th>
              <th class="px-3 py-2 text-right">Known cost</th>
              <th class="px-3 py-2 text-right">Cost / call</th>
              <th class="px-3 py-2 text-right">Error rate</th>
              <th class="px-3 py-2 text-right">P95 latency</th>
              <th class="w-10 px-3 py-2"><span class="sr-only">Inspect</span></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 text-gray-800 dark:divide-dark-border dark:text-dark-text">
            {#each [...byModel].sort((a, b) => b.total_tokens - a.total_tokens).slice(0, 20) as row}
              {@const fullyPriced = row.request_count > 0 && row.priced_request_count === row.request_count}
              <tr>
                <td class="max-w-72 truncate px-4 py-2 font-mono" title={row.key}>{row.key || '(none)'}</td>
                <td class="px-3 py-2 text-right tabular-nums">{fmtNum(row.request_count)}</td>
                <td class="px-3 py-2 text-right tabular-nums">{fmtNum(row.total_tokens)}</td>
                <td class="px-3 py-2 text-right tabular-nums">{fmtCost(row.cost_cents)}</td>
                <td class="px-3 py-2 text-right tabular-nums">{fullyPriced ? fmtCost(row.cost_cents / row.request_count) : '—'}</td>
                <td class="px-3 py-2 text-right tabular-nums">{row.request_count ? fmtPct((row.error_count / row.request_count) * 100) : '0.0%'}</td>
                <td class="px-3 py-2 text-right tabular-nums">{fmtLatency(row.p95_latency_ms)}</td>
                <td class="px-3 py-2 text-right"><button class="p-1 text-gray-500 hover:text-gray-900 dark:text-dark-text-muted dark:hover:text-dark-text" title={`Inspect ${row.key || 'model'} traces`} aria-label={`Inspect ${row.key || 'model'} traces`} onclick={() => inspectTraces({ model: row.key || '' })}><ExternalLink size={13} /></button></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </section>
  {/if}

  <!-- Error breakdown (by status) -->
  {#if byStatus.length > 0}
    <div class="grid grid-cols-1 lg:grid-cols-2 gap-3 mb-4">
    <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
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
            <th class="py-1.5 font-medium text-right">{pricingCoverage === 100 ? 'Cost' : 'Known cost'}</th>
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
    <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
      <div class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-3">Error reasons</div>
      <table class="w-full text-xs">
        <thead class="text-left text-gray-500 dark:text-dark-text-muted">
          <tr>
            <th class="py-1.5 font-medium">Reason</th>
            <th class="py-1.5 font-medium text-right">Calls</th>
            <th class="py-1.5 font-medium text-right">Share</th>
            <th class="w-10 py-1.5"><span class="sr-only">Inspect</span></th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-border">
          {#each byErrorCode as row}
            <tr>
              <td class="py-1.5 font-mono">{row.key || 'unknown'}</td>
              <td class="py-1.5 text-right tabular-nums">{fmtNum(row.request_count)}</td>
              <td class="py-1.5 text-right tabular-nums">{summary?.error_count ? fmtPct((row.request_count / summary.error_count) * 100) : '0.0%'}</td>
              <td class="py-1.5 text-right"><button class="p-1 text-gray-500 hover:text-gray-900 dark:text-dark-text-muted dark:hover:text-dark-text" title={`Inspect ${row.key || 'unknown'} errors`} aria-label={`Inspect ${row.key || 'unknown'} errors`} onclick={() => inspectTraces({ status: 'error', error_code: row.key || '' })}><ExternalLink size={13} /></button></td>
            </tr>
          {:else}
            <tr><td colspan="4" class="py-4 text-gray-500 dark:text-dark-text-muted">No errors in this range.</td></tr>
          {/each}
        </tbody>
      </table>
    </div>
    </div>
  {/if}

  <!-- Budget utilization -->
  {#if budgets.length > 0}
    <div class="p-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface mb-4">
      <div class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-3">
        Budget utilization
      </div>
      <p class="mb-3 text-[11px] text-gray-500 dark:text-dark-text-muted">Current agent budget periods. These values are independent of the dashboard date and attribution filters above.</p>
      <div class="flex flex-col gap-2">
        {#each budgets as b}
          {@const pct = Math.min(100, b.usage_percent)}
          {@const over = b.usage_percent > 100}
          {@const projected = projectedBudgetSpend(b)}
          {@const budgetComplete = b.request_count === b.priced_request_count}
          <div class="flex items-center gap-2 text-xs">
            <div class="w-52 min-w-0" title={b.agent_id}>
              <span class="font-medium text-gray-900 dark:text-dark-text block truncate">{b.agent_name || b.agent_id}</span>
              <span class="text-[9px] text-gray-400 dark:text-dark-text-muted block truncate">
                {b.budget_period}{b.period_end ? ` · resets ${new Date(b.period_end).toLocaleString(undefined, { timeZone: b.budget_timezone || 'UTC' })}` : ''}
              </span>
              <span class="text-[9px] text-gray-400 dark:text-dark-text-muted block truncate">
                {#if projected !== null}Projected ${projected.toFixed(2)} this period{:else if b.request_count > 0}Forecast unavailable · cost coverage incomplete{:else}No calls this period{/if}
              </span>
            </div>
            <div class="flex-1 h-4 relative bg-gray-100 dark:bg-dark-elevated rounded-sm overflow-hidden">
              <div
                class="h-full "
                class:bg-blue-500={budgetComplete && !over && pct < 80}
                class:bg-yellow-500={!over && (!budgetComplete || pct >= 80)}
                class:bg-red-500={over}
                style="width: {pct}%"
              ></div>
            </div>
            <div class="w-28 text-right font-mono tabular-nums" class:text-red-600={over} class:dark:text-red-400={over}>
              {budgetComplete ? '' : 'known '}${b.current_spend.toFixed(2)} / ${b.monthly_limit.toFixed(2)}
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
