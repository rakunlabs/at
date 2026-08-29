<script lang="ts">
  import { addToast } from '@/lib/store/toast.svelte';
  import { deleteFile, fileServeUrl } from '@/lib/api/files';
  import { submitOrgTask, type Organization } from '@/lib/api/organizations';
  import {
    addEpisodeReferences,
    saveEpisodeManifest,
    type EpisodeItem,
    type EpisodeReference,
    type SeriesManifest,
  } from '@/lib/api/studio';
  import { FilePenLine, ImagePlus, Loader2, Save, Sparkles, Trash2, X } from 'lucide-svelte';

  interface Props {
    episode: EpisodeItem;
    series: SeriesManifest;
    seriesOrg: Organization | null;
    busy?: boolean;
    onSaved: () => Promise<void>;
    onRevisionSubmitted: (taskId: string) => Promise<void>;
  }

  let { episode, series, seriesOrg, busy = false, onSaved, onRevisionSubmitted }: Props = $props();

  let open = $state(false);
  let title = $state('');
  let synopsis = $state('');
  let script = $state('');
  let status = $state('draft');
  let references = $state<EpisodeReference[]>([]);
  let pending = $state<{ file: File; note: string }[]>([]);
  let removedPaths = $state<string[]>([]);
  let revisionBrief = $state('');
  let saving = $state(false);
  let revising = $state(false);

  function sync() {
    title = episode.manifest.title || '';
    synopsis = episode.manifest.synopsis || '';
    script = episode.manifest.script || '';
    status = episode.manifest.status || 'draft';
    references = (episode.manifest.references || []).map((ref) => ({ ...ref }));
    pending = [];
    removedPaths = [];
  }

  function toggle() {
    if (!open) sync();
    open = !open;
  }

  function addPending(files: FileList | null) {
    if (!files) return;
    pending = [...pending, ...Array.from(files).map((file) => ({ file, note: '' }))];
  }

  async function save(): Promise<boolean> {
    if (!title.trim()) {
      addToast('Episode title is required', 'alert');
      return false;
    }
    saving = true;
    try {
      let current: EpisodeItem = {
        ...episode,
        manifest: {
          ...episode.manifest,
          title: title.trim(),
          synopsis: synopsis.trim(),
          script,
          status,
          references,
        },
      };
      await saveEpisodeManifest(current.dir, current.manifest);
      if (pending.length) current = await addEpisodeReferences(current, pending);
      references = current.manifest.references || [];
      pending = [];
      await Promise.all(removedPaths.map(async (path) => {
        try {
          await deleteFile(path);
        } catch {
          /* The manifest is already safe; an orphan can be cleaned later. */
        }
      }));
      removedPaths = [];
      addToast('Episode changes saved', 'info');
      await onSaved();
      return true;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save episode', 'alert');
      return false;
    } finally {
      saving = false;
    }
  }

  function removeReference(ref: EpisodeReference) {
    references = references.filter((item) => item.id !== ref.id);
    removedPaths = [...removedPaths, ref.path];
  }

  async function reviseStoryboard() {
    if (!seriesOrg || !revisionBrief.trim()) {
      addToast('Describe what should change in the episode', 'alert');
      return;
    }
    revising = true;
    try {
      const hasGeneratedMedia = episode.manifest.shots.some((shot) => shot.still || shot.clip || shot.first_frame || shot.last_frame);
      if (hasGeneratedMedia && !confirm('Revising the storyboard replaces the shot plan and disconnects existing generated stills/clips. Continue?')) return;
      if (!(await save())) return;
      const visualNotes = references.map((ref) => `- ${ref.path}${ref.note ? `: ${ref.note}` : ''}`).join('\n');
      const res = await submitOrgTask(seriesOrg.id, {
        title: `Revise ${series.name} E${episode.manifest.number}: ${title.trim()}`,
        description: [
          `Revise episode ${episode.manifest.number} of series "${series.slug}".`,
          'Read series_get and episode_get first. Update the script, replace the planned storyboard with shots_set, then call episode_update status=scripted. Do not generate media.',
          `Requested changes:\n${revisionBrief.trim()}`,
          visualNotes ? `User-supplied visual references (follow each note):\n${visualNotes}` : '',
          'Preserve unaffected story beats and continuity with other episodes.',
        ].filter(Boolean).join('\n\n'),
      });
      revisionBrief = '';
      await onRevisionSubmitted(res.id);
      addToast(`Revision task ${res.identifier || ''} started`, 'info');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to start revision', 'alert');
    } finally {
      revising = false;
    }
  }

</script>

<div class="border-y border-gray-200 bg-white dark:border-dark-border dark:bg-dark-surface">
  <button onclick={toggle} aria-expanded={open} class="w-full px-3 py-2 flex items-center gap-2 text-left hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors">
    <FilePenLine size={13} class="text-gray-500 dark:text-dark-text-muted" />
    <span class="text-[11px] font-medium text-gray-800 dark:text-dark-text">Edit episode & references</span>
    <span class="ml-auto text-[10px] text-gray-400 dark:text-dark-text-muted">{references.length || episode.manifest.references?.length || 0} visual refs</span>
  </button>

  {#if open}
    <div class="border-t border-gray-100 dark:border-dark-border p-3 space-y-4">
      <div class="grid grid-cols-1 sm:grid-cols-[1fr_140px] gap-2">
        <label class="text-[10px] text-gray-500 dark:text-dark-text-muted">Title<input bind:value={title} class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text" /></label>
        <label class="text-[10px] text-gray-500 dark:text-dark-text-muted">Status<select bind:value={status} class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text"><option value="draft">Draft</option><option value="scripted">Scripted</option><option value="generating">Generating</option><option value="assembled">Assembled</option><option value="published">Published</option></select></label>
      </div>
      <label class="block text-[10px] text-gray-500 dark:text-dark-text-muted">Synopsis<textarea bind:value={synopsis} rows="2" class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text resize-y"></textarea></label>
      <label class="block text-[10px] text-gray-500 dark:text-dark-text-muted">Script<textarea bind:value={script} rows="8" placeholder="Full episode script" class="mt-0.5 w-full px-2 py-1.5 text-xs leading-5 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text resize-y"></textarea></label>

      <div>
        <div class="flex items-center justify-between gap-3 mb-2">
          <div><h4 class="text-[11px] font-semibold text-gray-800 dark:text-dark-text">Visual references</h4><p class="text-[10px] text-gray-400 dark:text-dark-text-muted">Attach a location, prop, wardrobe or mood image and say how it should be used.</p></div>
          <label class="shrink-0 inline-flex items-center gap-1 px-2 py-1 text-[10px] border border-gray-200 dark:border-dark-border cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-elevated"><ImagePlus size={11} /> Add images<input type="file" accept="image/*" multiple class="hidden" onchange={(e) => addPending((e.currentTarget as HTMLInputElement).files)} /></label>
        </div>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-2">
          {#each references as ref (ref.id)}
            <div class="flex gap-2 border border-gray-200 dark:border-dark-border p-1.5">
              <img src={fileServeUrl(ref.path, episode.manifest.updated_at)} alt={ref.note || 'Episode reference'} class="h-16 w-20 shrink-0 object-cover bg-gray-100 dark:bg-dark-elevated" />
              <textarea bind:value={ref.note} aria-label={`How to use ${ref.note || ref.id}`} rows="2" placeholder="Use this as…" class="min-w-0 flex-1 px-1.5 py-1 text-[10px] border border-gray-100 dark:border-dark-border bg-white dark:bg-dark-base resize-none"></textarea>
              <button onclick={() => removeReference(ref)} class="self-start p-2 text-gray-400 hover:text-red-500" title="Remove reference"><Trash2 size={11} /></button>
            </div>
          {/each}
          {#each pending as item, index (`${item.file.name}-${index}`)}
            <div class="flex gap-2 border border-dashed border-gray-300 dark:border-dark-border p-1.5">
              <div class="h-16 w-20 shrink-0 flex items-center justify-center bg-gray-100 dark:bg-dark-elevated text-gray-400"><ImagePlus size={16} /></div>
              <div class="min-w-0 flex-1"><p class="text-[9px] text-gray-400 truncate">{item.file.name}</p><textarea bind:value={item.note} aria-label={`How to use ${item.file.name}`} rows="2" placeholder="e.g. Use this lighting for scene 2" class="mt-0.5 w-full px-1.5 py-1 text-[10px] border border-gray-100 dark:border-dark-border bg-white dark:bg-dark-base resize-none"></textarea></div>
              <button onclick={() => (pending = pending.filter((_, i) => i !== index))} class="self-start p-2 text-gray-400 hover:text-red-500" title="Remove pending image"><X size={11} /></button>
            </div>
          {/each}
        </div>
      </div>

      <div class="flex justify-end"><button onclick={save} disabled={saving || busy} class="inline-flex items-center gap-1 px-3 py-1.5 text-[11px] font-medium bg-gray-900 dark:bg-accent text-white disabled:opacity-50">{#if saving}<Loader2 size={11} class="animate-spin" />{:else}<Save size={11} />{/if} Save changes</button></div>

      <div class="pt-3 border-t border-gray-100 dark:border-dark-border">
        <label class="block text-[10px] text-gray-500 dark:text-dark-text-muted">Revise storyboard with AI<textarea bind:value={revisionBrief} rows="3" placeholder="Example: Make scene 2 take place at night like the first reference, keep the dialogue, and add a close-up before the reveal." class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text resize-y"></textarea></label>
        <div class="mt-2 flex items-center justify-between gap-3"><p class="text-[10px] text-gray-400 dark:text-dark-text-muted">Updates the script and shot plan; it does not spend credits generating media.</p><button onclick={reviseStoryboard} disabled={revising || busy || !revisionBrief.trim()} class="shrink-0 inline-flex items-center gap-1 px-3 py-1.5 text-[11px] font-medium border border-gray-900 dark:border-accent text-gray-900 dark:text-dark-text disabled:opacity-40">{#if revising}<Loader2 size={11} class="animate-spin" />{:else}<Sparkles size={11} />{/if} Revise storyboard</button></div>
      </div>
    </div>
  {/if}
</div>
