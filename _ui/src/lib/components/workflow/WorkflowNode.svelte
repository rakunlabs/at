<script lang="ts" module>
  export interface WorkflowNodePort {
    id: string;
    label?: string;
    /** Kaykay port type; drives handle colour and connection checks. Defaults to `data`. */
    port?: string;
    /** Accepted port types. Defaults to data + text; pass `undefined` explicitly to accept anything. */
    accept?: string[];
    /** Rarely-used port: rendered muted until something is connected to it. */
    optional?: boolean;
  }

  export interface WorkflowNodeField {
    label: string;
    value: string | number | null | undefined;
    mono?: boolean;
  }
</script>

<script lang="ts">
  import type { Snippet } from 'svelte';
  import { Handle, getFlow } from 'kaykay';
  import { ChevronDown, ChevronRight, SquareCheck, SquareX, Loader, Pin, TriangleAlert } from 'lucide-svelte';
  import SquareAlert from '@/lib/components/icons/SquareAlert.svelte';
  import { workflowRun } from '@/lib/store/workflow-run.svelte';
  import { getWorkflowNodeDefinition } from '@/lib/workflow/node-definitions';
  import { getWorkflowNodeAppearance, workflowKindStripe, workflowKindTile } from '@/lib/workflow/node-appearance';
  import { getWorkflowNodeActions } from '@/lib/workflow/node-actions';

  // Shared card for every workflow step: header (icon, name, type), port rows
  // with the connection points beside their names, a short configuration
  // summary and the last run's result.
  interface Props {
    id: string;
    type: string;
    data: { label?: string; node_number?: number; execution?: unknown };
    selected?: boolean;
    inputs?: WorkflowNodePort[];
    outputs?: WorkflowNodePort[];
    fields?: WorkflowNodeField[];
    /** Code / expression / template preview. */
    code?: string;
    tags?: string[];
    /** Shown as a setup prompt when set; pass it only while the step is unconfigured. */
    setup?: string;
    /** Neutral description shown when there is nothing else to summarise. */
    empty?: string;
    children?: Snippet;
    /** Additional handles outside the port rows (config handles on top/bottom). */
    extra?: Snippet;
  }

  let {
    id, type, data, selected = false, inputs = [], outputs = [], fields = [], code = '', tags = [],
    setup = '', empty = '', children, extra,
  }: Props = $props();

  const flow = getFlow();
  const actions = getWorkflowNodeActions();
  let collapsed = $derived(actions?.isCollapsed(id) ?? false);
  let definition = $derived(getWorkflowNodeDefinition(type));
  let appearance = $derived(getWorkflowNodeAppearance(type));
  let Icon = $derived(appearance.icon);
  let title = $derived(data.label?.trim() || definition?.label || type);
  let subtitle = $derived(data.label?.trim() && data.label.trim() !== definition?.label ? definition?.label : definition?.description);
  let shownFields = $derived(fields.filter(field => field.value !== undefined && field.value !== null && field.value !== ''));
  let rows = $derived(Math.max(inputs.length, outputs.length));
  let connected = $derived.by(() => {
    const ids = new Set<string>();
    for (const edge of flow?.edges ?? []) {
      if (edge.target === id && edge.target_handle) ids.add(`in:${edge.target_handle}`);
      if (edge.source === id && edge.source_handle) ids.add(`out:${edge.source_handle}`);
    }
    return ids;
  });

  let run = $derived(workflowRun.nodeRunStates[id]);
  let errorOutput = $derived((data.execution as { on_error?: string } | undefined)?.on_error === 'error_output');
  let configNode = $derived(type.endsWith('_config'));

  function duration(ms?: number): string {
    if (ms == null) return '';
    return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`;
  }

  function muted(port: WorkflowNodePort, direction: 'in' | 'out'): boolean {
    return !!port.optional && !connected.has(`${direction}:${port.id}`);
  }
</script>

<div
  class={[
    'workflow-node-card',
    workflowKindStripe[appearance.kind],
    selected && 'workflow-node-selected',
    run?.status === 'error' && !run.error_policy && 'workflow-node-failed',
  ]}
>
  <div class="flex items-start gap-2.5 px-3 pt-3 pb-2.5">
    <span class={['flex size-8 shrink-0 items-center justify-center', workflowKindTile[appearance.kind]]}>
      <Icon size={16} strokeWidth={2.25} />
    </span>
    <div class="min-w-0 flex-1">
      <div class="truncate text-sm font-semibold leading-5 text-dark-text" title={title}>{title}</div>
      {#if subtitle}
        <div class="truncate text-xs leading-4 text-dark-text-muted" title={subtitle}>{subtitle}</div>
      {/if}
    </div>
    <div class="flex shrink-0 items-center gap-1.5 pt-0.5">
      {#if run?.status === 'running'}
        <Loader size={14} class="text-blue-400" aria-label="Running" />
      {:else if run?.status === 'completed'}
        {#if run.pinned}<Pin size={14} class="text-blue-400" aria-label="Pinned output" />{:else}<SquareCheck size={14} class="text-green-400" aria-label="Completed" />{/if}
      {:else if run?.status === 'error'}
        {#if run.error_policy}<TriangleAlert size={14} class="text-amber-600" aria-label="Handled failure" />{:else}<SquareX size={14} class="text-red-400" aria-label="Failed" />{/if}
      {/if}
      {#if data.node_number != null}
        <span class="text-[11px] tabular-nums text-dark-text-faint">#{data.node_number}</span>
      {/if}
      {#if actions && (rows > 0 || setup || shownFields.length || code || tags.length || children || empty)}
        <button
          type="button"
          onclick={() => actions.toggleCollapsed(id)}
          aria-expanded={!collapsed}
          aria-label={collapsed ? `Expand ${title}` : `Collapse ${title}`}
          title={collapsed ? 'Expand' : 'Collapse'}
          class="-mr-1 flex size-5 items-center justify-center text-dark-text-faint hover:bg-dark-elevated hover:text-dark-text"
        >
          {#if collapsed}<ChevronRight size={14} />{:else}<ChevronDown size={14} />{/if}
        </button>
      {/if}
    </div>
  </div>

  {#if collapsed}
    <!-- Handles stay mounted so connections keep their endpoints; they meet at
         the header's edges. Expand the step to connect a specific port. -->
    <div class="wf-collapsed-ports pointer-events-none absolute inset-x-0 top-0 h-[54px]">
      {#each inputs as input (input.id)}
        <div class="wf-port absolute inset-y-0 left-0 w-0">
          <Handle id={input.id} type="input" port={input.port ?? 'data'} accept={'accept' in input ? input.accept : ['data', 'text']} position="left" label={input.label ?? input.id} />
        </div>
      {/each}
      {#each outputs as output (output.id)}
        <div class="wf-port absolute inset-y-0 right-0 w-0">
          <Handle id={output.id} type="output" port={output.port ?? 'data'} position="right" label={output.label ?? output.id} />
        </div>
      {/each}
    </div>
  {:else if rows > 0}
    <div class="border-t py-1 border-dark-border">
      {#each Array(rows) as _, i}
        {@const input = inputs[i]}
        {@const output = outputs[i]}
        <div class="grid grid-cols-2 text-xs leading-6">
          <div class="wf-port relative min-w-0">
            {#if input}
              <Handle id={input.id} type="input" port={input.port ?? 'data'} accept={'accept' in input ? input.accept : ['data', 'text']} position="left" label={input.label ?? input.id} />
              <span class={['block truncate pl-3', muted(input, 'in') ? 'text-dark-text-faint' : 'text-dark-text-secondary']}>{input.label ?? input.id}</span>
            {/if}
          </div>
          <div class="wf-port relative min-w-0">
            {#if output}
              <Handle id={output.id} type="output" port={output.port ?? 'data'} position="right" label={output.label ?? output.id} />
              <span class={['block truncate pr-3 text-right', muted(output, 'out') ? 'text-dark-text-faint' : 'text-dark-text-secondary']}>{output.label ?? output.id}</span>
            {/if}
          </div>
        </div>
      {/each}
    </div>
  {/if}

  {#if !collapsed && (setup || shownFields.length || code || tags.length || children || empty)}
    <div class="space-y-2 border-t px-3 py-2.5 border-dark-border">
      {#if setup}
        <div class="flex items-center gap-1.5 border px-2 py-1 text-xs border-amber-900 bg-amber-950/40 text-amber-300">
          <SquareAlert size={12} class="shrink-0" />{setup}
        </div>
      {/if}
      {#if shownFields.length}
        <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs">
          {#each shownFields as field (field.label)}
            <dt class="text-dark-text-muted">{field.label}</dt>
            <dd class={['truncate text-dark-text', field.mono && 'font-mono']} title={String(field.value)}>{field.value}</dd>
          {/each}
        </dl>
      {/if}
      {#if code}
        <pre class="line-clamp-3 overflow-hidden whitespace-pre-wrap break-all border px-2 py-1.5 font-mono text-[11px] leading-4 border-dark-border bg-dark-base text-dark-text-secondary">{code}</pre>
      {/if}
      {#if tags.length}
        <div class="flex flex-wrap gap-1">
          {#each tags as tag}
            <span class="border px-1.5 py-px font-mono text-[11px] border-dark-border bg-dark-elevated text-dark-text-secondary">{tag}</span>
          {/each}
        </div>
      {/if}
      {@render children?.()}
      {#if empty && !setup && !shownFields.length && !code && !tags.length && !children}
        <p class="text-xs text-dark-text-muted">{empty}</p>
      {/if}
    </div>
  {/if}

  {#if run && run.status !== 'idle'}
    <div class={[
      'flex items-center gap-1.5 border-t px-3 py-1.5 text-[11px]',
      run.status === 'running' && 'border-blue-950 bg-blue-950/40 text-blue-300',
      run.status === 'completed' && 'border-green-950 bg-green-950/40 text-green-300',
      run.status === 'error' && (run.error_policy
        ? 'border-amber-950 bg-amber-950/40 text-amber-300'
        : 'border-red-950 bg-red-950/40 text-red-300'),
    ]}>
      <span class="min-w-0 flex-1 truncate font-medium" title={run.status === 'error' ? run.error : undefined}>
        {#if run.status === 'running'}
          {run.retry_delay_ms != null ? `Retrying in ${duration(run.retry_delay_ms)}` : 'Running'}{(run.max_attempts ?? 1) > 1 ? ` · attempt ${run.attempt ?? 1}/${run.max_attempts}` : ''}
        {:else if run.status === 'completed'}
          {run.pinned ? 'Pinned output' : run.skipped ? 'Skipped' : 'Succeeded'}{(run.invocations ?? 1) > 1 ? ` · ${run.invocations} runs` : ''}
        {:else if run.status === 'error'}
          {run.error_policy ? 'Failure handled' : run.error?.split('\n')[0] || 'Failed'}
        {/if}
      </span>
      {#if run.status !== 'running' && run.duration_ms != null}
        <span class="shrink-0 tabular-nums opacity-80">{duration(run.duration_ms)}</span>
      {/if}
    </div>
  {/if}

  {#if errorOutput}
    <Handle id="__error" type="output" port="data" position={configNode ? 'bottom' : 'top'} label="failure" />
  {/if}
  {@render extra?.()}
</div>
