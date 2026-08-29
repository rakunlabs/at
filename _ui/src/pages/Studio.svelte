<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { getInfo } from '@/lib/api/gateway';
  import { listOrganizations, type Organization } from '@/lib/api/organizations';
  import { listAgents, updateAgent } from '@/lib/api/agents';
  import { installSkillTemplate } from '@/lib/api/skills';
  import { installIntegrationPack } from '@/lib/api/integration-packs';
  import { loadCharacters, loadSeriesList, loadVoices, type CharacterItem, type VoiceItem } from '@/lib/api/studio';
  import StudioCharacters from '@/lib/components/studio/StudioCharacters.svelte';
  import StudioSeries from '@/lib/components/studio/StudioSeries.svelte';
  import StudioProductions from '@/lib/components/studio/StudioProductions.svelte';
  import { Clapperboard, Film, Loader2, RefreshCw, User, Video } from 'lucide-svelte';

  storeNavbar.title = 'Studio';

  type StudioTab = 'characters' | 'series' | 'productions';

  const AVATAR_ORG = 'Avatar Studio';
  const SERIES_ORG = 'Series Studio';
  const SKILL_TEMPLATES = [
    'fal-avatar',
    'fal-cinema',
    'ltx-video',
    'fal-image',
    'elevenlabs-voice',
    'openai-tts',
    'video-composer',
    'ffmpeg-guide',
    'series-library',
  ];
  const PACKS = ['avatar-studio', 'video-series'];
  const PACK_AGENTS = [
    'Studio Director',
    'Avatar Designer',
    'Video Producer',
    'Showrunner',
    'Script Writer',
    'Character Designer',
    'Scene Director',
    'Episode Editor',
  ];

  let loading = $state(true);
  let installing = $state(false);
  let tab = $state<StudioTab>('characters');
  let avatarOrg = $state<Organization | null>(null);
  let seriesOrg = $state<Organization | null>(null);
  let assetsRoot = $state('');
  let characters = $state<CharacterItem[]>([]);
  let voices = $state<VoiceItem[]>([]);
  let seriesCount = $state(0);
  let taskRefreshKey = $state(0);

  async function load() {
    loading = true;
    try {
      const [info, orgs] = await Promise.all([getInfo(), listOrganizations()]);
      assetsRoot = info.assets_root || '';
      avatarOrg = (orgs.data || []).find((o) => o.name === AVATAR_ORG) || null;
      seriesOrg = (orgs.data || []).find((o) => o.name === SERIES_ORG) || null;
      await reloadLibrary();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load studio', 'alert');
    } finally {
      loading = false;
    }
  }

  async function reloadLibrary() {
    if (!assetsRoot) return;
    const [nextCharacters, nextVoices, nextSeries] = await Promise.all([
      loadCharacters(assetsRoot),
      loadVoices(assetsRoot),
      loadSeriesList(assetsRoot),
    ]);
    characters = nextCharacters;
    voices = nextVoices;
    seriesCount = nextSeries.length;
  }

  function taskSubmitted() {
    taskRefreshKey += 1;
  }

  async function setupStudio() {
    installing = true;
    try {
      // Templates sync their handlers/system prompts on server startup; install
      // calls here are idempotent from the user's perspective.
      for (const slug of SKILL_TEMPLATES) {
        try {
          await installSkillTemplate(slug);
        } catch {
          /* already installed */
        }
      }
      for (const slug of PACKS) {
        try {
          await installIntegrationPack(slug, { skills: true, mcp_sets: false, organization: true });
        } catch {
          /* pack or organization may already exist */
        }
      }

      // Both packs intentionally ship provider/model empty.
      const info = await getInfo();
      const provider = info.providers?.[0];
      if (provider) {
        const model = provider.default_model || provider.models?.[0] || '';
        const agents = await listAgents();
        for (const agent of agents.data || []) {
          if (PACK_AGENTS.includes(agent.name) && !agent.config?.provider) {
            try {
              await updateAgent(agent.id, { config: { ...agent.config, provider: provider.key, model } } as any);
            } catch {
              /* leave this agent for manual provider assignment */
            }
          }
        }
      }
      addToast('Video studio installed', 'info');
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Setup failed', 'alert');
    } finally {
      installing = false;
    }
  }

  load();
</script>

<div class="h-full overflow-y-auto bg-gray-50 dark:bg-dark-base">
  {#if loading}
    <div class="flex items-center justify-center h-40 text-gray-400 dark:text-dark-text-muted"><Loader2 size={18} class="animate-spin" /></div>
  {:else if !avatarOrg && !seriesOrg}
    <div class="max-w-xl mx-auto mt-16 border-y border-gray-200 dark:border-dark-border py-10 text-center">
      <div class="mx-auto mb-4 h-12 w-12 bg-gray-900 dark:bg-dark-highest text-white dark:text-accent flex items-center justify-center"><Film size={23} /></div>
      <h2 class="text-lg font-semibold text-gray-900 dark:text-dark-text">Build a cast. Keep the continuity.</h2>
      <p class="mx-auto mt-2 max-w-md text-xs leading-5 text-gray-500 dark:text-dark-text-muted">
        Create recurring characters with a visual bible and bound voice, write episodes into a structured storyboard, generate identity-consistent shots, then assemble captioned final cuts.
      </p>
      <div class="mt-5 flex flex-wrap justify-center gap-x-5 gap-y-1 text-[10px] uppercase tracking-wide text-gray-400 dark:text-dark-text-muted">
        <span>Character bible</span><span>Style lock</span><span>Shot continuity</span><span>Structured renders</span>
      </div>
      <button onclick={setupStudio} disabled={installing} class="mt-6 inline-flex items-center gap-2 px-4 py-2 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors disabled:opacity-50">
        {#if installing}<Loader2 size={13} class="animate-spin" />{:else}<Clapperboard size={13} />{/if}
        {installing ? 'Installing studio…' : 'Set up Video Studio'}
      </button>
      <p class="mt-3 text-[10px] text-gray-400 dark:text-dark-text-muted">Requires a FAL connection. ElevenLabs and OpenAI are optional voice/transcription fallbacks.</p>
    </div>
  {:else}
    <div class="max-w-7xl mx-auto p-4 sm:p-6">
      <!-- Studio masthead: one compact operational overview, not a dashboard of cards. -->
      <header class="flex flex-col sm:flex-row sm:items-end gap-4 pb-4 border-b border-gray-200 dark:border-dark-border">
        <div class="min-w-0 flex-1">
          <div class="flex items-center gap-2">
            <div class="h-8 w-8 bg-gray-900 dark:bg-dark-highest text-white dark:text-accent flex items-center justify-center"><Film size={16} /></div>
            <div><h1 class="text-base font-semibold text-gray-900 dark:text-dark-text">Video Studio</h1><p class="text-[10px] text-gray-400 dark:text-dark-text-muted">Characters → episodes → shots → final cuts</p></div>
          </div>
        </div>
        <div class="flex items-center gap-4 text-[10px] text-gray-400 dark:text-dark-text-muted tabular-nums">
          <span><strong class="text-sm font-semibold text-gray-800 dark:text-dark-text">{characters.length}</strong> characters</span>
          <span><strong class="text-sm font-semibold text-gray-800 dark:text-dark-text">{seriesCount}</strong> series</span>
          <button onclick={() => reloadLibrary()} class="p-1 hover:text-gray-700 dark:hover:text-dark-text" title="Refresh libraries"><RefreshCw size={13} /></button>
          <button onclick={setupStudio} disabled={installing} class="inline-flex items-center gap-1 px-2 py-1 border border-gray-200 dark:border-dark-border hover:text-gray-700 dark:hover:text-dark-text disabled:opacity-50" title="Install newly added studio skills and refresh pack setup">{#if installing}<Loader2 size={10} class="animate-spin" />{:else}<RefreshCw size={10} />{/if} Sync studio</button>
        </div>
      </header>

      {#if !seriesOrg}
        <div class="mt-4 flex flex-col sm:flex-row sm:items-center gap-2 px-3 py-2 bg-amber-50 dark:bg-amber-950 text-amber-800 dark:text-amber-300">
          <p class="text-[11px] flex-1">Avatar Studio is installed, but the episodic Series Studio agents and cinema tools are missing.</p>
          <button onclick={setupStudio} disabled={installing} class="inline-flex items-center justify-center gap-1 px-2.5 py-1 text-[10px] font-medium bg-amber-900 text-white dark:bg-amber-300 dark:text-amber-950 disabled:opacity-50">{#if installing}<Loader2 size={10} class="animate-spin" />{/if} Upgrade studio</button>
        </div>
      {/if}

      <nav class="mt-5 flex gap-5 border-b border-gray-200 dark:border-dark-border" aria-label="Studio sections">
        <button onclick={() => (tab = 'characters')} class={['pb-2.5 text-xs font-medium border-b-2 -mb-px flex items-center gap-1.5 transition-colors', tab === 'characters' ? 'border-gray-900 dark:border-accent text-gray-900 dark:text-dark-text' : 'border-transparent text-gray-400 hover:text-gray-700 dark:hover:text-dark-text-secondary']}><User size={12} /> Characters</button>
        <button onclick={() => (tab = 'series')} class={['pb-2.5 text-xs font-medium border-b-2 -mb-px flex items-center gap-1.5 transition-colors', tab === 'series' ? 'border-gray-900 dark:border-accent text-gray-900 dark:text-dark-text' : 'border-transparent text-gray-400 hover:text-gray-700 dark:hover:text-dark-text-secondary']}><Film size={12} /> Series</button>
        <button onclick={() => (tab = 'productions')} class={['pb-2.5 text-xs font-medium border-b-2 -mb-px flex items-center gap-1.5 transition-colors', tab === 'productions' ? 'border-gray-900 dark:border-accent text-gray-900 dark:text-dark-text' : 'border-transparent text-gray-400 hover:text-gray-700 dark:hover:text-dark-text-secondary']}><Video size={12} /> Productions</button>
      </nav>

      <main class="mt-5">
        {#if tab === 'characters'}
          <StudioCharacters {assetsRoot} {characters} {voices} {seriesOrg} {avatarOrg} onReload={reloadLibrary} onTaskSubmitted={taskSubmitted} />
        {:else if tab === 'series'}
          <StudioSeries {assetsRoot} {characters} {seriesOrg} onTaskSubmitted={taskSubmitted} />
        {:else}
          <StudioProductions {assetsRoot} {avatarOrg} {seriesOrg} refreshKey={taskRefreshKey} />
        {/if}
      </main>
    </div>
  {/if}
</div>
