<script lang="ts">
  let { data = $bindable(), providers = [], workflow }: { data: Record<string, any>; providers?: any[]; workflow?: any } = $props();
</script>

<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Provider</span>
  <select
    bind:value={data.provider}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
  >
    <option value="">Select provider</option>
    {#each providers as p}
      <option value={p.key}>{p.key}</option>
    {/each}
  </select></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Model</span>
  <input
    type="text"
    bind:value={data.model}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder="whisper-1"
  /></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Language</span>
  <input
    type="text"
    bind:value={data.language}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder="en"
  /></label>
  <div class="mt-0.5 text-[10px] text-gray-400">ISO-639-1 code (e.g. en, es, fr). Leave empty for auto-detect.</div>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Response Format</span>
  <select
    bind:value={data.response_format}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
  >
    <option value="json">json</option>
    <option value="text">text</option>
    <option value="srt">srt</option>
    <option value="verbose_json">verbose_json</option>
    <option value="vtt">vtt</option>
  </select></label>
</div>
<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Audio data to transcribe (URL, base64, or binary).">
        <span class="text-[11px] font-mono font-medium text-gray-700">audio</span>
        <span class="text-[10px] text-gray-400 ml-1">— Audio input (required)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">string</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Transcribed text output.">
        <span class="text-[11px] font-mono font-medium text-gray-700">output</span>
        <span class="text-[10px] text-gray-400 ml-1">— Transcription result</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ text: string, language: string, duration: number }"}</div>
      </div>
    </div>
  </div>
</div>
