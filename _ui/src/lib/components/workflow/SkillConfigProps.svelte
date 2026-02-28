<script lang="ts">
  let { data, skills = [] }: { data: Record<string, any>; skills?: any[] } = $props();
</script>

<div>
  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Skills</span>
  {#if skills.length > 0}
    <div class="mt-0.5 space-y-0.5">
      {#each skills as skill}
        <label class="flex items-center gap-1.5 text-[11px] text-gray-700 cursor-pointer">
          <input
            type="checkbox"
            checked={data.skills?.includes(skill.name) || false}
            onchange={(e) => {
              const current = data.skills || [];
              if ((e.target as HTMLInputElement).checked) {
                data.skills = [...current, skill.name];
              } else {
                data.skills = current.filter((s: string) => s !== skill.name);
              }
            }}
            class="rounded border-gray-300"
          />
          <span class="font-mono">{skill.name}</span>
        </label>
      {/each}
    </div>
  {:else}
    <div class="mt-0.5 text-[10px] text-gray-400 italic">No skills available</div>
  {/if}
</div>
<div class="mt-1 px-2 py-1.5 bg-green-50 border border-green-200 rounded text-[10px] text-green-700">
  Connect this node's <span class="font-mono font-medium">skills</span> output to an Agent Call's <span class="font-mono font-medium">skills</span> input.
</div>
<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="This is a static resource node with no runtime inputs.">
        <span class="text-[11px] text-gray-400 italic">None — static configuration node</span>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Emits the list of selected skill names. Connect to an agent_call node's 'skills' input port.">
        <span class="text-[11px] font-mono font-medium text-gray-700">skills</span>
        <span class="text-[10px] text-gray-400 ml-1">— List of skill names for agent_call</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ skills: string[] }"}</div>
      </div>
    </div>
  </div>
</div>
