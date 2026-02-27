<script lang="ts">
  import { StickyNoteNode } from 'kaykay';
  import type { NodeProps } from 'kaykay';
  import snarkdown from 'snarkdown';

  interface StickyNoteData {
    text: string;
    color?: string;
  }

  let { id, data, selected }: NodeProps<StickyNoteData> = $props();
</script>

<StickyNoteNode {id} {data} {selected}>
  {#snippet children(text)}
    {@html snarkdown(text)}
  {/snippet}
</StickyNoteNode>
