<script lang="ts">
  import { presetRange } from '@/lib/api/usage';

  interface Props {
    from?: string;
    to?: string;
    onchange?: (range: { from: string; to: string; preset: string }) => void;
  }

  let { from = $bindable(''), to = $bindable(''), onchange }: Props = $props();
  let preset = $state('7d');

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
    if (!iso) return '';
    const d = new Date(iso);
    const pad = (n: number) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
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
        ? 'bg-gray-900 text-white border-gray-900 dark:bg-accent dark:border-accent'
        : 'border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated',
    ]}
  >24h</button>
  <button
    onclick={() => applyPreset('7d')}
    class={[
      'px-2 py-1 border',
      preset === '7d'
        ? 'bg-gray-900 text-white border-gray-900 dark:bg-accent dark:border-accent'
        : 'border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated',
    ]}
  >7d</button>
  <button
    onclick={() => applyPreset('30d')}
    class={[
      'px-2 py-1 border',
      preset === '30d'
        ? 'bg-gray-900 text-white border-gray-900 dark:bg-accent dark:border-accent'
        : 'border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated',
    ]}
  >30d</button>
  <button
    onclick={() => applyPreset('mtd')}
    class={[
      'px-2 py-1 border',
      preset === 'mtd'
        ? 'bg-gray-900 text-white border-gray-900 dark:bg-accent dark:border-accent'
        : 'border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated',
    ]}
  >MTD</button>

  <span class="mx-2 text-gray-300 dark:text-dark-border">|</span>

  <input
    type="datetime-local"
    value={isoToLocal(from)}
    onchange={(e) => {
      from = localToIso((e.currentTarget as HTMLInputElement).value);
      handleCustom();
    }}
    class="px-1.5 py-1 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-surface text-gray-700 dark:text-dark-text-secondary"
  />
  <span class="text-gray-400 dark:text-dark-text-muted">→</span>
  <input
    type="datetime-local"
    value={isoToLocal(to)}
    onchange={(e) => {
      to = localToIso((e.currentTarget as HTMLInputElement).value);
      handleCustom();
    }}
    class="px-1.5 py-1 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-surface text-gray-700 dark:text-dark-text-secondary"
  />
</div>
