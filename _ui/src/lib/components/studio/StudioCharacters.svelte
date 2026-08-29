<script lang="ts">
  import { addToast } from '@/lib/store/toast.svelte';
  import { submitOrgTask, type Organization } from '@/lib/api/organizations';
  import { fileServeUrl, uploadFile, deleteFile } from '@/lib/api/files';
  import {
    saveCharacterManifest,
    characterAssetPath,
    type CharacterItem,
    type VoiceItem,
  } from '@/lib/api/studio';
  import { Loader2, Plus, Trash2, RefreshCw, Upload, User, Mic, Video, Play, Sparkles, X, Save } from 'lucide-svelte';

  interface Props {
    assetsRoot: string;
    characters: CharacterItem[];
    voices: VoiceItem[];
    seriesOrg: Organization | null;
    avatarOrg: Organization | null;
    onReload: () => Promise<void>;
    onTaskSubmitted: () => void;
  }
  let { assetsRoot, characters, voices, seriesOrg, avatarOrg, onReload, onTaskSubmitted }: Props = $props();

  // Create-character form
  let showForm = $state(false);
  let charName = $state('');
  let charPrompt = $state('');
  let charPhoto = $state<File | null>(null);
  let makeSheet = $state(true);
  let creating = $state(false);

  // Bible editor
  let selected = $state<CharacterItem | null>(null);
  let editVoice = $state('');
  let editWardrobe = $state('');
  let editStyleNotes = $state('');
  let editPersona = $state('');
  let editDescription = $state('');
  let saving = $state(false);
  let busyAction = $state('');

  // Quick talking-head video (Avatar Studio flow)
  let quickScript = $state('');
  let quickVoice = $state('');
  let quickQuality = $state<'720p' | '1080p'>('720p');
  let quickGenerating = $state(false);

  const taskOrg = () => seriesOrg || avatarOrg;

  function openEditor(c: CharacterItem) {
    selected = c;
    editVoice = c.manifest.voice || '';
    editWardrobe = c.manifest.wardrobe || '';
    editStyleNotes = c.manifest.style_notes || '';
    editPersona = c.manifest.persona || '';
    editDescription = c.manifest.description || '';
  }

  async function createCharacter() {
    const org = taskOrg();
    if (!org || !charName.trim() || (!charPrompt.trim() && !charPhoto)) {
      addToast('Name and a prompt or photo are required', 'alert');
      return;
    }
    creating = true;
    try {
      let photoPath = '';
      if (charPhoto) {
        const up = await uploadFile(charPhoto, `${assetsRoot}/uploads`);
        photoPath = up.path;
      }
      const lines = [
        `Create a new character and save it to the library as "${charName.trim()}".`,
        charPrompt.trim() ? `Portrait brief: ${charPrompt.trim()}` : '',
        photoPath ? `Use this photo as the identity reference (reference_image): ${photoPath}` : '',
        makeSheet ? 'After saving the portrait, generate the turnaround sheet with character_sheet (front, profile, back).' : '',
        'Only create the character (portrait + bible) — no video in this task.',
      ].filter(Boolean);
      const res = await submitOrgTask(org.id, {
        title: `Create character: ${charName.trim()}`,
        description: lines.join('\n'),
      });
      addToast(`Character task ${res.identifier || ''} started`, 'info');
      showForm = false;
      charName = '';
      charPrompt = '';
      charPhoto = null;
      onTaskSubmitted();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to start character task', 'alert');
    } finally {
      creating = false;
    }
  }

  async function saveBible() {
    if (!selected) return;
    saving = true;
    try {
      await saveCharacterManifest(assetsRoot, {
        ...selected.manifest,
        voice: editVoice,
        wardrobe: editWardrobe,
        style_notes: editStyleNotes,
        persona: editPersona,
        description: editDescription,
      });
      addToast('Character saved', 'info');
      await onReload();
      selected = null;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Save failed', 'alert');
    } finally {
      saving = false;
    }
  }

  async function runCharacterAction(action: 'turnarounds' | 'sora') {
    const org = taskOrg();
    if (!selected || !org) return;
    busyAction = action;
    const name = selected.manifest.name;
    try {
      const brief =
        action === 'turnarounds'
          ? `Generate the turnaround sheet for the existing library character "${name}" using character_sheet (front, profile, back). Do not recreate the portrait.`
          : `Register the existing library character "${name}" in the Sora 2 character registry: render a short (max 4 seconds of audio, 720p+) talking_video of the character's portrait, then call sora_create_character with it. Do not recreate the portrait.`;
      const res = await submitOrgTask(org.id, {
        title: action === 'turnarounds' ? `Turnarounds: ${name}` : `Sora character: ${name}`,
        description: brief,
      });
      addToast(`Task ${res.identifier || ''} started`, 'info');
      onTaskSubmitted();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to start task', 'alert');
    } finally {
      busyAction = '';
    }
  }

  async function removeCharacter(c: CharacterItem) {
    if (!confirm(`Delete character "${c.manifest.name}" (image + manifest + turnarounds)?`)) return;
    try {
      if (c.imagePath) await deleteFile(c.imagePath);
      for (const t of c.manifest.turnarounds || []) {
        try {
          await deleteFile(characterAssetPath(assetsRoot, t));
        } catch {
          /* already gone */
        }
      }
      try {
        await deleteFile(c.manifestPath);
      } catch {
        /* no manifest */
      }
      if (selected?.manifest.slug === c.manifest.slug) selected = null;
      await onReload();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Delete failed', 'alert');
    }
  }

  async function quickVideo() {
    const org = avatarOrg || seriesOrg;
    if (!org || !selected || !quickScript.trim()) {
      addToast('Select a character and write a script', 'alert');
      return;
    }
    quickGenerating = true;
    try {
      const name = selected.manifest.name;
      const boundVoice = quickVoice || selected.manifest.voice || '';
      const lines = [
        `Produce a lip-synced talking-head video of the avatar "${name}".`,
        `Script (speak exactly this, in its original language):\n"""\n${quickScript.trim()}\n"""`,
        boundVoice ? `Voice: use the cloned/library voice "${boundVoice}".` : 'Voice: pick a fitting default TTS voice.',
        `Resolution: ${quickQuality}.`,
        'Deliver the final video_file path in the task result.',
      ];
      const res = await submitOrgTask(org.id, {
        title: `Avatar video: ${name} — ${quickScript.trim().slice(0, 60)}`,
        description: lines.join('\n'),
      });
      addToast(`Video task ${res.identifier || ''} started`, 'info');
      quickScript = '';
      onTaskSubmitted();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to start video task', 'alert');
    } finally {
      quickGenerating = false;
    }
  }

  const readiness = (c: CharacterItem): string[] => {
    const missing: string[] = [];
    if (!(c.manifest.turnarounds || []).length) missing.push('turnarounds');
    if (!c.manifest.voice) missing.push('voice');
    return missing;
  };
</script>

<div class="space-y-6">
  <!-- ── Character gallery ── -->
  <section>
    <div class="flex items-center justify-between mb-2">
      <h2 class="text-sm font-semibold text-gray-900 dark:text-dark-text flex items-center gap-1.5">
        <User size={14} /> Characters
      </h2>
      <div class="flex items-center gap-2">
        <button onclick={() => onReload()} class="p-1 text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary" title="Refresh">
          <RefreshCw size={13} />
        </button>
        <button
          onclick={() => (showForm = !showForm)}
          class="inline-flex items-center gap-1 px-2.5 py-1 text-[11px] font-medium border border-gray-300 dark:border-dark-border text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
        >
          <Plus size={12} /> New character
        </button>
      </div>
    </div>

    {#if showForm}
      <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-3 mb-3 space-y-2">
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-2">
          <input
            bind:value={charName}
            placeholder="Character name (e.g. maya)"
            class="px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text"
          />
          <label class="flex items-center gap-2 px-2 py-1.5 text-xs border border-dashed border-gray-300 dark:border-dark-border text-gray-500 dark:text-dark-text-muted cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-elevated">
            <Upload size={12} />
            <span class="truncate">{charPhoto ? charPhoto.name : 'Optional: photo (identity reference)'}</span>
            <input type="file" accept="image/*" class="hidden" onchange={(e) => (charPhoto = (e.currentTarget as HTMLInputElement).files?.[0] || null)} />
          </label>
        </div>
        <textarea
          bind:value={charPrompt}
          rows="2"
          placeholder="Portrait brief — identity, age, wardrobe, mood (e.g. 'female detective, ~35, worn red coat, tired eyes, neon-lit night')"
          class="w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text resize-y"
        ></textarea>
        <label class="flex items-center gap-1.5 text-[11px] text-gray-600 dark:text-dark-text-secondary">
          <input type="checkbox" bind:checked={makeSheet} class="accent-gray-900 dark:accent-accent" />
          Also generate the turnaround sheet (front / profile / back) — needed for scene consistency
        </label>
        <div class="flex justify-end gap-2">
          <button onclick={() => (showForm = false)} class="px-2.5 py-1 text-[11px] text-gray-500 dark:text-dark-text-muted hover:underline">Cancel</button>
          <button
            onclick={createCharacter}
            disabled={creating}
            class="inline-flex items-center gap-1 px-3 py-1 text-[11px] font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-50"
          >
            {#if creating}<Loader2 size={11} class="animate-spin" />{/if} Create
          </button>
        </div>
      </div>
    {/if}

    {#if characters.length === 0}
      <p class="text-xs text-gray-400 dark:text-dark-text-muted py-6 text-center border border-dashed border-gray-200 dark:border-dark-border">
        No characters yet — create one to get started.
      </p>
    {:else}
      <div class="grid grid-cols-2 sm:grid-cols-4 lg:grid-cols-6 gap-3">
        {#each characters as c (c.manifest.slug)}
          {@const missing = readiness(c)}
          <button
            onclick={() => openEditor(c)}
            class={[
              'group relative text-left border transition-colors',
              selected?.manifest.slug === c.manifest.slug
                ? 'border-gray-900 dark:border-accent ring-1 ring-gray-900 dark:ring-accent'
                : 'border-gray-200 dark:border-dark-border hover:border-gray-400 dark:hover:border-dark-text-muted',
            ]}
            title={c.manifest.description || c.manifest.name}
          >
            {#if c.imagePath}
              <img src={fileServeUrl(c.imagePath, c.manifest.updated_at || c.modTime)} alt={c.manifest.name} class="w-full aspect-[3/4] object-cover bg-gray-100 dark:bg-dark-elevated" />
            {:else}
              <div class="w-full aspect-[3/4] flex items-center justify-center bg-gray-100 dark:bg-dark-elevated text-gray-300 dark:text-dark-text-faint"><User size={24} /></div>
            {/if}
            <div class="px-1.5 py-1">
              <div class="text-[11px] font-medium text-gray-700 dark:text-dark-text-secondary truncate">{c.manifest.name}</div>
              <div class="flex items-center gap-1 text-[9px] text-gray-400 dark:text-dark-text-muted">
                {#if c.manifest.voice}<Mic size={8} />{/if}
                {#if (c.manifest.turnarounds || []).length}<span>{(c.manifest.turnarounds || []).length + 1} refs</span>{/if}
                {#if c.manifest.sora_character_id}<Sparkles size={8} />{/if}
                {#if missing.length}<span class="text-amber-500" title={`Missing: ${missing.join(', ')}`}>•</span>{/if}
              </div>
            </div>
            <span
              role="button"
              tabindex="0"
              onclick={(e) => { e.stopPropagation(); removeCharacter(c); }}
              onkeydown={(e) => { if (e.key === 'Enter') { e.stopPropagation(); removeCharacter(c); } }}
              class="absolute top-1 right-1 p-1 bg-white/80 dark:bg-dark-base/80 text-gray-400 hover:text-red-500 opacity-0 group-hover:opacity-100 transition-opacity"
              title="Delete character"
            >
              <Trash2 size={11} />
            </span>
          </button>
        {/each}
      </div>
    {/if}
  </section>

  <!-- ── Bible editor ── -->
  {#if selected}
    <section class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
      <div class="flex items-center justify-between px-3 py-2 border-b border-gray-100 dark:border-dark-border">
        <h3 class="text-xs font-semibold text-gray-900 dark:text-dark-text">
          Character bible — {selected.manifest.name}
        </h3>
        <button onclick={() => (selected = null)} class="text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary"><X size={13} /></button>
      </div>
      <div class="p-3 grid grid-cols-1 lg:grid-cols-[200px_1fr] gap-4">
        <div class="space-y-2">
          {#if selected.imagePath}
            <img src={fileServeUrl(selected.imagePath, selected.manifest.updated_at || selected.modTime)} alt={selected.manifest.name} class="w-full aspect-[3/4] object-cover bg-gray-100 dark:bg-dark-elevated" />
          {/if}
          {#if (selected.manifest.turnarounds || []).length}
            <div class="flex gap-1 overflow-x-auto">
              {#each selected.manifest.turnarounds || [] as t (t)}
                <img src={fileServeUrl(characterAssetPath(assetsRoot, t), selected.manifest.updated_at)} alt={t} class="h-16 aspect-[3/4] object-cover bg-gray-100 dark:bg-dark-elevated shrink-0" title={t} />
              {/each}
            </div>
          {/if}
          <div class="flex flex-col gap-1">
            <button
              onclick={() => runCharacterAction('turnarounds')}
              disabled={busyAction !== ''}
              class="inline-flex items-center justify-center gap-1 px-2 py-1 text-[10px] font-medium border border-gray-300 dark:border-dark-border text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated disabled:opacity-50"
            >
              {#if busyAction === 'turnarounds'}<Loader2 size={10} class="animate-spin" />{/if}
              {(selected.manifest.turnarounds || []).length ? 'Regenerate turnarounds' : 'Generate turnarounds'}
            </button>
            <button
              onclick={() => runCharacterAction('sora')}
              disabled={busyAction !== ''}
              class="inline-flex items-center justify-center gap-1 px-2 py-1 text-[10px] font-medium border border-gray-300 dark:border-dark-border text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated disabled:opacity-50"
              title={selected.manifest.sora_character_id ? `Registered: ${selected.manifest.sora_character_id}` : 'Register in the Sora 2 character registry'}
            >
              {#if busyAction === 'sora'}<Loader2 size={10} class="animate-spin" />{:else}<Sparkles size={10} />{/if}
              {selected.manifest.sora_character_id ? 'Re-register Sora character' : 'Register Sora character'}
            </button>
          </div>
        </div>
        <div class="space-y-2">
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-2">
            <label class="text-[10px] text-gray-500 dark:text-dark-text-muted">
              Bound voice (dialogue TTS)
              <select bind:value={editVoice} class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text">
                <option value="">— none —</option>
                {#each voices as v (v.voice_id)}
                  <option value={v.slug}>{v.name} (cloned)</option>
                {/each}
              </select>
            </label>
            <label class="text-[10px] text-gray-500 dark:text-dark-text-muted">
              Description
              <input bind:value={editDescription} class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text" />
            </label>
          </div>
          <label class="block text-[10px] text-gray-500 dark:text-dark-text-muted">
            Wardrobe (canonical outfit — appended to every scene prompt)
            <input bind:value={editWardrobe} placeholder="e.g. worn red trench coat, silver pendant" class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text" />
          </label>
          <label class="block text-[10px] text-gray-500 dark:text-dark-text-muted">
            Style notes (lighting / grade / lens for this character)
            <input bind:value={editStyleNotes} placeholder="e.g. soft cinematic key light, teal-orange grade" class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text" />
          </label>
          <label class="block text-[10px] text-gray-500 dark:text-dark-text-muted">
            Persona (speech style — used by the Script Writer)
            <textarea bind:value={editPersona} rows="2" placeholder="e.g. sarcastic, short sentences, warm underneath" class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text resize-y"></textarea>
          </label>
          {#if selected.manifest.sora_character_id}
            <p class="text-[10px] text-gray-400 dark:text-dark-text-muted">Sora: <code>{selected.manifest.sora_character_id}</code>{selected.manifest.sora_character_name ? ` (“${selected.manifest.sora_character_name}” in prompts)` : ''}</p>
          {/if}
          <div class="flex justify-end">
            <button
              onclick={saveBible}
              disabled={saving}
              class="inline-flex items-center gap-1 px-3 py-1 text-[11px] font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-50"
            >
              {#if saving}<Loader2 size={11} class="animate-spin" />{:else}<Save size={11} />{/if} Save bible
            </button>
          </div>
        </div>
      </div>
    </section>

    <!-- ── Quick talking-head video ── -->
    <section>
      <h2 class="text-sm font-semibold text-gray-900 dark:text-dark-text flex items-center gap-1.5 mb-2">
        <Video size={14} /> Quick talking-head video — {selected.manifest.name}
      </h2>
      <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-3 space-y-2">
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-2">
          <div class="flex items-center gap-1.5">
            <Mic size={12} class="shrink-0 text-gray-400" />
            <select bind:value={quickVoice} class="flex-1 px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text">
              <option value="">{selected.manifest.voice ? `Bound voice (${selected.manifest.voice})` : 'Default TTS voice'}</option>
              {#each voices as v (v.voice_id)}
                <option value={v.name}>{v.name} (cloned)</option>
              {/each}
            </select>
          </div>
          <select bind:value={quickQuality} class="px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text">
            <option value="720p">720p (up to 60s)</option>
            <option value="1080p">1080p (up to 30s)</option>
          </select>
        </div>
        <textarea
          bind:value={quickScript}
          rows="3"
          placeholder="Script — exactly what the character should say (≤ 60 seconds spoken; longer scripts are chunked automatically)"
          class="w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text resize-y"
        ></textarea>
        <div class="flex justify-end">
          <button
            onclick={quickVideo}
            disabled={quickGenerating || !quickScript.trim()}
            class="inline-flex items-center gap-1.5 px-4 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-50"
          >
            {#if quickGenerating}<Loader2 size={12} class="animate-spin" />{:else}<Play size={12} />{/if}
            Generate
          </button>
        </div>
        <p class="text-[10px] text-gray-400 dark:text-dark-text-muted">
          Lip-sync is billed per second of audio (FAL OmniHuman). Cloned voices need an ElevenLabs key.
        </p>
      </div>
    </section>
  {/if}
</div>
