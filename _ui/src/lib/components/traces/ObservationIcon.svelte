<script lang="ts">
  import { Sparkles, Wrench, Flag, Bot, Layers, Binary, CircleAlert } from 'lucide-svelte';
  interface Props { type: string; error?: boolean; label?: boolean }
  let { type, error = false, label = false }: Props = $props();

  const styles: Record<string, { icon: any; cls: string; text: string }> = {
    generation: { icon: Sparkles, cls: 'text-blue-400', text: 'gen' },
    tool: { icon: Wrench, cls: 'text-purple-400', text: 'tool' },
    event: { icon: Flag, cls: 'text-dark-text-muted', text: 'event' },
    agent: { icon: Bot, cls: 'text-emerald-400', text: 'agent' },
    span: { icon: Layers, cls: 'text-slate-400', text: 'span' },
    embedding: { icon: Binary, cls: 'text-cyan-400', text: 'embed' },
  };
  const style = $derived(styles[type] || styles.span);
</script>

<span class={['inline-flex shrink-0 items-center gap-1', error ? 'text-red-400' : style.cls]} title={error ? `${type} (error)` : type}>
  {#if error}<CircleAlert size={12} />{:else}<style.icon size={12} />{/if}
  {#if label}<span class="font-mono text-[10px] uppercase">{style.text}</span>{/if}
</span>
