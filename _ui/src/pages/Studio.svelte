<script lang="ts">
  import { onDestroy } from 'svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { getInfo } from '@/lib/api/gateway';
  import { listOrganizations, submitOrgTask, type Organization } from '@/lib/api/organizations';
  import { listTasks, getTask, type Task } from '@/lib/api/tasks';
  import { listAgents, updateAgent } from '@/lib/api/agents';
  import { installSkillTemplate } from '@/lib/api/skills';
  import { installIntegrationPack } from '@/lib/api/integration-packs';
  import { browseFiles, fileServeUrl, fetchFileText, uploadFile, deleteFile, type FileEntry } from '@/lib/api/files';
  import { Clapperboard, Loader2, Plus, Trash2, RefreshCw, Video, Mic, User, Upload, ExternalLink, Play } from 'lucide-svelte';

  storeNavbar.title = 'Studio';

  const ORG_NAME = 'Avatar Studio';
  const PACK_SLUG = 'avatar-studio';
  const SKILL_TEMPLATES = ['fal-avatar', 'elevenlabs-voice', 'openai-tts', 'video-composer', 'ffmpeg-guide'];
  const PACK_AGENTS = ['Studio Director', 'Avatar Designer', 'Video Producer'];

  // ─── State ───
  let loading = $state(true);
  let installing = $state(false);
  let org = $state<Organization | null>(null);
  let assetsRoot = $state('');

  interface AvatarItem {
    name: string;
    path: string;
    modTime: string;
    description?: string;
  }
  let avatars = $state<AvatarItem[]>([]);
  let voices = $state<{ name: string; voice_id: string }[]>([]);

  // Create-avatar form
  let showAvatarForm = $state(false);
  let avatarName = $state('');
  let avatarPrompt = $state('');
  let avatarPhoto = $state<File | null>(null);
  let creatingAvatar = $state(false);

  // Generate-video form
  let selectedAvatar = $state('');
  let script = $state('');
  let voice = $state('');
  let quality = $state<'720p' | '1080p'>('720p');
  let generating = $state(false);

  // Productions
  let productions = $state<Task[]>([]);
  let pollTimer: ReturnType<typeof setInterval> | null = null;

  const IMG_EXT = ['.png', '.jpg', '.jpeg', '.webp'];

  // ─── Load ───

  async function load() {
    loading = true;
    try {
      const info = await getInfo();
      assetsRoot = info.assets_root || '';
      const orgs = await listOrganizations();
      org = (orgs.data || []).find((o) => o.name === ORG_NAME) || null;
      if (org) {
        await Promise.all([loadAvatars(), loadVoices(), loadProductions()]);
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load studio', 'alert');
    } finally {
      loading = false;
    }
  }

  async function loadAvatars() {
    if (!assetsRoot) return;
    try {
      const res = await browseFiles(`${assetsRoot}/avatars`);
      const entries = res.entries || [];
      const manifests = new Map<string, any>();
      for (const e of entries.filter((x) => !x.is_dir && x.name.endsWith('.json'))) {
        try {
          manifests.set(e.name.replace(/\.json$/, ''), JSON.parse(await fetchFileText(e.path)));
        } catch { /* ignore broken manifest */ }
      }
      avatars = entries
        .filter((e) => !e.is_dir && IMG_EXT.some((ext) => e.name.toLowerCase().endsWith(ext)))
        .map((e) => {
          const stem = e.name.replace(/\.[^.]+$/, '');
          return {
            name: manifests.get(stem)?.name || stem,
            path: e.path,
            modTime: e.mod_time,
            description: manifests.get(stem)?.description || '',
          };
        });
    } catch {
      avatars = []; // Directory does not exist yet — empty library.
    }
  }

  async function loadVoices() {
    if (!assetsRoot) return;
    try {
      const res = await browseFiles(`${assetsRoot}/voices`);
      const out: { name: string; voice_id: string }[] = [];
      for (const e of (res.entries || []).filter((x) => !x.is_dir && x.name.endsWith('.json'))) {
        try {
          const m = JSON.parse(await fetchFileText(e.path));
          if (m.voice_id) out.push({ name: m.name || e.name.replace(/\.json$/, ''), voice_id: m.voice_id });
        } catch { /* ignore */ }
      }
      voices = out;
    } catch {
      voices = [];
    }
  }

  async function loadProductions() {
    if (!org) return;
    try {
      const res = await listTasks({ 'organization_id': org.id, _sort: 'created_at:desc', _limit: 20 } as any);
      // Only root tasks (delegation children are noise here).
      productions = (res.data || []).filter((t) => !t.parent_id);
      schedulePoll();
    } catch { /* ignore */ }
  }

  const RUNNING = ['open', 'todo', 'in_progress', 'in_review'];

  function schedulePoll() {
    const anyRunning = productions.some((t) => RUNNING.includes(t.status));
    if (anyRunning && !pollTimer) {
      pollTimer = setInterval(refreshRunning, 5000);
    } else if (!anyRunning && pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
  }

  async function refreshRunning() {
    const running = productions.filter((t) => RUNNING.includes(t.status));
    if (running.length === 0) {
      schedulePoll();
      return;
    }
    let libraryChanged = false;
    for (const t of running) {
      try {
        const fresh = await getTask(t.id);
        if (fresh && fresh.status !== t.status) {
          productions = productions.map((p) => (p.id === fresh.id ? fresh : p));
          if (!RUNNING.includes(fresh.status)) libraryChanged = true;
        }
      } catch { /* ignore */ }
    }
    if (libraryChanged) {
      await Promise.all([loadAvatars(), loadVoices()]);
    }
    schedulePoll();
  }

  onDestroy(() => {
    if (pollTimer) clearInterval(pollTimer);
  });

  // ─── Setup ───

  async function setupStudio() {
    installing = true;
    try {
      // 1. Install the media skill templates (ignore "already installed").
      for (const slug of SKILL_TEMPLATES) {
        try {
          await installSkillTemplate(slug);
        } catch { /* already installed */ }
      }
      // 2. Install the integration pack (agents + organization).
      await installIntegrationPack(PACK_SLUG, { skills: true, mcp_sets: false, organization: true });
      // 3. Assign a provider/model to the new agents (pack ships them empty).
      const info = await getInfo();
      const provider = info.providers?.[0];
      if (provider) {
        const model = provider.default_model || provider.models?.[0] || '';
        const agents = await listAgents();
        for (const a of agents.data || []) {
          if (PACK_AGENTS.includes(a.name) && !a.config?.provider) {
            try {
              await updateAgent(a.id, { config: { ...a.config, provider: provider.key, model } } as any);
            } catch { /* leave for manual config */ }
          }
        }
      }
      addToast('Avatar Studio installed', 'info');
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Setup failed', 'alert');
    } finally {
      installing = false;
    }
  }

  // ─── Actions ───

  async function createAvatar() {
    if (!org || !avatarName.trim() || (!avatarPrompt.trim() && !avatarPhoto)) {
      addToast('Name and a prompt or photo are required', 'alert');
      return;
    }
    creatingAvatar = true;
    try {
      let photoPath = '';
      if (avatarPhoto) {
        const up = await uploadFile(avatarPhoto, `${assetsRoot}/uploads`);
        photoPath = up.path;
      }
      const lines = [
        `Create a new avatar and save it to the library as "${avatarName.trim()}".`,
        avatarPrompt.trim() ? `Portrait brief: ${avatarPrompt.trim()}` : '',
        photoPath ? `Use this photo as the identity reference (reference_image): ${photoPath}` : '',
        'Only create and save the avatar — no video in this task.',
      ].filter(Boolean);
      const res = await submitOrgTask(org.id, {
        title: `Create avatar: ${avatarName.trim()}`,
        description: lines.join('\n'),
      });
      addToast(`Avatar task ${res.identifier || ''} started`, 'info');
      showAvatarForm = false;
      avatarName = '';
      avatarPrompt = '';
      avatarPhoto = null;
      await loadProductions();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to start avatar task', 'alert');
    } finally {
      creatingAvatar = false;
    }
  }

  async function generateVideo() {
    if (!org || !selectedAvatar || !script.trim()) {
      addToast('Pick an avatar and write a script', 'alert');
      return;
    }
    generating = true;
    try {
      const lines = [
        `Produce a lip-synced talking-head video of the avatar "${selectedAvatar}".`,
        `Script (speak exactly this, in its original language):\n"""\n${script.trim()}\n"""`,
        voice ? `Voice: use the cloned/library voice "${voice}".` : 'Voice: pick a fitting default TTS voice.',
        `Resolution: ${quality}.`,
        'Deliver the final video_file path in the task result.',
      ];
      const res = await submitOrgTask(org.id, {
        title: `Avatar video: ${selectedAvatar} — ${script.trim().slice(0, 60)}`,
        description: lines.join('\n'),
      });
      addToast(`Video task ${res.identifier || ''} started`, 'info');
      script = '';
      await loadProductions();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to start video task', 'alert');
    } finally {
      generating = false;
    }
  }

  async function removeAvatar(a: AvatarItem) {
    if (!confirm(`Delete avatar "${a.name}"?`)) return;
    try {
      await deleteFile(a.path);
      const manifest = a.path.replace(/\.[^.]+$/, '.json');
      try { await deleteFile(manifest); } catch { /* no manifest */ }
      await loadAvatars();
      if (selectedAvatar === a.name) selectedAvatar = '';
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Delete failed', 'alert');
    }
  }

  /** Extract the final video path from a task result. */
  function videoPath(t: Task): string {
    if (!t.result) return '';
    const m = t.result.match(/(\/[^\s"'`)\]}]+\.(mp4|mov|webm))/);
    return m ? m[1] : '';
  }

  function statusColor(s: string): string {
    if (s === 'done' || s === 'completed') return 'text-green-600 dark:text-green-400';
    if (s === 'blocked' || s === 'cancelled' || s === 'failed') return 'text-red-500 dark:text-red-400';
    return 'text-amber-600 dark:text-amber-400';
  }

  load();
</script>

<div class="h-full overflow-y-auto p-4">
  {#if loading}
    <div class="flex items-center justify-center h-40 text-gray-400 dark:text-dark-text-muted">
      <Loader2 size={18} class="animate-spin" />
    </div>
  {:else if !org}
    <!-- Setup panel -->
    <div class="max-w-lg mx-auto mt-16 text-center border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-8">
      <Clapperboard size={32} class="mx-auto text-gray-300 dark:text-dark-text-faint mb-3" />
      <h2 class="text-base font-semibold text-gray-900 dark:text-dark-text mb-2">Avatar Studio</h2>
      <p class="text-xs text-gray-500 dark:text-dark-text-muted mb-1">
        Create persistent avatars from a prompt or your own photo, clone voices, and produce
        lip-synced talking-head videos (FAL OmniHuman + ElevenLabs).
      </p>
      <p class="text-[11px] text-gray-400 dark:text-dark-text-muted mb-5">
        Setup installs the media skills, three agents and the "{ORG_NAME}" organization.
        You'll need a <code>fal_api_key</code> (and optionally <code>elevenlabs_api_key</code>) in Connections or Variables.
      </p>
      <button
        onclick={setupStudio}
        disabled={installing}
        class="inline-flex items-center gap-2 px-4 py-2 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors disabled:opacity-50"
      >
        {#if installing}<Loader2 size={13} class="animate-spin" />{:else}<Clapperboard size={13} />{/if}
        {installing ? 'Installing…' : 'Set up Avatar Studio'}
      </button>
    </div>
  {:else}
    <div class="max-w-6xl mx-auto space-y-6">
      <!-- ── Avatar library ── -->
      <section>
        <div class="flex items-center justify-between mb-2">
          <h2 class="text-sm font-semibold text-gray-900 dark:text-dark-text flex items-center gap-1.5">
            <User size={14} /> Avatars
          </h2>
          <div class="flex items-center gap-2">
            <button onclick={() => Promise.all([loadAvatars(), loadVoices()])} class="p-1 text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary" title="Refresh">
              <RefreshCw size={13} />
            </button>
            <button
              onclick={() => (showAvatarForm = !showAvatarForm)}
              class="inline-flex items-center gap-1 px-2.5 py-1 text-[11px] font-medium border border-gray-300 dark:border-dark-border text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
            >
              <Plus size={12} /> New avatar
            </button>
          </div>
        </div>

        {#if showAvatarForm}
          <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-3 mb-3 space-y-2">
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-2">
              <input
                bind:value={avatarName}
                placeholder="Avatar name (e.g. ray)"
                class="px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text"
              />
              <label class="flex items-center gap-2 px-2 py-1.5 text-xs border border-dashed border-gray-300 dark:border-dark-border text-gray-500 dark:text-dark-text-muted cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-elevated">
                <Upload size={12} />
                <span class="truncate">{avatarPhoto ? avatarPhoto.name : 'Optional: your photo (identity reference)'}</span>
                <input type="file" accept="image/*" class="hidden" onchange={(e) => (avatarPhoto = (e.currentTarget as HTMLInputElement).files?.[0] || null)} />
              </label>
            </div>
            <textarea
              bind:value={avatarPrompt}
              rows="2"
              placeholder="Portrait brief — character, style, expression (e.g. 'friendly female doctor, warm smile, studio lighting, minimal background')"
              class="w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text resize-y"
            ></textarea>
            <div class="flex justify-end gap-2">
              <button onclick={() => (showAvatarForm = false)} class="px-2.5 py-1 text-[11px] text-gray-500 dark:text-dark-text-muted hover:underline">Cancel</button>
              <button
                onclick={createAvatar}
                disabled={creatingAvatar}
                class="inline-flex items-center gap-1 px-3 py-1 text-[11px] font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-50"
              >
                {#if creatingAvatar}<Loader2 size={11} class="animate-spin" />{/if} Create
              </button>
            </div>
          </div>
        {/if}

        {#if avatars.length === 0}
          <p class="text-xs text-gray-400 dark:text-dark-text-muted py-6 text-center border border-dashed border-gray-200 dark:border-dark-border">
            No avatars yet — create one to get started.
          </p>
        {:else}
          <div class="grid grid-cols-2 sm:grid-cols-4 lg:grid-cols-6 gap-3">
            {#each avatars as a (a.path)}
              <button
                onclick={() => (selectedAvatar = a.name)}
                class={[
                  'group relative text-left border transition-colors',
                  selectedAvatar === a.name
                    ? 'border-gray-900 dark:border-accent ring-1 ring-gray-900 dark:ring-accent'
                    : 'border-gray-200 dark:border-dark-border hover:border-gray-400 dark:hover:border-dark-text-muted',
                ]}
                title={a.description || a.name}
              >
                <img src={fileServeUrl(a.path, a.modTime)} alt={a.name} class="w-full aspect-[3/4] object-cover bg-gray-100 dark:bg-dark-elevated" />
                <div class="px-1.5 py-1 text-[11px] font-medium text-gray-700 dark:text-dark-text-secondary truncate">{a.name}</div>
                <span
                  role="button"
                  tabindex="0"
                  onclick={(e) => { e.stopPropagation(); removeAvatar(a); }}
                  onkeydown={(e) => { if (e.key === 'Enter') { e.stopPropagation(); removeAvatar(a); } }}
                  class="absolute top-1 right-1 p-1 bg-white/80 dark:bg-dark-base/80 text-gray-400 hover:text-red-500 opacity-0 group-hover:opacity-100 transition-opacity"
                  title="Delete avatar"
                >
                  <Trash2 size={11} />
                </span>
              </button>
            {/each}
          </div>
        {/if}
      </section>

      <!-- ── Generate video ── -->
      <section>
        <h2 class="text-sm font-semibold text-gray-900 dark:text-dark-text flex items-center gap-1.5 mb-2">
          <Video size={14} /> Generate video
        </h2>
        <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-3 space-y-2">
          <div class="grid grid-cols-1 sm:grid-cols-3 gap-2">
            <div class="text-xs text-gray-600 dark:text-dark-text-secondary flex items-center gap-1.5">
              <User size={12} class="shrink-0" />
              {#if selectedAvatar}
                <span>Avatar: <strong>{selectedAvatar}</strong></span>
              {:else}
                <span class="text-gray-400 dark:text-dark-text-muted">Select an avatar above</span>
              {/if}
            </div>
            <div class="flex items-center gap-1.5">
              <Mic size={12} class="shrink-0 text-gray-400" />
              <select bind:value={voice} class="flex-1 px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text">
                <option value="">Default TTS voice</option>
                {#each voices as v (v.voice_id)}
                  <option value={v.name}>{v.name} (cloned)</option>
                {/each}
              </select>
            </div>
            <select bind:value={quality} class="px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text">
              <option value="720p">720p (up to 60s)</option>
              <option value="1080p">1080p (up to 30s)</option>
            </select>
          </div>
          <textarea
            bind:value={script}
            rows="3"
            placeholder="Script — exactly what the avatar should say (≤ 60 seconds spoken; longer scripts are chunked automatically)"
            class="w-full px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base text-gray-900 dark:text-dark-text resize-y"
          ></textarea>
          <div class="flex justify-end">
            <button
              onclick={generateVideo}
              disabled={generating || !selectedAvatar || !script.trim()}
              class="inline-flex items-center gap-1.5 px-4 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-50"
            >
              {#if generating}<Loader2 size={12} class="animate-spin" />{:else}<Play size={12} />{/if}
              Generate
            </button>
          </div>
          <p class="text-[10px] text-gray-400 dark:text-dark-text-muted">
            Lip-sync is billed per second of audio (FAL OmniHuman). Cloned voices need an ElevenLabs key.
          </p>
        </div>
      </section>

      <!-- ── Productions ── -->
      <section>
        <div class="flex items-center justify-between mb-2">
          <h2 class="text-sm font-semibold text-gray-900 dark:text-dark-text flex items-center gap-1.5">
            <Clapperboard size={14} /> Productions
          </h2>
          <button onclick={loadProductions} class="p-1 text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary" title="Refresh">
            <RefreshCw size={13} />
          </button>
        </div>
        {#if productions.length === 0}
          <p class="text-xs text-gray-400 dark:text-dark-text-muted py-6 text-center border border-dashed border-gray-200 dark:border-dark-border">
            No productions yet.
          </p>
        {:else}
          <div class="space-y-2">
            {#each productions as t (t.id)}
              {@const vp = videoPath(t)}
              <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-3">
                <div class="flex items-center gap-2">
                  {#if RUNNING.includes(t.status)}
                    <Loader2 size={12} class="animate-spin text-amber-500 shrink-0" />
                  {/if}
                  <a href={`#/tasks/${t.id}`} class="text-xs font-medium text-gray-900 dark:text-dark-text hover:underline truncate">
                    {t.identifier ? `${t.identifier} — ` : ''}{t.title}
                  </a>
                  <span class={['ml-auto text-[10px] font-medium uppercase shrink-0', statusColor(t.status)]}>{t.status}</span>
                  <a href={`#/tasks/${t.id}`} class="text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary shrink-0" title="Open task">
                    <ExternalLink size={11} />
                  </a>
                </div>
                {#if vp}
                  <!-- svelte-ignore a11y_media_has_caption -->
                  <video controls preload="metadata" src={fileServeUrl(vp)} class="mt-2 max-h-72 bg-black"></video>
                {/if}
              </div>
            {/each}
          </div>
        {/if}
      </section>
    </div>
  {/if}
</div>
