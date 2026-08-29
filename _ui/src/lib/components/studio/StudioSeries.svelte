<script lang="ts">
  import { onDestroy } from 'svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { submitOrgTask, type Organization } from '@/lib/api/organizations';
  import { getTask } from '@/lib/api/tasks';
  import {
    bindEpisodeTask,
    createEpisodeDraft,
    loadEpisode,
    loadEpisodes,
    loadSeries,
    loadSeriesList,
    saveSeriesManifest,
    slugify,
    EPISODE_STATUS_COLORS,
    type CharacterItem,
    type EpisodeItem,
    type SeriesCastEntry,
    type SeriesManifest,
  } from '@/lib/api/studio';
  import StudioStoryboard from './StudioStoryboard.svelte';
  import { BookOpen, ChevronLeft, Film, Loader2, Plus, RefreshCw, Save, Sparkles, Video, WandSparkles } from 'lucide-svelte';

  interface Props {
    assetsRoot: string;
    characters: CharacterItem[];
    seriesOrg: Organization | null;
    onTaskSubmitted: () => void;
  }
  let { assetsRoot, characters, seriesOrg, onTaskSubmitted }: Props = $props();

  let loading = $state(true);
  let seriesList = $state<SeriesManifest[]>([]);
  let selectedSeries = $state<SeriesManifest | null>(null);
  let episodes = $state<EpisodeItem[]>([]);
  let selectedEpisode = $state<EpisodeItem | null>(null);

  let showCreate = $state(false);
  let creating = $state(false);
  let newName = $state('');
  let newDescription = $state('');
  let newStyle = $state('cinematic naturalism, consistent production design, coherent lighting and color grade');
  let newNegative = $state('low quality, distorted face, inconsistent wardrobe, watermark, text');
  let newSeed = $state('');
  let newAspect = $state('16:9');
  let newDraftModel = $state('scene_video_budget');
  let newFinalModel = $state('scene_video');
  let newCast = $state<string[]>([]);

  let editing = $state(false);
  let saving = $state(false);
  let editDescription = $state('');
  let editStyle = $state('');
  let editNegative = $state('');
  let editSeed = $state('');
  let editAspect = $state('16:9');
  let editDraftModel = $state('scene_video_budget');
  let editFinalModel = $state('scene_video');
  let editCast = $state<string[]>([]);

  let showEpisodeForm = $state(false);
  let episodeTitle = $state('');
  let episodePremise = $state('');
  let taskBusy = $state(false);
  let activeTaskId = $state('');
  let activeAction = $state('');
  let pollTimer: ReturnType<typeof setInterval> | null = null;

  async function reloadList() {
    loading = true;
    try {
      seriesList = await loadSeriesList(assetsRoot);
      if (selectedSeries) {
        const fresh = await loadSeries(assetsRoot, selectedSeries.slug);
        if (fresh) selectedSeries = fresh;
        await reloadEpisodes();
      }
    } finally {
      loading = false;
    }
  }

  async function selectSeries(s: SeriesManifest) {
    selectedSeries = s;
    selectedEpisode = null;
    editing = false;
    episodes = await loadEpisodes(assetsRoot, s.slug);
    syncEditForm(s);
  }

  async function reloadEpisodes() {
    if (!selectedSeries) return;
    episodes = await loadEpisodes(assetsRoot, selectedSeries.slug);
    if (selectedEpisode) {
      const fresh = await loadEpisode(assetsRoot, selectedSeries.slug, selectedEpisode.manifest.number);
      selectedEpisode = fresh;
    }
  }

  function syncEditForm(s: SeriesManifest) {
    editDescription = s.description || '';
    editStyle = s.style?.block || '';
    editNegative = s.style?.negative || '';
    editSeed = s.style?.seed == null ? '' : String(s.style.seed);
    editAspect = s.style?.aspect_ratio || '16:9';
    editDraftModel = s.style?.draft_model || 'scene_video_budget';
    editFinalModel = s.style?.final_model || 'scene_video';
    editCast = (s.cast || []).map((c) => c.character);
  }

  function toggleIn(list: string[], slug: string): string[] {
    return list.includes(slug) ? list.filter((x) => x !== slug) : [...list, slug];
  }

  function castFrom(slugs: string[]): SeriesCastEntry[] {
    return slugs.map((character) => ({ character, role: '' }));
  }

  async function createSeries() {
    if (!newName.trim()) {
      addToast('Series name is required', 'alert');
      return;
    }
    const slug = slugify(newName);
    if (!slug) {
      addToast('Series name needs letters or numbers', 'alert');
      return;
    }
    if (seriesList.some((s) => s.slug === slug)) {
      addToast('A series with this name already exists', 'alert');
      return;
    }
    if ((newDraftModel.startsWith('ltx-') || newFinalModel.startsWith('ltx-')) && !['16:9', '9:16'].includes(newAspect)) {
      addToast('LTX-2.5 supports 16:9 or 9:16 series only', 'alert');
      return;
    }
    creating = true;
    try {
      const now = new Date().toISOString().replace(/\.\d+Z$/, 'Z');
      const manifest: SeriesManifest = {
        name: newName.trim(),
        slug,
        description: newDescription.trim(),
        cast: castFrom(newCast),
        style: {
          block: newStyle.trim(),
          negative: newNegative.trim(),
          seed: newSeed ? Number(newSeed) : null,
          aspect_ratio: newAspect,
          draft_model: newDraftModel,
          final_model: newFinalModel,
        },
        created_at: now,
        updated_at: now,
      };
      await saveSeriesManifest(assetsRoot, manifest);
      addToast('Series created', 'info');
      showCreate = false;
      newName = '';
      newDescription = '';
      newCast = [];
      await reloadList();
      await selectSeries(manifest);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to create series', 'alert');
    } finally {
      creating = false;
    }
  }

  async function saveSeries() {
    if (!selectedSeries) return;
    if ((editDraftModel.startsWith('ltx-') || editFinalModel.startsWith('ltx-')) && !['16:9', '9:16'].includes(editAspect)) {
      addToast('LTX-2.5 supports 16:9 or 9:16 series only', 'alert');
      return;
    }
    saving = true;
    try {
      const updated: SeriesManifest = {
        ...selectedSeries,
        description: editDescription.trim(),
        cast: castFrom(editCast),
        style: {
          block: editStyle.trim(),
          negative: editNegative.trim(),
          seed: editSeed ? Number(editSeed) : null,
          aspect_ratio: editAspect,
          draft_model: editDraftModel,
          final_model: editFinalModel,
        },
      };
      await saveSeriesManifest(assetsRoot, updated);
      selectedSeries = updated;
      seriesList = seriesList.map((s) => (s.slug === updated.slug ? updated : s));
      editing = false;
      addToast('Series style and cast saved', 'info');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Save failed', 'alert');
    } finally {
      saving = false;
    }
  }

  async function writeEpisode() {
    if (!seriesOrg || !selectedSeries || !episodeTitle.trim() || !episodePremise.trim()) {
      addToast('Episode title and premise are required', 'alert');
      return;
    }
    taskBusy = true;
    try {
      const draft = await createEpisodeDraft(
        assetsRoot,
        selectedSeries.slug,
        episodes,
        episodeTitle.trim(),
        episodePremise.trim(),
      );
      const res = await submitOrgTask(seriesOrg.id, {
        title: `${selectedSeries.name} — Episode ${draft.manifest.number}: ${episodeTitle.trim()}`,
        description: [
          `Write episode ${draft.manifest.number} of series "${selectedSeries.slug}".`,
          `An episode draft already exists; use episode_get, then episode_update + shots_set. Do NOT create another episode.`,
          `Premise: ${episodePremise.trim()}`,
          'Stop after the episode is scripted and its complete planned shot list is recorded. Do not generate media in this task.',
        ].join('\n'),
      });
      await bindEpisodeTask(assetsRoot, selectedSeries.slug, draft.manifest.number, res.id);
      activeTaskId = res.id;
      activeAction = 'Writing episode';
      selectedEpisode = await loadEpisode(assetsRoot, selectedSeries.slug, draft.manifest.number);
      showEpisodeForm = false;
      episodeTitle = '';
      episodePremise = '';
      await reloadEpisodes();
      startPoll();
      onTaskSubmitted();
      addToast(`Episode task ${res.identifier || ''} started`, 'info');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to start episode', 'alert');
      taskBusy = false;
    }
  }

  async function runStage(stage: 'stills' | 'shots' | 'assemble') {
    if (!seriesOrg || !selectedSeries || !selectedEpisode) return;
    taskBusy = true;
    const num = selectedEpisode.manifest.number;
    const briefs = {
      stills: [
        `Generate ONLY the keyframe stills for episode ${num} of series "${selectedSeries.slug}".`,
        'Read series_get and episode_get. For each planned shot without a still, compose it with edit_image_nano_banana using cast references and the series style lock, then shot_update still=<path> status=still_ready. Do not generate clips yet.',
      ],
      shots: [
        `Generate the video clips for episode ${num} of series "${selectedSeries.slug}".`,
        'Read series_get and episode_get. Use existing stills as start images, full character reference sets, style/negative/seed, and chain continuity via extract_last_frame. Record every clip and provenance with shot_update. Skip shots already generated unless failed.',
      ],
      assemble: [
        `Assemble episode ${num} of series "${selectedSeries.slug}" into the final video.`,
        'Read episode_get. Use each character\'s bound voice for dialogue, assemble generated clips, generate + burn subtitles, verify with ffprobe, then episode_update status=assembled final_video=<path> duration_s=<seconds>.',
      ],
    };
    const titles = { stills: 'Generate stills', shots: 'Generate shots', assemble: 'Assemble episode' };
    try {
      const res = await submitOrgTask(seriesOrg.id, {
        title: `${titles[stage]} — ${selectedSeries.name} E${num}`,
        description: briefs[stage].join('\n'),
      });
      await bindEpisodeTask(assetsRoot, selectedSeries.slug, num, res.id);
      activeTaskId = res.id;
      activeAction = titles[stage];
      startPoll();
      onTaskSubmitted();
      addToast(`Task ${res.identifier || ''} started`, 'info');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to start stage', 'alert');
      taskBusy = false;
    }
  }

  function startPoll() {
    if (pollTimer) clearInterval(pollTimer);
    pollTimer = setInterval(pollActive, 5000);
  }

  async function pollActive() {
    await reloadEpisodes();
    if (!activeTaskId) return;
    try {
      const task = await getTask(activeTaskId);
      if (task && !['open', 'todo', 'in_progress', 'in_review'].includes(task.status)) {
        if (pollTimer) clearInterval(pollTimer);
        pollTimer = null;
        taskBusy = false;
        activeTaskId = '';
        activeAction = '';
        await reloadEpisodes();
      }
    } catch {
      /* keep manifest polling; task may briefly be unavailable */
    }
  }

  onDestroy(() => {
    if (pollTimer) clearInterval(pollTimer);
  });

  reloadList();
</script>

<div class="space-y-5">
  {#if !selectedSeries}
    <div class="flex items-center justify-between">
      <div>
        <h2 class="text-sm font-semibold text-gray-900 dark:text-dark-text flex items-center gap-1.5"><Film size={14} /> Series</h2>
        <p class="mt-0.5 text-[11px] text-gray-400 dark:text-dark-text-muted">Cast, style lock, episodes and shot-by-shot production state.</p>
      </div>
      <div class="flex items-center gap-2">
        <button onclick={reloadList} class="p-1 text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary" title="Refresh"><RefreshCw size={13} /></button>
        <button onclick={() => (showCreate = !showCreate)} class="inline-flex items-center gap-1 px-2.5 py-1 text-[11px] font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover"><Plus size={12} /> New series</button>
      </div>
    </div>

    {#if showCreate}
      <section class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-4 space-y-3">
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-2">
          <label class="text-[10px] text-gray-500 dark:text-dark-text-muted">Series name<input bind:value={newName} placeholder="Neon Alley" class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text" /></label>
          <label class="text-[10px] text-gray-500 dark:text-dark-text-muted">Aspect ratio<select bind:value={newAspect} class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text"><option>16:9</option><option>9:16</option><option>1:1</option><option>21:9</option></select></label>
        </div>
        <label class="block text-[10px] text-gray-500 dark:text-dark-text-muted">Premise / logline<textarea bind:value={newDescription} rows="2" placeholder="A disgraced detective follows impossible clues through a city that changes every night." class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text resize-y"></textarea></label>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-2">
          <label class="text-[10px] text-gray-500 dark:text-dark-text-muted">Style lock<textarea bind:value={newStyle} rows="3" class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text resize-y"></textarea></label>
          <label class="text-[10px] text-gray-500 dark:text-dark-text-muted">Negative prompt<textarea bind:value={newNegative} rows="3" class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text resize-y"></textarea></label>
        </div>
        <div class="grid grid-cols-1 sm:grid-cols-3 gap-2">
          <label class="text-[10px] text-gray-500 dark:text-dark-text-muted">Seed (optional)<input bind:value={newSeed} type="number" placeholder="424242" class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text" /></label>
          <label class="text-[10px] text-gray-500 dark:text-dark-text-muted">Draft model<select bind:value={newDraftModel} class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text"><option value="scene_video_budget">Vidu Q2 (FAL)</option><option value="ltx-2-5-fast">LTX-2.5 Fast (direct)</option><option value="continuity_video_budget">MiniMax H3 continuity (FAL)</option></select></label>
          <label class="text-[10px] text-gray-500 dark:text-dark-text-muted">Final model<select bind:value={newFinalModel} class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text"><option value="scene_video">Kling v3 Elements (FAL)</option><option value="scene_video_veo">Veo 3.1 (FAL)</option><option value="ltx-2-5-pro">LTX-2.5 Pro (direct)</option><option value="scene_video_long">Seedance 2.5 (FAL)</option></select></label>
        </div>
        <div>
          <div class="text-[10px] text-gray-500 dark:text-dark-text-muted mb-1">Cast</div>
          {#if characters.length}
            <div class="flex flex-wrap gap-1.5">
              {#each characters as c (c.manifest.slug)}
                <button onclick={() => (newCast = toggleIn(newCast, c.manifest.slug))} class={['px-2 py-1 text-[10px] border transition-colors', newCast.includes(c.manifest.slug) ? 'bg-gray-900 dark:bg-accent text-white border-gray-900 dark:border-accent' : 'border-gray-200 dark:border-dark-border text-gray-600 dark:text-dark-text-secondary']}>{c.manifest.name}</button>
              {/each}
            </div>
          {:else}
            <p class="text-[10px] text-amber-600 dark:text-amber-400">Create characters first; an empty cast is allowed and can be filled later.</p>
          {/if}
        </div>
        <div class="flex justify-end gap-2"><button onclick={() => (showCreate = false)} class="px-2 py-1 text-[11px] text-gray-500">Cancel</button><button onclick={createSeries} disabled={creating} class="inline-flex items-center gap-1 px-3 py-1 text-[11px] font-medium bg-gray-900 dark:bg-accent text-white disabled:opacity-50">{#if creating}<Loader2 size={11} class="animate-spin" />{/if} Create series</button></div>
      </section>
    {/if}

    {#if loading}
      <div class="h-32 flex items-center justify-center text-gray-400"><Loader2 size={16} class="animate-spin" /></div>
    {:else if seriesList.length === 0}
      <div class="border border-dashed border-gray-200 dark:border-dark-border py-10 text-center"><Film size={24} class="mx-auto mb-2 text-gray-300 dark:text-dark-text-faint" /><p class="text-xs text-gray-400 dark:text-dark-text-muted">No series yet. Start with a premise, cast and one visual style lock.</p></div>
    {:else}
      <div class="divide-y divide-gray-100 dark:divide-dark-border border-y border-gray-200 dark:border-dark-border">
        {#each seriesList as s (s.slug)}
          <button onclick={() => selectSeries(s)} class="w-full py-3 flex items-center gap-3 text-left hover:bg-gray-50 dark:hover:bg-dark-surface transition-colors">
            <div class="w-10 h-14 shrink-0 bg-gray-900 dark:bg-dark-highest flex items-end p-1.5"><Film size={14} class="text-white dark:text-accent" /></div>
            <div class="min-w-0">
              <h3 class="text-xs font-semibold text-gray-900 dark:text-dark-text">{s.name}</h3>
              <p class="text-[11px] text-gray-500 dark:text-dark-text-muted truncate">{s.description || 'No premise yet'}</p>
              <p class="mt-1 text-[9px] uppercase tracking-wide text-gray-400 dark:text-dark-text-muted">{(s.cast || []).length} cast · {s.style?.aspect_ratio || '16:9'} · seed {s.style?.seed ?? 'auto'}</p>
            </div>
            <span class="ml-auto text-[10px] text-gray-400 dark:text-dark-text-muted">Open →</span>
          </button>
        {/each}
      </div>
    {/if}
  {:else}
    <div class="flex items-start gap-3">
      <button onclick={() => { selectedSeries = null; selectedEpisode = null; }} class="mt-0.5 p-1 text-gray-400 hover:text-gray-700 dark:hover:text-dark-text" title="Back to series"><ChevronLeft size={15} /></button>
      <div class="min-w-0 flex-1">
        <div class="flex items-start justify-between gap-3">
          <div><h2 class="text-base font-semibold text-gray-900 dark:text-dark-text">{selectedSeries.name}</h2><p class="text-[11px] text-gray-500 dark:text-dark-text-muted">{selectedSeries.description}</p></div>
          <div class="flex items-center gap-2"><button onclick={() => (editing = !editing)} class="px-2 py-1 text-[10px] border border-gray-200 dark:border-dark-border text-gray-600 dark:text-dark-text-secondary">{editing ? 'Close settings' : 'Style & cast'}</button><button onclick={reloadEpisodes} class="p-1 text-gray-400 hover:text-gray-600" title="Refresh"><RefreshCw size={13} /></button></div>
        </div>
        <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[10px] text-gray-400 dark:text-dark-text-muted"><span>Style: <strong class="font-medium text-gray-600 dark:text-dark-text-secondary">{selectedSeries.style?.block || 'unset'}</strong></span><span>{selectedSeries.style?.aspect_ratio || '16:9'}</span><span>Seed {selectedSeries.style?.seed ?? 'auto'}</span></div>
      </div>
    </div>

    {#if editing}
      <section class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-3 space-y-2">
        <label class="block text-[10px] text-gray-500 dark:text-dark-text-muted">Premise<textarea bind:value={editDescription} rows="2" class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text"></textarea></label>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-2"><label class="text-[10px] text-gray-500 dark:text-dark-text-muted">Style lock<textarea bind:value={editStyle} rows="3" class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text"></textarea></label><label class="text-[10px] text-gray-500 dark:text-dark-text-muted">Negative<textarea bind:value={editNegative} rows="3" class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text"></textarea></label></div>
        <div class="grid grid-cols-2 sm:grid-cols-4 gap-2"><label class="text-[10px] text-gray-500 dark:text-dark-text-muted">Seed<input bind:value={editSeed} type="number" class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base" /></label><label class="text-[10px] text-gray-500 dark:text-dark-text-muted">Aspect<select bind:value={editAspect} class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base"><option>16:9</option><option>9:16</option><option>1:1</option><option>21:9</option></select></label><label class="text-[10px] text-gray-500 dark:text-dark-text-muted">Draft model<select bind:value={editDraftModel} class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base"><option value="scene_video_budget">Vidu Q2</option><option value="ltx-2-5-fast">LTX-2.5 Fast</option><option value="continuity_video_budget">MiniMax H3</option></select></label><label class="text-[10px] text-gray-500 dark:text-dark-text-muted">Final model<select bind:value={editFinalModel} class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base"><option value="scene_video">Kling v3 Elements</option><option value="scene_video_veo">Veo 3.1</option><option value="ltx-2-5-pro">LTX-2.5 Pro</option><option value="scene_video_long">Seedance 2.5</option></select></label></div>
        <div class="flex flex-wrap gap-1.5">{#each characters as c (c.manifest.slug)}<button onclick={() => (editCast = toggleIn(editCast, c.manifest.slug))} class={['px-2 py-1 text-[10px] border', editCast.includes(c.manifest.slug) ? 'bg-gray-900 dark:bg-accent text-white border-gray-900 dark:border-accent' : 'border-gray-200 dark:border-dark-border text-gray-600 dark:text-dark-text-secondary']}>{c.manifest.name}</button>{/each}</div>
        <div class="flex justify-end"><button onclick={saveSeries} disabled={saving} class="inline-flex items-center gap-1 px-3 py-1 text-[11px] font-medium bg-gray-900 dark:bg-accent text-white disabled:opacity-50">{#if saving}<Loader2 size={11} class="animate-spin" />{:else}<Save size={11} />{/if} Save</button></div>
      </section>
    {/if}

    <section class="grid grid-cols-1 lg:grid-cols-[230px_1fr] gap-4">
      <aside class="border-r border-gray-200 dark:border-dark-border lg:pr-3">
        <div class="flex items-center justify-between mb-2"><h3 class="text-xs font-semibold text-gray-900 dark:text-dark-text">Episodes</h3><button onclick={() => (showEpisodeForm = !showEpisodeForm)} class="p-1 text-gray-400 hover:text-gray-700 dark:hover:text-dark-text" title="Write next episode"><Plus size={13} /></button></div>
        {#if showEpisodeForm}
          <div class="mb-3 space-y-1.5"><input bind:value={episodeTitle} placeholder="Episode title" class="w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base" /><textarea bind:value={episodePremise} rows="3" placeholder="What happens in this episode? Include continuity notes from prior episodes." class="w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base"></textarea><button onclick={writeEpisode} disabled={taskBusy} class="w-full inline-flex items-center justify-center gap-1 px-2 py-1 text-[10px] font-medium bg-gray-900 dark:bg-accent text-white disabled:opacity-50">{#if taskBusy}<Loader2 size={10} class="animate-spin" />{:else}<BookOpen size={10} />{/if} Start writing</button></div>
        {/if}
        <div class="space-y-px">
          {#each episodes as ep (ep.manifest.number)}
            <button onclick={() => (selectedEpisode = ep)} class={['w-full text-left px-2 py-2 border-l transition-colors', selectedEpisode?.manifest.number === ep.manifest.number ? 'border-gray-900 dark:border-accent bg-gray-50 dark:bg-dark-elevated' : 'border-transparent hover:bg-gray-50 dark:hover:bg-dark-surface']}><div class="flex items-center gap-1"><span class="text-[10px] font-mono text-gray-400">E{String(ep.manifest.number).padStart(2, '0')}</span><span class={['ml-auto text-[9px] font-medium', EPISODE_STATUS_COLORS[ep.manifest.status || 'draft']]}>{ep.manifest.status || 'draft'}</span></div><div class="text-[11px] font-medium text-gray-700 dark:text-dark-text-secondary truncate">{ep.manifest.title || 'Untitled episode'}</div><div class="text-[9px] text-gray-400">{(ep.manifest.shots || []).length} shots · {ep.manifest.duration_s || '—'}s</div></button>
          {/each}
          {#if !episodes.length && !showEpisodeForm}<p class="py-5 text-center text-[10px] text-gray-400">No episodes yet.</p>{/if}
        </div>
      </aside>

      <div class="min-w-0">
        {#if selectedEpisode}
          <div class="flex flex-col sm:flex-row sm:items-start gap-3 mb-3"><div class="min-w-0 flex-1"><h3 class="text-sm font-semibold text-gray-900 dark:text-dark-text">E{String(selectedEpisode.manifest.number).padStart(2, '0')} — {selectedEpisode.manifest.title || 'Untitled'}</h3><p class="text-[11px] text-gray-500 dark:text-dark-text-muted">{selectedEpisode.manifest.synopsis || 'No synopsis yet'}</p></div><div class="flex flex-wrap gap-1"><button onclick={() => runStage('stills')} disabled={taskBusy || !selectedEpisode.manifest.shots.length} class="inline-flex items-center gap-1 px-2 py-1 text-[10px] border border-gray-200 dark:border-dark-border disabled:opacity-40"><WandSparkles size={10} /> Stills</button><button onclick={() => runStage('shots')} disabled={taskBusy || !selectedEpisode.manifest.shots.length} class="inline-flex items-center gap-1 px-2 py-1 text-[10px] border border-gray-200 dark:border-dark-border disabled:opacity-40"><Sparkles size={10} /> Shots</button><button onclick={() => runStage('assemble')} disabled={taskBusy || !selectedEpisode.manifest.shots.some((s) => s.clip)} class="inline-flex items-center gap-1 px-2 py-1 text-[10px] bg-gray-900 dark:bg-accent text-white disabled:opacity-40"><Video size={10} /> Assemble</button></div></div>
          {#if taskBusy}<div class="mb-3 flex items-center gap-2 px-2 py-1.5 bg-amber-50 dark:bg-amber-950 text-[10px] text-amber-700 dark:text-amber-300"><Loader2 size={11} class="animate-spin" /> {activeAction || 'Production task'} is running; storyboard updates every 5 seconds.</div>{/if}
          <StudioStoryboard episode={selectedEpisode} busy={taskBusy} />
        {:else}
          <div class="min-h-64 flex flex-col items-center justify-center border border-dashed border-gray-200 dark:border-dark-border text-gray-400 dark:text-dark-text-muted"><Film size={22} class="mb-2" /><p class="text-xs">Select an episode to open its storyboard.</p></div>
        {/if}
      </div>
    </section>
  {/if}
</div>
