<script lang="ts">
  import { routeChoice } from '@/lib/helper/route-choice.svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { getInfo } from '@/lib/api/gateway';
  import { listOrganizations, listOrgAgents, type Organization } from '@/lib/api/organizations';
  import { getAgent, updateAgent, type Agent } from '@/lib/api/agents';
  import { installSkillTemplate, listSkills } from '@/lib/api/skills';
  import { installIntegrationPack } from '@/lib/api/integration-packs';
  import { loadCharacters, loadSeriesList, loadStudioToolkit, loadVoices, saveStudioToolkit, type CharacterItem, type VoiceItem } from '@/lib/api/studio';
  import {
    STUDIO_CAPABILITIES,
    applyToolkitToAgent,
    capabilityConfigured,
    createStudioToolkit,
    isStudioProductionAgent,
    toolkitSources,
    type StudioToolkit as Toolkit,
  } from '@/lib/helper/studio-toolkit';
  import StudioCharacters from '@/lib/components/studio/StudioCharacters.svelte';
  import StudioSeries from '@/lib/components/studio/StudioSeries.svelte';
  import StudioProductions from '@/lib/components/studio/StudioProductions.svelte';
  import StudioLongForm from '@/lib/components/studio/StudioLongForm.svelte';
  import StudioToolkit from '@/lib/components/studio/StudioToolkit.svelte';
  import { Clapperboard, Film, Loader2, RefreshCw, User, Video, Wrench } from 'lucide-svelte';

  storeNavbar.title = 'Studio';

  type StudioTab = 'characters' | 'series' | 'long-videos' | 'productions' | 'toolkit';

  const AVATAR_ORG = 'Avatar Studio';
  const SERIES_ORG = 'Series Studio';
  const LONG_VIDEO_ORG = 'Long Video Studio';
  const SKILL_TEMPLATES = ['studio-library', 'series-library', 'video-composer', 'ffmpeg-guide'];
  const PACKS = [
    { slug: 'avatar-studio', name: AVATAR_ORG },
    { slug: 'video-series', name: SERIES_ORG },
    { slug: 'long-form-video', name: LONG_VIDEO_ORG },
  ];

  let loading = $state(true);
  let installing = $state(false);
  const tabRoute = routeChoice<StudioTab>('tab', ['characters', 'series', 'long-videos', 'productions', 'toolkit'], 'characters');
  let tab = $derived(tabRoute.value);
  let avatarOrg = $state<Organization | null>(null);
  let seriesOrg = $state<Organization | null>(null);
  let longVideoOrg = $state<Organization | null>(null);
  let assetsRoot = $state('');
  let characters = $state<CharacterItem[]>([]);
  let voices = $state<VoiceItem[]>([]);
  let seriesCount = $state(0);
  let taskRefreshKey = $state(0);
  let toolkit = $state<Toolkit>(createStudioToolkit());
  let studioAgents = $state<Agent[]>([]);

  let configuredCount = $derived(STUDIO_CAPABILITIES.filter((cap) => capabilityConfigured(toolkit.capabilities[cap])).length);
  let toolkitEmpty = $derived(configuredCount === 0);
  // Teams installed before the toolkit carry vendor skills and no studio_library.
  let legacyAgents = $derived(studioAgents.filter((agent) => (agent.config?.skills || []).length > 0 && !isStudioProductionAgent(agent.config)));

  async function load() {
    loading = true;
    try {
      const [info, orgs] = await Promise.all([
        getInfo(),
        Promise.all(PACKS.map((pack) => listOrganizations({ name: pack.name, _limit: 1 }))),
      ]);
      assetsRoot = info.assets_root || '';
      avatarOrg = orgs[0].data?.[0] || null;
      seriesOrg = orgs[1].data?.[0] || null;
      longVideoOrg = orgs[2].data?.[0] || null;
      await Promise.all([reloadLibrary(), reloadToolkit(), reloadAgents()]);
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

  async function reloadToolkit() {
    if (!assetsRoot) return;
    try {
      toolkit = await loadStudioToolkit(assetsRoot);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load the studio toolkit', 'alert');
    }
  }

  /** Members of the studio teams; names are not unique, so only these agents are touched. */
  async function reloadAgents(): Promise<Agent[]> {
    const ids = [avatarOrg?.id, seriesOrg?.id, longVideoOrg?.id].filter((id): id is string => !!id);
    const memberships = await Promise.all(ids.map((id) => listOrgAgents(id)));
    const agentIds = [...new Set(memberships.flatMap((members) => members.map((member) => member.agent_id)))];
    studioAgents = await Promise.all(agentIds.map((id) => getAgent(id)));
    return studioAgents;
  }

  /**
   * Attaches the toolkit's sources to every studio production agent. `applied`
   * only advances when every agent was updated, so a retry still detaches
   * sources that were removed.
   */
  async function applyToolkit(next: Toolkit, agents: Agent[]): Promise<{ toolkit: Toolkit; failures: string[] }> {
    const sources = toolkitSources(next);
    const failures: string[] = [];
    for (const agent of agents) {
      if (!isStudioProductionAgent(agent.config)) continue;
      const config = applyToolkitToAgent(agent.config, next.applied, sources);
      if (!config) continue;
      try {
        await updateAgent(agent.id, { config });
      } catch (e: any) {
        failures.push(`${agent.name}: ${e?.response?.data?.message || e?.message || 'update failed'}`);
      }
    }
    return { toolkit: failures.length ? next : { ...next, applied: sources }, failures };
  }

  async function saveToolkit(next: Toolkit) {
    if (!assetsRoot) throw new Error('The asset library is unavailable.');
    const result = await applyToolkit(next, await reloadAgents());
    toolkit = await saveStudioToolkit(assetsRoot, result.toolkit);
    await reloadAgents();
    if (result.failures.length) throw new Error(`Toolkit saved, but some agents were not updated: ${result.failures.join('; ')}`);
    addToast('Toolkit saved and attached to the studio agents', 'info');
  }

  function taskSubmitted() {
    taskRefreshKey += 1;
  }

  async function setupStudio() {
    installing = true;
    try {
      const info = await getInfo();
      const providers = (info.providers || []).map((provider) => ({
        key: provider.key,
        model: provider.default_model?.trim() || provider.models?.find((model) => model.trim())?.trim() || '',
      }));
      const fallback = providers.find((provider) => provider.key?.trim() && provider.model);
      if (!fallback) throw new Error('Configure a provider with a usable model before setting up the Studio.');

      // Install only missing entries: the pack installer creates new teams on every call.
      for (const slug of SKILL_TEMPLATES) {
        const existing = await listSkills({ name: slug.replaceAll('-', '_'), _limit: 1 });
        if (!existing.data?.length) await installSkillTemplate(slug);
      }
      const selectedOrgs = [avatarOrg, seriesOrg, longVideoOrg];
      for (const [index, pack] of PACKS.entries()) {
        let organizationId = selectedOrgs[index]?.id;
        if (!organizationId) {
          const existing = await listOrganizations({ name: pack.name, _limit: 1 });
          organizationId = existing.data?.[0]?.id;
        }
        if (!organizationId) {
          const result = await installIntegrationPack(pack.slug, { skills: true, mcp_sets: false, organization: true });
          if (!result.organization_id || !result.agents_created) throw new Error(`${pack.name} setup was incomplete. Check Organizations before retrying.`);
        }
      }
      const orgs = await Promise.all(PACKS.map((pack) => listOrganizations({ name: pack.name, _limit: 1 })));
      avatarOrg = orgs[0].data?.[0] || null;
      seriesOrg = orgs[1].data?.[0] || null;
      longVideoOrg = orgs[2].data?.[0] || null;

      const agents = await reloadAgents();
      for (const agent of agents) {
        if (agent.config?.provider?.trim() && agent.config?.model?.trim()) continue;
        const provider = agent.config?.provider?.trim()
          ? providers.find((provider) => provider.key === agent.config.provider)
          : fallback;
        if (!provider?.model) {
          throw new Error(`No usable model for ${agent.name} (${agent.id}) with provider ${agent.config?.provider}. Configure that provider before retrying.`);
        }
        await updateAgent(agent.id, { config: { ...agent.config, provider: provider.key, model: provider.model } });
      }
      await reloadToolkit();
      if (assetsRoot && !toolkitEmpty) {
        const result = await applyToolkit(toolkit, await reloadAgents());
        toolkit = await saveStudioToolkit(assetsRoot, result.toolkit);
        if (result.failures.length) addToast(`Some agents did not receive the toolkit: ${result.failures.join('; ')}`, 'alert');
      }
      addToast('Studio installed', 'info');
      await load();
      if (toolkitEmpty) tabRoute.value = 'toolkit';
    } catch (e: any) {
      addToast(e?.response?.data?.message || e?.message || 'Setup failed', 'alert');
    } finally {
      installing = false;
    }
  }

  load();
</script>

<div class="h-full overflow-y-auto bg-dark-base">
  {#if loading}
    <div class="flex items-center justify-center h-40 text-dark-text-muted"><Loader2 size={18} class="animate-spin" /></div>
  {:else if !avatarOrg && !seriesOrg && !longVideoOrg}
    <div class="max-w-xl mx-auto mt-16 border-y border-dark-border py-10 text-center">
      <div class="mx-auto mb-4 h-12 w-12 bg-dark-highest text-accent flex items-center justify-center"><Film size={23} /></div>
      <h2 class="text-lg font-semibold text-dark-text">Build a cast. Keep the continuity.</h2>
      <p class="mx-auto mt-2 max-w-md text-xs leading-5 text-dark-text-muted">
        Create recurring characters, write episodic storyboards, or develop standalone long videos with an AI brief assistant. Generate visuals and narration, then assemble final cuts.
      </p>
      <div class="mt-5 flex flex-wrap justify-center gap-x-5 gap-y-1 text-[11px] text-dark-text-muted">
        <span>Character bible</span><span>Style lock</span><span>Shot continuity</span><span>Structured renders</span>
      </div>
      <button onclick={setupStudio} disabled={installing} class="mt-6 inline-flex items-center gap-2 px-4 py-2 text-xs font-medium bg-accent text-dark-base hover:bg-accent-hover disabled:opacity-50">
        {#if installing}<Loader2 size={13} class="animate-spin" />{:else}<Clapperboard size={13} />{/if}
        {installing ? 'Installing studio…' : 'Set up Studio'}
      </button>
      <p class="mt-3 text-[10px] text-dark-text-muted">Works with any generation provider: after setup, pick the MCP sets, skills or built-in tools to use for image, video and speech.</p>
    </div>
  {:else}
    <div class="max-w-7xl mx-auto p-4 sm:p-6">
      <!-- Studio masthead: one compact operational overview, not a dashboard of cards. -->
      <header class="flex flex-col sm:flex-row sm:items-end gap-4 pb-4 border-b border-dark-border">
        <div class="min-w-0 flex-1">
          <div class="flex items-center gap-2">
            <div class="h-8 w-8 bg-dark-highest text-accent flex items-center justify-center"><Film size={16} /></div>
            <div><h1 class="text-base font-semibold text-dark-text">Studio</h1><p class="text-[10px] text-dark-text-muted">Characters, series, and standalone long videos</p></div>
          </div>
        </div>
        <div class="flex items-center gap-4 text-[10px] text-dark-text-muted tabular-nums">
          <span><strong class="text-sm font-semibold text-dark-text">{characters.length}</strong> characters</span>
          <span><strong class="text-sm font-semibold text-dark-text">{seriesCount}</strong> series</span>
          <button onclick={() => (tabRoute.value = 'toolkit')} class="hover:text-dark-text" title="Generation tools assigned per capability"><strong class="text-sm font-semibold text-dark-text">{configuredCount}/{STUDIO_CAPABILITIES.length}</strong> capabilities</button>
          <button onclick={() => reloadLibrary()} class="p-1 hover:text-dark-text" title="Refresh libraries"><RefreshCw size={13} /></button>
          <button onclick={setupStudio} disabled={installing} class="inline-flex items-center gap-1 px-2 py-1 border border-dark-border hover:text-dark-text disabled:opacity-50" title="Install missing studio skills and teams, and re-attach the toolkit">{#if installing}<Loader2 size={10} class="animate-spin" />{:else}<RefreshCw size={10} />{/if} Sync studio</button>
        </div>
      </header>

      {#if !avatarOrg || !seriesOrg || !longVideoOrg}
        <div class="mt-4 flex flex-col sm:flex-row sm:items-center gap-2 px-3 py-2 bg-amber-950 text-amber-300">
          <p class="text-[11px] flex-1">Some studio teams are missing ({[!avatarOrg && 'Avatar', !seriesOrg && 'Series', !longVideoOrg && 'Long Video'].filter(Boolean).join(', ')}).</p>
          <button onclick={setupStudio} disabled={installing} class="inline-flex items-center justify-center gap-1 px-2.5 py-1 text-[10px] font-medium bg-amber-300 text-amber-950 disabled:opacity-50">{#if installing}<Loader2 size={10} class="animate-spin" />{/if} Install missing</button>
        </div>
      {/if}
      {#if legacyAgents.length}
        <div class="mt-4 px-3 py-2 bg-amber-950 text-amber-300 text-[11px] leading-4">
          {legacyAgents.length} studio agent{legacyAgents.length === 1 ? ' is' : 's are'} from an older, FAL-specific version ({legacyAgents.map((agent) => agent.name).join(', ')}) and will not receive the toolkit.
          Delete the old studio organizations on the <a href="#/organizations" class="underline">Organizations</a> page, then use <strong>Sync studio</strong> to install the current teams.
        </div>
      {/if}
      {#if toolkitEmpty && tab !== 'toolkit'}
        <div class="mt-4 flex flex-col sm:flex-row sm:items-center gap-2 px-3 py-2 border border-dark-border">
          <p class="text-[11px] flex-1 text-dark-text-secondary">No generation tools are assigned yet. Agents can plan and assemble, but cannot generate images, video or speech until you pick tools.</p>
          <button onclick={() => (tabRoute.value = 'toolkit')} class="inline-flex items-center justify-center gap-1 px-2.5 py-1 text-[10px] font-medium bg-accent text-dark-base"><Wrench size={10} /> Open toolkit</button>
        </div>
      {/if}

      <nav class="mt-5 flex flex-wrap gap-x-5 gap-y-3 border-b border-dark-border" aria-label="Studio sections">
        <button onclick={() => (tabRoute.value = 'characters')} class={['pb-2.5 text-xs font-medium border-b-2 -mb-px flex items-center gap-1.5 ', tab === 'characters' ? 'border-accent text-dark-text' : 'border-transparent text-dark-text-muted hover:text-dark-text-secondary']}><User size={12} /> Characters</button>
        <button onclick={() => (tabRoute.value = 'series')} class={['pb-2.5 text-xs font-medium border-b-2 -mb-px flex items-center gap-1.5 ', tab === 'series' ? 'border-accent text-dark-text' : 'border-transparent text-dark-text-muted hover:text-dark-text-secondary']}><Film size={12} /> Series</button>
        <button onclick={() => (tabRoute.value = 'long-videos')} class={['pb-2.5 text-xs font-medium border-b-2 -mb-px flex items-center gap-1.5 ', tab === 'long-videos' ? 'border-accent text-dark-text' : 'border-transparent text-dark-text-muted hover:text-dark-text-secondary']}><Clapperboard size={12} /> Long Videos</button>
        <button onclick={() => (tabRoute.value = 'productions')} class={['pb-2.5 text-xs font-medium border-b-2 -mb-px flex items-center gap-1.5 ', tab === 'productions' ? 'border-accent text-dark-text' : 'border-transparent text-dark-text-muted hover:text-dark-text-secondary']}><Video size={12} /> Productions</button>
        <button onclick={() => (tabRoute.value = 'toolkit')} class={['pb-2.5 text-xs font-medium border-b-2 -mb-px flex items-center gap-1.5 ', tab === 'toolkit' ? 'border-accent text-dark-text' : 'border-transparent text-dark-text-muted hover:text-dark-text-secondary']}><Wrench size={12} /> Toolkit</button>
      </nav>

      <main class="mt-5">
        {#if tab === 'characters'}
          <StudioCharacters {assetsRoot} {characters} {voices} {seriesOrg} {avatarOrg} {toolkit} onReload={reloadLibrary} onTaskSubmitted={taskSubmitted} />
        {:else if tab === 'series'}
          <StudioSeries {assetsRoot} {characters} {seriesOrg} {toolkit} onTaskSubmitted={taskSubmitted} />
        {:else if tab === 'productions'}
          <StudioProductions {assetsRoot} {avatarOrg} {seriesOrg} {longVideoOrg} refreshKey={taskRefreshKey} />
        {:else if tab === 'toolkit'}
          <StudioToolkit {toolkit} agentCount={studioAgents.filter((agent) => isStudioProductionAgent(agent.config)).length} onSave={saveToolkit} />
        {/if}
        <!-- Keep the editor mounted across Studio tabs so unsaved briefs and chat survive. -->
        <div hidden={tab !== 'long-videos'}>
          <StudioLongForm {assetsRoot} {longVideoOrg} onTaskSubmitted={taskSubmitted} />
        </div>
      </main>
    </div>
  {/if}
</div>
