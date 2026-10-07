<script lang="ts">
  import { untrack } from 'svelte';
  import CodeExpander from './CodeExpander.svelte';

  let { data }: { data: Record<string, any> } = $props();

  $effect.pre(() => {
    untrack(() => {
      if (!data.language) data.language = 'bash';
    });
  });

  let codeLang = $derived(data.language === 'python' ? 'python' : 'bash');
  let codePlaceholder = $derived(data.language === 'python'
    ? 'import json, sys\n\nprint(json.dumps({"result": "hello"}))'
    : "echo 'Hello World'"
  );
</script>

<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Language</span>
  <select
    bind:value={data.language}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle bg-dark-elevated text-dark-text"
  >
    <option value="bash">Bash</option>
    <option value="python">Python</option>
  </select></label>
</div>
<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Inputs</span>
  <input
    type="number"
    bind:value={data.input_count}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
    min="1"
    max="10"
    placeholder="1"
  /></label>
</div>
<div>
  <CodeExpander
    bind:value={data.command}
    label={data.language === 'python' ? 'Python Code' : 'Command'}
    language={codeLang}
    rows={4}
    placeholder={codePlaceholder}
  />
  {#if data.language === 'python'}
    <div class="mt-0.5 text-[10px] text-dark-text-muted">Python 3 script. Input data available via <code class="font-mono bg-dark-elevated px-0.5">AT_NODE_INPUT</code> env var (JSON). Print result to stdout.</div>
  {:else}
    <div class="mt-0.5 text-[10px] text-dark-text-muted">Shell command (supports <code class="font-mono bg-dark-elevated px-0.5">{'{{.var}}'}</code> templates from inputs)</div>
  {/if}
</div>
<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Working Dir</span>
  <input
    type="text"
    bind:value={data.working_dir}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle font-mono focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
    placeholder="(sandbox root)"
  /></label>
  <div class="mt-0.5 text-[10px] text-dark-text-muted">Subdirectory within sandbox</div>
</div>
<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Timeout (sec)</span>
  <input
    type="number"
    bind:value={data.timeout}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
    min="1"
    max="600"
    placeholder="60"
  /></label>
</div>
<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Sandbox Root</span>
  <input
    type="text"
    bind:value={data.sandbox_root}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle font-mono focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
    placeholder="/tmp/at-sandbox"
  /></label>
  <div class="mt-0.5 text-[10px] text-dark-text-muted">All commands run inside this directory</div>
</div>
<!-- Port descriptions -->
<div class="border-t border-dark-border pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Input data available for template resolution in the command string. Use 'data' (single) or 'data1'...'dataN' (multi-input).">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">data</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Template context for command (or data1..dataN)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">any</div>
      </div>
      <div title="Dynamic override for the static command config.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">command</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Override the static command (optional)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">string</div>
      </div>
      <div title="Dynamic override for the static working directory config.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">working_dir</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Override the static working dir (optional)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">string</div>
      </div>
      <div title="Additional environment variables merged with static config env.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">env</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Extra environment variables (optional)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">map</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Activated when exit code is 0. Output includes stdout, stderr, exit_code, result.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">true</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Exit code 0 (stdout, stderr, exit_code)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"{ ...inputs, stdout: string, stderr: string, exit_code: number, result: string }"}</div>
      </div>
      <div title="Activated when exit code is non-zero. Output includes stdout, stderr, exit_code, result.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">false</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Non-zero exit code</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"{ ...inputs, stdout: string, stderr: string, exit_code: number, result: string }"}</div>
      </div>
      <div title="Always activated regardless of exit code.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">always</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Fires for every execution</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"{ ...inputs, stdout: string, stderr: string, exit_code: number, result: string }"}</div>
      </div>
    </div>
  </div>
</div>
