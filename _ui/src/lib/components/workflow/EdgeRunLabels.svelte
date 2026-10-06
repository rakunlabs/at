<script lang="ts">
  import { ViewportPortal, getFlow } from 'kaykay';
  import { workflowRun } from '@/lib/store/workflow-run.svelte';
  import { edgeRunLabel } from '@/lib/workflow/edge-runs';

  // n8n-style "3 items" on each connection the last run used. Drawn as an
  // overlay rather than through FlowEdge.label, so run results never enter the
  // graph, its undo history or the labels a person set on an edge.
  const flow = getFlow();

  let labels = $derived.by(() => {
    const items: { id: string; x: number; y: number; text: string; failure: boolean }[] = [];
    for (const edge of flow.edges) {
      const text = edgeRunLabel(edge, workflowRun.nodeRunStates[edge.source], workflowRun.nodeRunStates[edge.target]);
      if (!text) continue;
      const waypoints = edge.waypoints ?? [];
      let point = waypoints.length ? waypoints[Math.floor(waypoints.length / 2)] : undefined;
      if (!point) {
        const source = flow.getHandlePosition(edge.source, edge.source_handle);
        const target = flow.getHandlePosition(edge.target, edge.target_handle);
        if (!source || !target) continue;
        // The midpoint of the endpoints lies on a symmetric bezier at t = 0.5.
        point = { x: (source.x + target.x) / 2, y: (source.y + target.y) / 2 };
      }
      items.push({ id: edge.id, x: point.x, y: point.y, text, failure: edge.source_handle === '__error' });
    }
    return items;
  });
</script>

{#if labels.length}
  <ViewportPortal>
    {#each labels as label (label.id)}
      <span
        class={[
          'absolute -translate-x-1/2 -translate-y-1/2 whitespace-nowrap border px-1.5 text-[11px] leading-[18px] tabular-nums',
          label.failure
            ? 'border-red-900 bg-red-950 text-red-300'
            : 'border-green-900 bg-dark-surface text-green-400',
        ]}
        style:left="{label.x}px"
        style:top="{label.y}px"
      >{label.text}</span>
    {/each}
  </ViewportPortal>
{/if}
