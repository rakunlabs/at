<script lang="ts">
  let { data = $bindable(), providers = [], workflow }: { data: Record<string, any>; providers?: any[]; workflow?: any } = $props();
</script>

<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Provider</span>
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
    <span class="text-[11px] font-medium text-dark-text-muted">Model</span>
  <input
    type="text"
    bind:value={data.model}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
    placeholder="whisper-1"
  /></label>
</div>
<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Language</span>
  <input
    type="text"
    bind:value={data.language}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
    placeholder="en"
  /></label>
  <div class="mt-0.5 text-[10px] text-dark-text-muted">ISO-639-1 code (e.g. en, es, fr). Leave empty for auto-detect.</div>
</div>
<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Response Format</span>
  <select
    bind:value={data.response_format}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
  >
    <option value="json">json</option>
    <option value="text">text</option>
    <option value="srt">srt</option>
    <option value="verbose_json">verbose_json</option>
    <option value="vtt">vtt</option>
  </select></label>
</div>
<!-- Port descriptions -->
<div class="border-t border-dark-border pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Audio data to transcribe (URL, base64, or binary).">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">audio</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Audio input (required)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">string</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Transcribed text output.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">output</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Transcription result</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"{ text: string, language: string, duration: number }"}</div>
      </div>
    </div>
  </div>
</div>
