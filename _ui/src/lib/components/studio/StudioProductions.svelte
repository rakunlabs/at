<script lang="ts">
  import { onDestroy } from 'svelte';
  import { listTasks, getTask, type Task } from '@/lib/api/tasks';
  import { fileServeUrl } from '@/lib/api/files';
  import { loadEpisodes, loadSeriesList, episodeAssetPath, type EpisodeItem, type SeriesManifest } from '@/lib/api/studio';
  import type { Organization } from '@/lib/api/organizations';
  import { loadVideoProjects, videoErrorMessage, type VideoProject } from '@/lib/api/studio-videos';
  import { Clapperboard, ExternalLink, Loader2, RefreshCw, Video } from 'lucide-svelte';

  interface Props {
    assetsRoot: string;
    avatarOrg: Organization | null;
    seriesOrg: Organization | null;
    longVideoOrg: Organization | null;
    refreshKey?: number;
  }
  let { assetsRoot, avatarOrg, seriesOrg, longVideoOrg, refreshKey = 0 }: Props = $props();

  interface EpisodeVideo {
    series: SeriesManifest;
    episode: EpisodeItem;
    videoPath: string;
  }

  let loading = $state(true);
  let productions = $state<Task[]>([]);
  let episodeByTask = $state<Map<string, EpisodeVideo>>(new Map());
  let finalEpisodes = $state<EpisodeVideo[]>([]);
  let longVideos = $state<VideoProject[]>([]);
  let videoError = $state('');
  let pollTimer: ReturnType<typeof setInterval> | null = null;
  let lastRefreshKey = $state(0);

  const RUNNING = ['open', 'todo', 'in_progress', 'in_review'];

  async function load() {
    loading = true;
    try {
      await Promise.all([loadTasks(), loadStructuredEpisodes()]);
      schedulePoll();
    } finally {
      loading = false;
    }
  }

  async function loadTasks() {
    const orgs = [avatarOrg, seriesOrg, longVideoOrg].filter((o): o is Organization => !!o);
    const results = await Promise.all(
      orgs.map(async (org) => {
        try {
          return (await listTasks({ organization_id: org.id, _sort: 'created_at:desc', _limit: 40 } as any)).data || [];
        } catch {
          return [];
        }
      }),
    );
    const seen = new Set<string>();
    productions = results
      .flat()
      .filter((t) => !t.parent_id)
      .filter((t) => {
        if (seen.has(t.id)) return false;
        seen.add(t.id);
        return true;
      })
      .sort((a, b) => String(b.created_at || '').localeCompare(String(a.created_at || '')));
  }

  async function loadStructuredEpisodes() {
    try {
      const warnings: string[] = [];
      longVideos = await loadVideoProjects(assetsRoot, (message) => warnings.push(message));
      videoError = warnings.join('\n');
    } catch (e) {
      videoError = videoErrorMessage(e);
    }
    const series = await loadSeriesList(assetsRoot);
    const taskMap = new Map<string, EpisodeVideo>();
    const videos: EpisodeVideo[] = [];
    for (const s of series) {
      const episodes = await loadEpisodes(assetsRoot, s.slug);
      for (const ep of episodes) {
        const rel = ep.manifest.final_video || '';
        const item: EpisodeVideo = {
          series: s,
          episode: ep,
          videoPath: rel ? episodeAssetPath(ep.dir, rel) : '',
        };
        if (ep.manifest.task_id) taskMap.set(ep.manifest.task_id, item);
        if (item.videoPath) videos.push(item);
      }
    }
    episodeByTask = taskMap;
    finalEpisodes = videos.sort((a, b) => String(b.episode.manifest.updated_at || '').localeCompare(String(a.episode.manifest.updated_at || '')));
  }

  function schedulePoll() {
    const anyRunning = productions.some((t) => RUNNING.includes(t.status));
    if (anyRunning && !pollTimer) pollTimer = setInterval(refreshRunning, 5000);
    if (!anyRunning && pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
  }

  async function refreshRunning() {
    const running = productions.filter((t) => RUNNING.includes(t.status));
    for (const t of running) {
      try {
        const fresh = await getTask(t.id);
        if (fresh) productions = productions.map((p) => (p.id === fresh.id ? fresh : p));
      } catch {
        /* keep polling */
      }
    }
    await loadStructuredEpisodes();
    schedulePoll();
  }

  /** Structured episode final_video first; free-text regex only for legacy one-offs. */
  function videoPath(t: Task): string {
    const project = longVideos.find((video) => video.task_id === t.id);
    if (project?.output?.final_video) return episodeAssetPath(project.dir, project.output.final_video);
    const structured = episodeByTask.get(t.id)?.videoPath;
    if (structured) return structured;
    if (!t.result) return '';
    const m = t.result.match(/(\/[^\s"'`)\]}]+\.(mp4|mov|webm))/);
    return m ? m[1] : '';
  }

  function statusColor(s: string): string {
    if (s === 'done' || s === 'completed') return 'text-green-600 dark:text-green-400';
    if (s === 'blocked' || s === 'cancelled' || s === 'failed') return 'text-red-500 dark:text-red-400';
    return 'text-amber-600 dark:text-amber-400';
  }

  $effect(() => {
    if (refreshKey !== lastRefreshKey) {
      lastRefreshKey = refreshKey;
      load();
    }
  });

  onDestroy(() => {
    if (pollTimer) clearInterval(pollTimer);
  });

  load();
</script>

<div class="space-y-6">
  {#if longVideoOrg || longVideos.length || videoError}
    <section aria-label="Finished long videos">
      <div class="flex items-center justify-between mb-2">
        <h2 class="text-sm font-semibold text-gray-900 dark:text-dark-text">Finished long videos</h2>
        <button onclick={load} class="p-1 text-gray-500 dark:text-dark-text-muted" aria-label="Refresh productions"><RefreshCw size={13} /></button>
      </div>
      {#if videoError}<p role="alert" class="mb-2 text-xs text-red-700 dark:text-red-400">Could not load long videos: {videoError}</p>{/if}
      {#if !longVideos.some((project) => project.output?.final_video)}
        <p class="text-xs text-gray-500 dark:text-dark-text-muted py-6 text-center border border-dashed border-gray-200 dark:border-dark-border">No finished long videos yet. Prepare a brief in Long Videos to get started.</p>
      {:else}
        <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
          {#each longVideos.filter((project) => project.output?.final_video) as project (project.brief.id)}
            <article class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
              <!-- svelte-ignore a11y_media_has_caption -->
              <video controls preload="metadata" aria-label={project.brief.title} src={fileServeUrl(episodeAssetPath(project.dir, project.output?.final_video || ''), project.output?.updated_at)} class="w-full aspect-video bg-black object-contain"></video>
              <div class="p-3 flex items-center gap-2">
                <h3 class="min-w-0 break-words text-xs font-semibold text-gray-900 dark:text-dark-text">{project.brief.title}</h3>
                {#if project.task_id}<a href={`#/tasks/${encodeURIComponent(project.task_id)}`} class="ml-auto shrink-0 text-gray-500 dark:text-dark-text-muted" aria-label="Open long video task"><ExternalLink size={13} /></a>{/if}
              </div>
            </article>
          {/each}
        </div>
      {/if}
    </section>
  {/if}
  <section>
    <div class="flex items-center justify-between mb-2">
      <div>
        <h2 class="text-sm font-semibold text-gray-900 dark:text-dark-text flex items-center gap-1.5"><Video size={14} /> Finished episodes</h2>
        <p class="mt-0.5 text-[11px] text-gray-400 dark:text-dark-text-muted">Structured outputs from <code>episode.json.final_video</code>.</p>
      </div>
      <button onclick={load} class="p-1 text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary" title="Refresh"><RefreshCw size={13} /></button>
    </div>
    {#if finalEpisodes.length === 0}
      <p class="text-xs text-gray-400 dark:text-dark-text-muted py-6 text-center border border-dashed border-gray-200 dark:border-dark-border">No assembled episodes yet.</p>
    {:else}
      <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
        {#each finalEpisodes as item (`${item.series.slug}-${item.episode.manifest.number}`)}
          <article class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
            <!-- svelte-ignore a11y_media_has_caption -->
            <video controls preload="metadata" src={fileServeUrl(item.videoPath, item.episode.manifest.updated_at)} class="w-full aspect-video bg-black object-contain"></video>
            <div class="p-2.5 flex items-center gap-2">
              <div class="min-w-0"><h3 class="text-xs font-semibold text-gray-900 dark:text-dark-text truncate">{item.series.name} — E{String(item.episode.manifest.number).padStart(2, '0')}</h3><p class="text-[10px] text-gray-400 dark:text-dark-text-muted truncate">{item.episode.manifest.title || 'Untitled'} · {item.episode.manifest.duration_s || '—'}s</p></div>
              {#if item.episode.manifest.task_id}<a href={`#/tasks/${item.episode.manifest.task_id}`} class="ml-auto text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary" title="Open assembly task"><ExternalLink size={12} /></a>{/if}
            </div>
          </article>
        {/each}
      </div>
    {/if}
  </section>

  <section>
    <div class="flex items-center gap-1.5 mb-2"><Clapperboard size={14} /><h2 class="text-sm font-semibold text-gray-900 dark:text-dark-text">Production tasks</h2>{#if loading}<Loader2 size={12} class="animate-spin text-gray-400" />{/if}</div>
    {#if productions.length === 0 && !loading}
      <p class="text-xs text-gray-400 dark:text-dark-text-muted py-6 text-center border border-dashed border-gray-200 dark:border-dark-border">No production tasks yet.</p>
    {:else}
      <div class="divide-y divide-gray-100 dark:divide-dark-border border-y border-gray-200 dark:border-dark-border">
        {#each productions as t (t.id)}
          {@const vp = videoPath(t)}
          {@const structured = episodeByTask.get(t.id)}
          <div class="py-3">
            <div class="flex items-center gap-2">
              {#if RUNNING.includes(t.status)}<Loader2 size={12} class="animate-spin text-amber-500 shrink-0" />{/if}
              <a href={`#/tasks/${t.id}`} class="text-xs font-medium text-gray-900 dark:text-dark-text hover:underline truncate">{t.identifier ? `${t.identifier} — ` : ''}{t.title}</a>
              {#if structured}<span class="hidden sm:inline text-[9px] px-1 py-px bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted">structured</span>{/if}
              <span class={['ml-auto text-[10px] font-medium uppercase shrink-0', statusColor(t.status)]}>{t.status}</span>
              <a href={`#/tasks/${t.id}`} class="text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary shrink-0" title="Open task"><ExternalLink size={11} /></a>
            </div>
            {#if vp}
              <!-- svelte-ignore a11y_media_has_caption -->
              <video controls preload="metadata" src={fileServeUrl(vp)} class="mt-2 max-h-72 max-w-full bg-black"></video>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </section>
</div>
