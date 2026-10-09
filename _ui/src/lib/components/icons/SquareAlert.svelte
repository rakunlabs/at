<script lang="ts">
  import { Icon } from 'lucide-svelte';

  // Lucide ships the alert glyph only in a circle; the UI uses square markers.
  const iconNode: [string, Record<string, string>][] = [
    ['rect', { width: '18', height: '18', x: '3', y: '3' }],
    ['line', { x1: '12', x2: '12', y1: '8', y2: '12' }],
    ['line', { x1: '12', x2: '12.01', y1: '16', y2: '16' }],
  ];

  let props: Record<string, any> = $props();
</script>

<Icon name="square-alert" {...props} {iconNode} />
