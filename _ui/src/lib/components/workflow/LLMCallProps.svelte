<script lang="ts">
  let { data, providers = [] }: { data: Record<string, any>; providers?: any[] } = $props();

  let selectedProvider = $derived(providers.find(p => p.key === data.provider));
  let availableModels = $derived(
    selectedProvider?.config?.models?.length
      ? selectedProvider.config.models
      : selectedProvider?.config?.model
        ? [selectedProvider.config.model]
        : []
  );
</script>

<div>
  <label class="block">
    <span class="text-[10px] font-medium text-dark-text-muted uppercase tracking-wider">Provider</span>
  <select
    bind:value={data.provider}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
  >
    <option value="">Select provider</option>
    {#each providers as p}
      <option value={p.key}>{p.key}</option>
    {/each}
  </select></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-dark-text-muted uppercase tracking-wider">Model</span>
  <select
    bind:value={data.model}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
  >
    <option value="">Select model</option>
    {#each availableModels as m}
      <option value={m}>{m}</option>
    {/each}
  </select></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-dark-text-muted uppercase tracking-wider">System Prompt</span>
  <textarea
    bind:value={data.system_prompt}
    rows={3}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle resize-y"
    placeholder="System prompt (optional)"
  ></textarea></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-dark-text-muted uppercase tracking-wider">Output format</span>
  <select
    bind:value={data.output_format}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
  >
    <option value="text">Text</option>
    <option value="json">JSON object</option>
  </select></label>
  {#if data.output_format === 'json'}
    <div class="mt-0.5 text-[10px] text-dark-text-muted">The answer is parsed and sent on the <span class="font-mono">json</span> output, so Switch rules and templates can read its fields (<span class="font-mono">/severity</span>, <span class="font-mono">{'\x7B\x7B.summary\x7D\x7D'}</span>). An answer that is not a JSON object fails the step. Describe the fields you want in the prompt.</div>
  {/if}
</div>
{#if data.output_format === 'json'}
  <div>
    <label class="block">
      <span class="text-[10px] font-medium text-dark-text-muted uppercase tracking-wider">JSON schema (optional)</span>
    <textarea
      bind:value={data.json_schema}
      rows={4}
      class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle font-mono focus:outline-none focus:ring-1 focus:ring-dark-border-subtle resize-y"
      placeholder={'{"type":"object","properties":{"severity":{"type":"string","enum":["critical","warning","ok"]}},"required":["severity"]}'}
    ></textarea></label>
    <div class="mt-0.5 text-[10px] text-dark-text-muted">Constrains the object on providers with structured output (OpenAI, Gemini); others receive it as an instruction.</div>
  </div>
{/if}
<!-- Port descriptions -->
<div class="border-t border-dark-border pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-dark-text-muted uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="The main user message or instruction sent to the LLM. This is required. Falls back to 'text' or 'data' inputs if the prompt port is not connected.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">prompt</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Main instruction sent to the LLM (required)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">string</div>
      </div>
      <div title="Optional supplementary data appended to the prompt under a 'Context:' header. Use this for reference documents, previous node outputs, or fetched content.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">context</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Extra reference data appended to prompt (optional)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">string</div>
      </div>
      <div title="Files sent to the model with the prompt: images, PDFs, audio, video or text. Connect the file output of HTTP Request (save response), Exec paths, or the files/image output of another LLM or Agent Call. Remote URLs must be downloaded with HTTP Request first. Up to 10 files, 20 MB total.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">attachments</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Images, PDFs, audio, text… sent with the prompt (optional)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"file ref | path | [..] | { name, content_base64 } | data: URL"}</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-dark-text-muted uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Returns a map with the LLM response text. Uses the 'data' port type so it can connect to any downstream node.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">response</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Map with LLM response, connectable to any node</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"{ response: string }"}</div>
      </div>
      <div title="Images the model returned (image-output models such as Gemini image), saved in the run workspace. Connect to Email attachments or inline images.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">files</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Generated images as run files ([] when none)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"[{ path, name, content_type, size_bytes }]"}</div>
      </div>
      <div title="The first generated image. Absent when the model returned none.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">image</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— First generated image</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"{ path, name, content_type, size_bytes }"}</div>
      </div>
      <div title="Only with Output format: JSON object. The parsed answer, for Switch rules (/field) and templates (.field).">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">json</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Parsed JSON answer (JSON output format)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">object</div>
      </div>
    </div>
  </div>
</div>
