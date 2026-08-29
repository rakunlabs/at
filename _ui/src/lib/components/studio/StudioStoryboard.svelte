<script lang="ts">
  import { fileServeUrl } from '@/lib/api/files';
  import {
    episodeAssetPath,
    SHOT_STATUS_COLORS,
    type EpisodeItem,
    type Shot,
  } from '@/lib/api/studio';
  import { Loader2, Film, Image as ImageIcon, Info, X } from 'lucide-svelte';

  interface Props {
    episode: EpisodeItem;
    busy?: boolean;
  }
  let { episode, busy = false }: Props = $props();

  let provenance = $state<Shot | null>(null);

  const stillUrl = (s: Shot) => (s.still ? fileServeUrl(episodeAssetPath(episode.dir, s.still), s.updated_at) : '');
  const clipUrl = (s: Shot) => (s.clip ? fileServeUrl(episodeAssetPath(episode.dir, s.clip), s.updated_at) : '');

  const scenes = $derived.by(() => {
    const groups = new Map<number, Shot[]>();
    for (const s of episode.manifest.shots || []) {
      const key = s.scene || 1;
      if (!groups.has(key)) groups.set(key, []);
      groups.get(key)!.push(s);
    }
    return [...groups.entries()].sort((a, b) => a[0] - b[0]);
  });
</script>

{#if (episode.manifest.shots || []).length === 0}
  <p class="text-xs text-gray-400 dark:text-dark-text-muted py-6 text-center border border-dashed border-gray-200 dark:border-dark-border">
    No shots yet — write the episode first.
    {#if busy}<Loader2 size={12} class="inline animate-spin ml-1" />{/if}
  </p>
{:else}
  <div class="space-y-4">
    {#each scenes as [sceneNo, shots] (sceneNo)}
      <div>
        <div class="text-[10px] font-semibold uppercase tracking-wide text-gray-400 dark:text-dark-text-muted mb-1.5">
          Scene {sceneNo}
        </div>
        <div class="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-3">
          {#each shots as s (s.id)}
            <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface flex flex-col">
              <!-- media: clip > still > placeholder -->
              {#if s.clip}
                <!-- svelte-ignore a11y_media_has_caption -->
                <video controls preload="metadata" src={clipUrl(s)} poster={stillUrl(s) || undefined} class="w-full aspect-video object-cover bg-black"></video>
              {:else if s.still}
                <img src={stillUrl(s)} alt={s.id} class="w-full aspect-video object-cover bg-gray-100 dark:bg-dark-elevated" />
              {:else}
                <div class="w-full aspect-video flex items-center justify-center bg-gray-50 dark:bg-dark-elevated text-gray-300 dark:text-dark-text-faint">
                  {#if busy}<Loader2 size={16} class="animate-spin" />{:else if s.status === 'failed'}<X size={16} class="text-red-400" />{:else}<ImageIcon size={16} />{/if}
                </div>
              {/if}
              <div class="p-1.5 space-y-1">
                <div class="flex items-center gap-1">
                  <span class="text-[10px] font-mono font-semibold text-gray-700 dark:text-dark-text-secondary">{s.id}</span>
                  <span class="text-[9px] text-gray-400 dark:text-dark-text-muted">{s.duration_s || '?'}s</span>
                  <span class={['ml-auto px-1 py-px text-[9px] font-medium rounded-sm', SHOT_STATUS_COLORS[s.status || 'planned'] || SHOT_STATUS_COLORS.planned]}>
                    {s.status || 'planned'}
                  </span>
                  <button
                    onclick={() => (provenance = provenance?.id === s.id ? null : s)}
                    class="text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary"
                    title="Provenance (prompt / model / seed / cost)"
                  >
                    <Info size={11} />
                  </button>
                </div>
                <p class="text-[10px] text-gray-500 dark:text-dark-text-muted line-clamp-2" title={s.description}>{s.description || '—'}</p>
                {#if (s.dialogue || []).length}
                  <p class="text-[9px] text-gray-400 dark:text-dark-text-muted italic truncate" title={(s.dialogue || []).map((d) => `${d.character}: ${d.line}`).join('\n')}>
                    <Film size={8} class="inline" /> {(s.dialogue || []).map((d) => d.character).join(', ')}
                  </p>
                {/if}
              </div>
            </div>
          {/each}
        </div>
      </div>
    {/each}
  </div>

  {#if provenance}
    {@const p = provenance}
    <div class="mt-3 border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-elevated p-3 text-[11px] text-gray-600 dark:text-dark-text-secondary space-y-1.5">
      <div class="flex items-center justify-between">
        <span class="font-semibold text-gray-900 dark:text-dark-text">Shot {p.id} — provenance</span>
        <button onclick={() => (provenance = null)} class="text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary"><X size={12} /></button>
      </div>
      {#if p.prompt}<div><span class="text-gray-400 dark:text-dark-text-muted">Prompt:</span> <span class="whitespace-pre-wrap">{p.prompt}</span></div>{/if}
      <div class="flex flex-wrap gap-x-4 gap-y-0.5">
        {#if p.model}<span><span class="text-gray-400 dark:text-dark-text-muted">Model:</span> <code>{p.model}</code></span>{/if}
        {#if p.seed != null}<span><span class="text-gray-400 dark:text-dark-text-muted">Seed:</span> {p.seed}</span>{/if}
        {#if p.cost_estimate_usd != null}<span><span class="text-gray-400 dark:text-dark-text-muted">Est. cost:</span> ${p.cost_estimate_usd}</span>{/if}
        {#if p.updated_at}<span><span class="text-gray-400 dark:text-dark-text-muted">Updated:</span> {p.updated_at}</span>{/if}
      </div>
      {#if (p.references || []).length}
        <div><span class="text-gray-400 dark:text-dark-text-muted">References:</span> {(p.references || []).join(', ')}</div>
      {/if}
      {#if (p.dialogue || []).length}
        <div>
          <span class="text-gray-400 dark:text-dark-text-muted">Dialogue:</span>
          {#each p.dialogue || [] as d, i (i)}
            <div class="pl-2">— <strong>{d.character}</strong>: {d.line}</div>
          {/each}
        </div>
      {/if}
    </div>
  {/if}
{/if}
