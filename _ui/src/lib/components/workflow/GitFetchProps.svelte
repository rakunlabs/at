<script lang="ts">
  let { data }: { data: Record<string, any> } = $props();
</script>

<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Repo URL</span>
  <input
    type="text"
    bind:value={data.repo_url}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder="https://github.com/user/repo.git"
  /></label>
  <div class="mt-0.5 text-[10px] text-gray-400">Supports templates: {'{{ getVar "repo_url" }}'}</div>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Branch</span>
  <input
    type="text"
    bind:value={data.branch}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder="main"
  /></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Token (Optional)</span>
  <input
    type="text"
    bind:value={data.token}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder={'{{ getVar "git_token" }}'}
  /></label>
  <div class="mt-0.5 text-[10px] text-gray-400">For private HTTPS repos. Supports templates.</div>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">SSH Key (Optional)</span>
  <textarea
    bind:value={data.ssh_key}
    rows={3}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
    placeholder={'{{ getVar "ssh_key" }}'}
  ></textarea></label>
  <div class="mt-0.5 text-[10px] text-gray-400">For private SSH repos. Supports templates.</div>
</div>

<!-- Template hint -->
<div class="border-t border-gray-200 pt-2 mt-2">
  <div class="text-[10px] text-gray-400 leading-relaxed">
    All fields support Go templates.<br/>
    <span class="font-mono">{'{{ getVar "key" }}'}</span> — resolve a variable<br/>
    <span class="font-mono">{'{{ .field }}'}</span> — from input data
  </div>
</div>

<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Any upstream data.">
        <span class="text-[11px] font-mono font-medium text-gray-700">data</span>
        <span class="text-[10px] text-gray-400 ml-1">— Trigger signal / template data</span>
      </div>
      <div title="Dynamic override for repo_url.">
        <span class="text-[11px] font-mono font-medium text-gray-700">repo_url</span>
        <span class="text-[10px] text-gray-400 ml-1">— Override repo URL</span>
      </div>
      <div title="Dynamic override for branch.">
        <span class="text-[11px] font-mono font-medium text-gray-700">branch</span>
        <span class="text-[10px] text-gray-400 ml-1">— Override branch</span>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Output with repo path and metadata.">
        <span class="text-[11px] font-mono font-medium text-gray-700">output</span>
        <span class="text-[10px] text-gray-400 ml-1">— Result</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">
          {"{ repo_dir, commit_sha, repo_url, branch, is_new_clone }"}
        </div>
      </div>
    </div>
  </div>
</div>
