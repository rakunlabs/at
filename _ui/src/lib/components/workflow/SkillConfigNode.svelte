<script lang="ts">
  import { Handle, type NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
  let skills = $derived<string[]>(Array.isArray(data.skills) ? data.skills : []);
</script>

<WorkflowNode {id} type="skill_config" {data} {selected} tags={skills} setup={skills.length ? '' : 'Select skills'}>
  {#snippet extra()}<Handle id="skills" type="output" port="config" position="top" label="skills" />{/snippet}
</WorkflowNode>
