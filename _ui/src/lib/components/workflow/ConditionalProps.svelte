<script lang="ts">
  let { data }: { data: Record<string, any> } = $props();
</script>

<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Expression (JS)</span>
  <textarea
    bind:value={data.expression}
    rows={3}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle font-mono focus:outline-none focus:ring-1 focus:ring-dark-border-subtle resize-y"
    placeholder="data.score > 0.8"
  ></textarea></label>
  <div class="mt-0.5 text-[10px] text-dark-text-muted">JS expression that evaluates to true/false</div>
</div>
<!-- Port descriptions -->
<div class="border-t border-dark-border pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Upstream data exposed as 'data' variable in the JS expression.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">data</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Available as 'data' in JS expression</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">any</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Activated when the expression evaluates to true. All input data passed through plus 'result' field.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">true</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Expression evaluated to true</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"{ ...inputs, result: boolean }"}</div>
      </div>
      <div title="Activated when the expression evaluates to false. All input data passed through plus 'result' field.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">false</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Expression evaluated to false</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"{ ...inputs, result: boolean }"}</div>
      </div>
    </div>
  </div>
</div>
