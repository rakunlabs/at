<script lang="ts">
  import { presetRange } from '@/lib/api/usage';
  import { formatDateTimeInput } from '@/lib/helper/format';

  interface Props {
    from?: string;
    to?: string;
    preset?: string;
    onchange?: (range: { from: string; to: string; preset: string }) => void;
  }

  let {
    from = $bindable(''),
    to = $bindable(''),
    preset = $bindable('7d'),
    onchange,
  }: Props = $props();

  // Initialize with preset if both are empty.
  $effect(() => {
    if (!from && !to) {
      applyPreset('7d');
    }
  });

  function applyPreset(p: '24h' | '7d' | '30d' | 'mtd') {
    preset = p;
    const r = presetRange(p);
    from = r.from;
    to = r.to;
    onchange?.({ from, to, preset: p });
  }

  function handleCustom() {
    preset = 'custom';
    onchange?.({ from, to, preset });
  }

  // dateTime-local input needs yyyy-MM-ddTHH:mm format (no Z, no seconds).
  function isoToLocal(iso: string): string {
    return formatDateTimeInput(iso);
  }

  function localToIso(local: string): string {
    if (!local) return '';
    return new Date(local).toISOString();
  }
</script>

<div class="flex items-center gap-1 text-xs">
  <button
    onclick={() => applyPreset('24h')}
    class={[
      'px-2 py-1 border',
      preset === '24h'
        ? 'text-dark-base bg-accent border-accent'
        : 'border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated',
    ]}
  >24h</button>
  <button
    onclick={() => applyPreset('7d')}
    class={[
      'px-2 py-1 border',
      preset === '7d'
        ? 'text-dark-base bg-accent border-accent'
        : 'border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated',
    ]}
  >7d</button>
  <button
    onclick={() => applyPreset('30d')}
    class={[
      'px-2 py-1 border',
      preset === '30d'
        ? 'text-dark-base bg-accent border-accent'
        : 'border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated',
    ]}
  >30d</button>
  <button
    onclick={() => applyPreset('mtd')}
    class={[
      'px-2 py-1 border',
      preset === 'mtd'
        ? 'text-dark-base bg-accent border-accent'
        : 'border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated',
    ]}
  >MTD</button>

  <span class="mx-2 text-dark-border">|</span>

  <input
    type="datetime-local"
    value={isoToLocal(from)}
    onchange={(e) => {
      from = localToIso((e.currentTarget as HTMLInputElement).value);
      handleCustom();
    }}
    class="px-1.5 py-1 border border-dark-border-subtle bg-dark-surface text-dark-text-secondary"
  />
  <span class="text-dark-text-muted">→</span>
  <input
    type="datetime-local"
    value={isoToLocal(to)}
    onchange={(e) => {
      to = localToIso((e.currentTarget as HTMLInputElement).value);
      handleCustom();
    }}
    class="px-1.5 py-1 border border-dark-border-subtle bg-dark-surface text-dark-text-secondary"
  />
</div>
