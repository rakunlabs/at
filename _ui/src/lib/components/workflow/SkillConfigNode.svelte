<script lang="ts">
  import { Handle, HandleGroup, type NodeProps } from 'kaykay';

  interface SkillConfigData {
    label?: string;
    skills?: string[];
    node_number?: number;
  }

  let { id, data, selected }: NodeProps<SkillConfigData> = $props();
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-36 max-w-52 text-xs shadow-sm select-none',
    selected && 'border-green-500 ring-2 ring-green-500/25'
  ]}
>
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-green-50">
    <span class="text-[9px] font-bold px-1 py-px rounded bg-green-600 text-white tracking-wide">SKILL</span>
    <span class="text-gray-900">{data.label || 'Skill Config'}</span>
    {#if data.node_number != null}<span class="text-[9px] font-medium text-gray-400 ml-auto">#{data.node_number}</span>{/if}
  </div>
  <div class="px-2.5 py-1.5">
    {#if data.skills && data.skills.length > 0}
      {#each data.skills as skill}
        <div class="flex gap-1 items-baseline mb-0.5">
          <span class="text-gray-400 text-[10px] shrink-0">•</span>
          <span class="text-gray-700 font-mono text-[11px]">{skill}</span>
        </div>
      {/each}
    {:else}
      <div class="text-gray-400 text-[11px]">No skills selected</div>
    {/if}
  </div>
  <HandleGroup position="top" class="!gap-1">
    <Handle id="skills" type="output" port="data" label="skills" />
  </HandleGroup>
</div>
