<script lang="ts">
  import CodeExpander from './CodeExpander.svelte';

  let { data }: { data: Record<string, any> } = $props();
</script>

<div>
  <CodeExpander
    bind:value={data.template}
    label="Template"
    language="go-template"
    rows={4}
    placeholder={'Hello \x7B\x7B.name\x7D\x7D, ...'}
  />
</div>
<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Variables (comma separated)</span>
  <input
    type="text"
    value={data.variables?.join(', ') || ''}
    oninput={(e) => { data.variables = (e.target as HTMLInputElement).value.split(',').map((s: string) => s.trim()).filter(Boolean); }}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
    placeholder="name, topic"
  /></label>
</div>
<!-- Port descriptions -->
<div class="border-t border-dark-border pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Upstream data used as the template context. If 'data' is the only input and is a map, its fields are promoted to top level.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">data</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Template context data (fields promoted to top level)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">map</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="The rendered template string output.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">text</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Rendered template string</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"{ text: string }"}</div>
      </div>
    </div>
  </div>
</div>
