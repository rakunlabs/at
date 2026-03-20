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
  <select
    bind:value={data.model}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
  >
    <option value="tts-1">tts-1</option>
    <option value="tts-1-hd">tts-1-hd</option>
  </select></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Voice</span>
  <select
    bind:value={data.voice}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
  >
    <option value="alloy">alloy</option>
    <option value="echo">echo</option>
    <option value="fable">fable</option>
    <option value="onyx">onyx</option>
    <option value="nova">nova</option>
    <option value="shimmer">shimmer</option>
  </select></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Response Format</span>
  <select
    bind:value={data.response_format}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
  >
    <option value="mp3">mp3</option>
    <option value="opus">opus</option>
    <option value="aac">aac</option>
    <option value="flac">flac</option>
  </select></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Speed</span>
  <input
    type="number"
    bind:value={data.speed}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
    min="0.25"
    max="4.0"
    step="0.25"
    placeholder="1.0"
  /></label>
</div>
<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Text content to convert to speech.">
        <span class="text-[11px] font-mono font-medium text-gray-700">text</span>
        <span class="text-[10px] text-gray-400 ml-1">— Text to synthesize (required)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">string</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Generated audio data.">
        <span class="text-[11px] font-mono font-medium text-gray-700">output</span>
        <span class="text-[10px] text-gray-400 ml-1">— Audio data (base64 or binary)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ audio: string, format: string }"}</div>
      </div>
    </div>
  </div>
</div>
