<script lang="ts">
  let { data }: { data: Record<string, any> } = $props();
</script>

<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Inputs</span>
  <input
    type="number"
    bind:value={data.input_count}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
    min="1"
    max="10"
    placeholder="1"
  /></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Command</span>
  <textarea
    bind:value={data.command}
    rows={4}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
    placeholder="echo 'Hello World'"
  ></textarea></label>
  <div class="mt-0.5 text-[10px] text-gray-400">Shell command (supports <code class="font-mono bg-gray-100 px-0.5 rounded">{'{{.var}}'}</code> templates from inputs)</div>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Working Dir</span>
  <input
    type="text"
    bind:value={data.working_dir}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder="(sandbox root)"
  /></label>
  <div class="mt-0.5 text-[10px] text-gray-400">Subdirectory within sandbox</div>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Timeout (sec)</span>
  <input
    type="number"
    bind:value={data.timeout}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
    min="1"
    max="600"
    placeholder="60"
  /></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Sandbox Root</span>
  <input
    type="text"
    bind:value={data.sandbox_root}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder="/tmp/at-sandbox"
  /></label>
  <div class="mt-0.5 text-[10px] text-gray-400">All commands run inside this directory</div>
</div>
<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Input data available for template resolution in the command string. Use 'data' (single) or 'data1'...'dataN' (multi-input).">
        <span class="text-[11px] font-mono font-medium text-gray-700">data</span>
        <span class="text-[10px] text-gray-400 ml-1">— Template context for command (or data1..dataN)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">any</div>
      </div>
      <div title="Dynamic override for the static command config.">
        <span class="text-[11px] font-mono font-medium text-gray-700">command</span>
        <span class="text-[10px] text-gray-400 ml-1">— Override the static command (optional)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">string</div>
      </div>
      <div title="Dynamic override for the static working directory config.">
        <span class="text-[11px] font-mono font-medium text-gray-700">working_dir</span>
        <span class="text-[10px] text-gray-400 ml-1">— Override the static working dir (optional)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">string</div>
      </div>
      <div title="Additional environment variables merged with static config env.">
        <span class="text-[11px] font-mono font-medium text-gray-700">env</span>
        <span class="text-[10px] text-gray-400 ml-1">— Extra environment variables (optional)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">map</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Activated when exit code is 0. Output includes stdout, stderr, exit_code, result.">
        <span class="text-[11px] font-mono font-medium text-gray-700">true</span>
        <span class="text-[10px] text-gray-400 ml-1">— Exit code 0 (stdout, stderr, exit_code)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ ...inputs, stdout: string, stderr: string, exit_code: number, result: string }"}</div>
      </div>
      <div title="Activated when exit code is non-zero. Output includes stdout, stderr, exit_code, result.">
        <span class="text-[11px] font-mono font-medium text-gray-700">false</span>
        <span class="text-[10px] text-gray-400 ml-1">— Non-zero exit code</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ ...inputs, stdout: string, stderr: string, exit_code: number, result: string }"}</div>
      </div>
      <div title="Always activated regardless of exit code.">
        <span class="text-[11px] font-mono font-medium text-gray-700">always</span>
        <span class="text-[10px] text-gray-400 ml-1">— Fires for every execution</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ ...inputs, stdout: string, stderr: string, exit_code: number, result: string }"}</div>
      </div>
    </div>
  </div>
</div>
