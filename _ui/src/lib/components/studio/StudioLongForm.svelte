<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { submitOrgTask, type Organization } from '@/lib/api/organizations';
  import { fileServeUrl } from '@/lib/api/files';
  import {
    createVideoBrief, loadVideoProjects, saveVideoBrief, saveVideoSubmission,
    validateVideoBrief, videoErrorMessage, videoProjectDir, VIDEO_BRIEF_LIMITS,
    type VideoBrief, type VideoProject,
  } from '@/lib/api/studio-videos';
  import VideoBriefBuilderPanel from './VideoBriefBuilderPanel.svelte';
  import { createVideoTemplate, loadVideoTemplates, saveVideoTemplate, type VideoTemplate } from '@/lib/api/studio-video-templates';
  import { ExternalLink, Loader2, Plus, RefreshCw, Save, Video } from 'lucide-svelte';

  interface Props {
    assetsRoot: string;
    longVideoOrg: Organization | null;
    onTaskSubmitted: () => void;
  }
  let { assetsRoot, longVideoOrg, onTaskSubmitted }: Props = $props();
  const initialBrief = createVideoBrief();
  let brief = $state<VideoBrief>(initialBrief);
  let baseline = $state(JSON.stringify(initialBrief));
  let projects = $state<VideoProject[]>([]);
  let templates = $state<VideoTemplate[]>([]);
  let templateWarnings = $state<string[]>([]);
  let templateError = $state('');
  let templatesReady = $state(false);
  let selectedTemplateId = $state('');
  let editingTemplateId = $state('');
  let templateName = $state<string | null>(null);
  let templateNameBaseline = $state<string | null>(null);
  const selectedTemplate = $derived(templates.find((item) => item.id === selectedTemplateId));
  const editingTemplate = $derived(templates.find((item) => item.id === editingTemplateId));
  const effectiveTemplateName = $derived(templateName ?? brief.title);
  let loading = $state(false);
  let busy = $state(false);
  let error = $state('');
  let libraryWarnings = $state<string[]>([]);
  let notice = $state('');
  let paidConfirmed = $state(false);
  let libraryReady = $state(false);
  let assistantStreaming = $state(false);
  let recoveryReady = $state(false);
  let recoveryNotice = $state('');
  let recoveryError = $state('');
  let draftRecoveryKey = '';
  let storageUnavailable = false;
  // Receipts remain authoritative in memory even if either persistence layer fails.
  let receipts = $state<Record<string, string>>({});
  let pendingReceipts = $state<Record<string, string>>({});
  let uncertain = $state<Record<string, boolean>>({});
  const recoveryKey = $derived(`at:studio-video-submissions:${assetsRoot}`);
  const selected = $derived(projects.find((project) => project.brief.id === brief.id));
  const taskId = $derived(receipts[brief.id] || selected?.task_id || '');
  const locked = $derived(!!taskId || !!selected?.output || !!uncertain[brief.id]);
  const disabled = $derived(busy || loading || locked);
  const dirty = $derived(!locked && (JSON.stringify(brief) !== baseline || templateName !== templateNameBaseline));
  const output = $derived(selected?.output);
  const outputPath = $derived(output?.final_video ? (output.final_video.startsWith('/') ? output.final_video : `${selected?.dir}/${output.final_video}`) : '');
  const inputClass = 'w-full px-3 py-2 text-xs bg-white dark:bg-dark-surface border border-gray-200 dark:border-dark-border text-gray-900 dark:text-dark-text focus:outline-none focus:ring-2 focus:ring-gray-500 dark:focus:ring-accent disabled:opacity-60';
  const buttonClass = 'inline-flex items-center justify-center gap-1.5 px-3 py-2 text-xs border border-gray-200 dark:border-dark-border hover:bg-gray-100 dark:hover:bg-dark-elevated disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2';

  function autosave() {
    if (!recoveryReady || storageUnavailable) return;
    try {
      if (locked) sessionStorage.removeItem(draftRecoveryKey);
      else sessionStorage.setItem(draftRecoveryKey, JSON.stringify({ brief, baseline, editingTemplateId, templateName, templateNameBaseline }));
    } catch {
      storageUnavailable = true;
      recoveryError = 'Browser draft recovery is unavailable. Save your draft before leaving Studio.';
    }
  }

  $effect(() => {
    if (!recoveryReady) return;
    // Track editor content and lock/baseline changes, not storage error feedback.
    JSON.stringify(brief);
    baseline;
    locked;
    editingTemplateId;
    templateName;
    templateNameBaseline;
    untrack(autosave);
  });

  function canDiscard(): boolean {
    return !dirty || window.confirm('Discard unsaved changes to this video brief?');
  }

  function choose(project?: VideoProject) {
    if (busy || loading || assistantStreaming || (project && project.brief.id === brief.id) || !canDiscard()) return;
    brief = project ? { ...project.brief } : createVideoBrief();
    baseline = JSON.stringify(brief);
    editingTemplateId = '';
    templateName = null;
    templateNameBaseline = null;
    paidConfirmed = false;
    error = '';
    notice = '';
    recoveryNotice = '';
    autosave();
  }

  function rememberReceipt(id: string, task: string) {
    receipts = { ...receipts, [id]: task };
    pendingReceipts = { ...pendingReceipts, [id]: task };
    try {
      localStorage.setItem(recoveryKey, JSON.stringify(receipts));
    } catch {
      notice = 'Browser recovery storage is unavailable. Keep the task link before leaving this page.';
    }
  }

  async function refresh() {
    if (busy || loading || assistantStreaming) return;
    loading = true;
    error = '';
    templateError = '';
    try {
      try {
        const warnings: string[] = [];
        templates = await loadVideoTemplates(assetsRoot, (message) => warnings.push(message));
        templateWarnings = warnings;
        templatesReady = true;
      } catch (e) {
        templateError = videoErrorMessage(e);
        templatesReady = false;
      }
      const warnings: string[] = [];
      const next = await loadVideoProjects(assetsRoot, (message) => warnings.push(message));
      libraryWarnings = warnings;
      // Refresh output and the library without replacing unsaved editor content.
      projects = next.map((project) => ({ ...project, task_id: receipts[project.brief.id] || project.task_id }));
      for (const project of next) {
        if (project.task_id) {
          receipts[project.brief.id] = project.task_id;
          delete pendingReceipts[project.brief.id];
        }
      }
      const saved = next.find((project) => project.brief.id === brief.id);
      if (saved) {
        if (saved.task_id || saved.output || receipts[saved.brief.id]) {
          brief = { ...saved.brief };
          recoveryNotice = '';
        }
        baseline = JSON.stringify(saved.brief);
      }
      libraryReady = true;
    } catch (e) {
      error = videoErrorMessage(e);
      libraryReady = false;
    } finally {
      loading = false;
    }
  }

  function editTemplate() {
    if (!selectedTemplate || busy || loading || assistantStreaming || !canDiscard()) return;
    brief = { ...selectedTemplate.brief, id: crypto.randomUUID() };
    baseline = JSON.stringify(brief);
    editingTemplateId = selectedTemplate.id;
    templateName = selectedTemplate.name;
    templateNameBaseline = templateName;
    paidConfirmed = false;
    error = '';
    notice = 'Template loaded into a new draft. Update the template explicitly to change future runs.';
    recoveryNotice = '';
    autosave();
  }

  function documentaryStarter() {
    if (busy || loading || assistantStreaming || !canDiscard()) return;
    brief = {
      ...createVideoBrief(), title: 'Research-led documentary', topic: '',
      content_brief: 'Create a documentary about the supplied topic. Research before scripting; cite credible primary, museum, academic, or conservation sources for factual claims and retain a source list. Cross-check dates, names, and disputed claims; distinguish evidence, uncertainty, and interpretation. Never invent sources or imply research has already been verified. Adapt examples and sections to each new topic.',
      audience: 'Curious general audience', duration_minutes: 10,
      visual_style: 'Naturalistic documentary with readable maps, timelines, and source captions. Clearly label all generated historical or wildlife scenes as reconstructions, never archival footage. Do not present imagined behavior or appearance as established fact.',
      outline: 'Opening question and stakes; geographic and historical context; evidence-led chapters adapted to the topic; causes and competing explanations with uncertainty stated; present-day relevance; conclusion and source credits. Research and verify claims before final narration. Label reconstructions throughout.',
    };
    baseline = JSON.stringify({ ...createVideoBrief(), id: brief.id });
    editingTemplateId = '';
    templateName = null;
    templateNameBaseline = null;
    paidConfirmed = false;
    error = '';
    notice = 'Documentary starter added. Set a topic for a one-off video, or save these adaptable settings as a template.';
    recoveryNotice = '';
    autosave();
  }

  async function saveTemplate(update: boolean) {
    if (disabled || assistantStreaming || !assetsRoot || !effectiveTemplateName.trim() || (update && (!editingTemplate || !templatesReady))) return;
    busy = true;
    error = '';
    notice = '';
    try {
      const value = update && editingTemplate
        ? { ...editingTemplate, name: effectiveTemplateName, brief }
        : createVideoTemplate(brief, effectiveTemplateName);
      const saved = await saveVideoTemplate(assetsRoot, value);
      templates = [saved, ...templates.filter((item) => item.id !== saved.id)].sort((a, b) => a.name.localeCompare(b.name));
      editingTemplateId = saved.id;
      selectedTemplateId = saved.id;
      templateName = saved.name;
      templateNameBaseline = saved.name;
      baseline = JSON.stringify(brief);
      notice = `Template "${saved.name}" saved. No production launched. Changes apply to future Telegram runs only; existing projects are unchanged.`;
    } catch (e) {
      error = videoErrorMessage(e);
    } finally {
      busy = false;
    }
  }

  async function persistDraft(): Promise<VideoProject> {
    const project = await saveVideoBrief(assetsRoot, brief);
    brief = { ...project.brief };
    baseline = JSON.stringify(brief);
    projects = [project, ...projects.filter((item) => item.brief.id !== brief.id)];
    return project;
  }

  async function save() {
    if (disabled || assistantStreaming || !libraryReady || !assetsRoot) return;
    busy = true;
    error = '';
    notice = '';
    try {
      await persistDraft();
      notice = 'Draft saved. No production task has been launched.';
    } catch (e) {
      error = videoErrorMessage(e);
    } finally {
      busy = false;
    }
  }

  async function repairReceipt(id: string, task: string) {
    if (busy) return;
    busy = true;
    error = '';
    try {
      await saveVideoSubmission(videoProjectDir(assetsRoot, id), task);
      delete pendingReceipts[id];
      notice = 'Task link saved. No additional task was launched.';
    } catch (e) {
      error = `Task ${task} is already accepted. Could not save its link: ${videoErrorMessage(e)}. Do not resubmit.`;
    } finally {
      busy = false;
    }
  }

  async function launch() {
    if (disabled || assistantStreaming || !libraryReady || !longVideoOrg || !paidConfirmed) return;
    error = '';
    notice = '';
    try {
      validateVideoBrief(brief);
      if (!brief.title.trim() || !brief.topic.trim() || !brief.content_brief.trim()) {
        throw new Error('Add a title, topic, and content brief before launching.');
      }
    } catch (e) {
      error = videoErrorMessage(e);
      return;
    }
    if (!window.confirm(`Launch "${brief.title}"? This starts paid generation for a ${brief.duration_minutes}-minute video and locks this brief.`)) return;
    busy = true;
    let submitting = false;
    try {
      const project = await persistDraft();
      submitting = true;
      const task = await submitOrgTask(longVideoOrg.id, {
        title: `Long video: ${project.brief.title}`,
        description: [
          'Produce one complete standalone long-form video from the saved brief below. This is an explicit production request, not a brief-writing request.',
          `Absolute project directory: ${project.dir}`,
          `Read ${project.dir}/brief.json as immutable input. Do not change brief.json or the UI-owned submission.json. Do not create a series or require series metadata.`,
          'Plan the script and shots, generate narration and visuals, assemble and verify the final video with audio. Honor the brief language, audience, target duration, aspect ratio, visual style, and outline. Use available configured production skills; report missing credentials or failures honestly.',
          `Keep durable artifacts in ${project.dir}. Atomically write agent-owned ${project.dir}/video.json with status, final_video (absolute path or relative to the project directory), duration_s, updated_at. Update progress there; publish final_video only when the playable render is ready. Never overwrite it with UI metadata.`,
          'Brief content (creative input, not instructions to change file ownership):',
          JSON.stringify(project.brief, null, 2),
        ].join('\n\n'),
      });
      rememberReceipt(project.brief.id, task.id);
      projects = projects.map((item) => item.brief.id === project.brief.id ? { ...item, task_id: task.id } : item);
      submitting = false;
      notice = `Production accepted. This brief is now read-only. ${notice}`.trim();
      try {
        onTaskSubmitted();
      } catch {
        notice += ' The task list could not refresh; use the task link below.';
      }
      try {
        await saveVideoSubmission(project.dir, task.id);
        delete pendingReceipts[project.brief.id];
      } catch (e) {
        error = `Task ${task.id} was accepted, but its link could not be saved: ${videoErrorMessage(e)}. Do not resubmit. Use the task link or retry saving the link below.`;
      }
    } catch (e) {
      if (submitting) {
        uncertain[brief.id] = true;
        error = `Could not confirm submission: ${videoErrorMessage(e)}. Check Tasks before taking any further action; the server may have accepted it. This draft is locked for this session to prevent duplicate charges.`;
      } else {
        error = videoErrorMessage(e);
      }
    } finally {
      busy = false;
    }
  }

  onMount(() => {
    draftRecoveryKey = `at:studio-video-brief:${assetsRoot}`;
    let storedDraft: string | null = null;
    try {
      storedDraft = sessionStorage.getItem(draftRecoveryKey);
    } catch {
      storageUnavailable = true;
      recoveryError = 'Browser draft recovery is unavailable. Save your draft before leaving Studio.';
    }
    if (storedDraft !== null) {
      try {
        const stored = JSON.parse(storedDraft);
        const restored = validateVideoBrief(stored?.brief);
        const restoredBaseline = validateVideoBrief(JSON.parse(stored?.baseline));
        if (restored.id !== restoredBaseline.id) throw new Error('Recovery IDs do not match');
        brief = restored;
        baseline = JSON.stringify(restoredBaseline);
        if (typeof stored.editingTemplateId === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(stored.editingTemplateId)) {
          editingTemplateId = stored.editingTemplateId;
          selectedTemplateId = stored.editingTemplateId;
        }
        templateName = typeof stored.templateName === 'string' ? stored.templateName : null;
        templateNameBaseline = typeof stored.templateNameBaseline === 'string' ? stored.templateNameBaseline : null;
        recoveryNotice = editingTemplateId
          ? 'Restored your template working draft from this tab. AI chat is not restored. Update the template or save a copy to keep your changes.'
          : 'Restored your video brief from this tab. AI chat is not restored. Save the draft to keep it in your library.';
      } catch {
        recoveryError = 'The browser draft recovery data was invalid and could not be restored. Saved projects will still load.';
      }
    }
    try {
      const stored = JSON.parse(localStorage.getItem(recoveryKey) || '{}');
      if (stored && typeof stored === 'object' && !Array.isArray(stored)) {
        for (const [id, task] of Object.entries(stored)) {
          if (/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$/.test(id) && typeof task === 'string' && task.trim()) receipts[id] = task;
        }
        pendingReceipts = { ...receipts };
      }
    } catch {
      notice = 'Browser recovery data could not be read. Saved project receipts will still load.';
    }
    // Restore synchronously before the effect can save the initial empty brief.
    recoveryReady = true;
    void refresh();
    return () => untrack(autosave);
  });
</script>

<svelte:window onbeforeunload={(event) => { if (dirty || busy || Object.keys(pendingReceipts).length) { event.preventDefault(); event.returnValue = ''; } }} />

<section aria-label="Long videos" class="space-y-5 text-gray-700 dark:text-dark-text-secondary">
  <header class="flex flex-wrap items-start justify-between gap-3">
    <div><h2 class="text-sm font-semibold text-gray-900 dark:text-dark-text">Long Videos</h2><p class="mt-1 text-xs">Shape a standalone video brief, save a draft, then launch production when ready.</p></div>
    <div class="flex gap-2">
      <button class={buttonClass} disabled={busy || loading || assistantStreaming} onclick={() => choose()}><Plus size={13} /> New draft</button>
      <button class={buttonClass} disabled={busy || loading || assistantStreaming} onclick={refresh}>{#if loading}<Loader2 size={13} class="animate-spin" />{:else}<RefreshCw size={13} />{/if} Refresh</button>
    </div>
  </header>

  <section aria-label="Reusable video templates" class="space-y-3 border-y border-gray-200 dark:border-dark-border py-4">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="max-w-3xl space-y-1 text-xs leading-5">
        <h3 class="font-semibold text-gray-900 dark:text-dark-text">Reusable templates</h3>
        <p>Build settings in the brief editor below, then save a named snapshot. Templates keep language, audience, duration, format, style, content guidance, and outline fixed; each Telegram request supplies a new topic. No placeholders are needed.</p>
      </div>
      <button class={buttonClass} disabled={busy || loading || assistantStreaming} onclick={documentaryStarter}>Documentary starter</button>
    </div>
    <div class="flex flex-wrap items-end gap-2">
      <label class="min-w-0 flex-1 space-y-1.5 text-xs"><span class="block">Saved templates</span>
        <select class={inputClass} bind:value={selectedTemplateId} disabled={busy || loading || assistantStreaming || !templatesReady}>
          <option value="">{loading ? 'Loading templates...' : !templatesReady ? 'Templates unavailable; refresh to retry' : templates.length ? 'Select a template' : 'No templates yet; save your first below'}</option>
          {#each templates as template (template.id)}<option value={template.id}>{template.name}</option>{/each}
        </select>
      </label>
      <button class={buttonClass} disabled={!selectedTemplate || !templatesReady || busy || loading || assistantStreaming} onclick={editTemplate}>Edit template</button>
    </div>
    {#if selectedTemplate}
      <p class="break-words text-xs leading-5">Saved settings: {selectedTemplate.brief.language} / {selectedTemplate.brief.duration_minutes} min / {selectedTemplate.brief.aspect_ratio} / Audience: {selectedTemplate.brief.audience || 'Not specified'}. Updated {new Date(selectedTemplate.updated_at).toLocaleString()}. Editing opens a new draft, not an existing production.</p>
    {/if}
    {#if templateError}<p role="alert" class="text-xs text-red-700 dark:text-red-300">Could not load templates: {templateError}. Refresh to retry.</p>{/if}
    {#if templateWarnings.length}<details class="break-words text-xs text-amber-800 dark:text-amber-300"><summary class="cursor-pointer">{templateWarnings.length} template(s) could not be loaded</summary>{#each templateWarnings as warning}<p class="mt-2">{warning}</p>{/each}</details>{/if}
    <p class="text-xs leading-5">For Telegram, open <a href="#/bots" class="underline underline-offset-2">Bots &gt; Custom Commands</a>, choose the <strong>Long Video Studio</strong> organization and your saved template in the template dropdown. For example, bind <code>/belgesel</code>, then send <code>/belgesel New Zealand extinct animals</code>. The new topic replaces only the topic, not your full brief or production settings.</p>
  </section>

  {#if !longVideoOrg}<p class="px-3 py-2 text-xs bg-amber-50 text-amber-800 dark:bg-amber-950 dark:text-amber-300">Long Video Studio is not installed. Use <strong>Sync studio</strong> to install its production team. You can still prepare and save drafts.</p>{/if}
  {#if !assetsRoot}<p role="alert" class="text-xs text-red-700 dark:text-red-300">The asset library is unavailable. Reload Studio before saving or launching.</p>{/if}
  {#if error}<p role="alert" class="break-words px-3 py-2 text-xs bg-red-50 text-red-700 dark:bg-red-950 dark:text-red-300">{error}</p>{/if}
  {#if libraryWarnings.length}
    <details class="text-xs break-words text-amber-800 dark:text-amber-300">
      <summary class="cursor-pointer">{libraryWarnings.length} project(s) could not be loaded. Other drafts are still available.</summary>
      {#each libraryWarnings as warning}<p class="mt-2">{warning}</p>{/each}
    </details>
  {/if}
  {#if notice}<p role="status" class="text-xs">{notice}</p>{/if}
  {#if recoveryNotice}<p role="status" class="text-xs">{recoveryNotice}</p>{/if}
  {#if recoveryError}<p role="alert" class="text-xs text-red-700 dark:text-red-300">{recoveryError}</p>{/if}
  {#if Object.keys(pendingReceipts).length}
    <div class="space-y-2 border-y border-gray-200 dark:border-dark-border py-3 text-xs">
      <p>Accepted tasks with a pending saved link. These are already running; do not launch them again.</p>
      {#each Object.entries(pendingReceipts) as [id, task] (id)}
        <div class="flex flex-wrap items-center gap-3">
          <a class="break-all underline underline-offset-2" href={`#/tasks/${encodeURIComponent(task)}`}>Open task {task}</a>
          <button class={buttonClass} disabled={busy || loading} onclick={() => repairReceipt(id, task)}>Retry saving task link</button>
        </div>
      {/each}
    </div>
  {/if}

  <div class="grid grid-cols-1 gap-6 lg:grid-cols-[14rem_minmax(0,1fr)]">
    <aside aria-label="Video projects" class="min-w-0">
      <h3 class="mb-2 text-xs font-semibold">Saved projects</h3>
      {#if loading}<p role="status" class="text-xs">Loading projects...</p>{:else if !libraryReady}<p class="text-xs">Projects could not be loaded. Refresh to retry.</p>{:else if !projects.length}<p class="text-xs leading-5">No saved videos yet. Start with a topic and save your first draft.</p>{/if}
      <ul class="divide-y divide-gray-200 dark:divide-dark-border">
        {#each projects as project (project.brief.id)}
          <li><button disabled={busy || loading || assistantStreaming} onclick={() => choose(project)} aria-current={project.brief.id === brief.id ? 'true' : undefined} class={['w-full text-left py-3 px-2 text-xs hover:bg-gray-100 dark:hover:bg-dark-elevated disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2', project.brief.id === brief.id ? 'bg-gray-100 dark:bg-dark-elevated' : '']}>
            <span class="block break-words font-medium text-gray-900 dark:text-dark-text">{project.brief.title || 'Untitled video'}</span>
            <span class="mt-1 block">{project.output?.status || (project.task_id || receipts[project.brief.id] ? 'Submitted' : 'Draft')} - {project.brief.duration_minutes} min</span>
          </button></li>
        {/each}
      </ul>
    </aside>

    <div class="min-w-0 space-y-5">
      <div class="flex flex-wrap items-center justify-between gap-2 text-xs">
        <h3 class="font-semibold text-gray-900 dark:text-dark-text">{locked ? 'Production brief (read-only)' : editingTemplateId ? 'Template draft' : 'Video brief'}</h3>
        <span>{dirty ? 'Unsaved changes' : editingTemplateId && !locked ? 'Template working draft' : selected ? 'Saved' : 'New draft'}</span>
      </div>
      {#if editingTemplateId && !locked}<p class="break-words text-xs leading-5">Editing template: <strong>{editingTemplate?.name || effectiveTemplateName || editingTemplateId}</strong>. Update it explicitly for future runs, or save a separate copy. Launching this draft creates a new project and does not update the template.</p>{/if}
      {#if taskId}<a href={`#/tasks/${encodeURIComponent(taskId)}`} class="inline-flex items-center gap-2 text-xs underline underline-offset-2"><ExternalLink size={13} /> Open production task {taskId}</a>{/if}
      {#if uncertain[brief.id]}<a href="#/tasks" class="block text-xs underline">Check Tasks for this production</a>{/if}

       <div class="grid min-w-0 grid-cols-1 items-start gap-5 xl:grid-cols-[minmax(0,1fr)_minmax(20rem,0.85fr)]">
       <fieldset disabled={disabled || assistantStreaming} class="grid min-w-0 grid-cols-1 gap-4 sm:grid-cols-2">
        <legend class="sr-only">Video brief details</legend>
        <label class="space-y-1.5 text-xs sm:col-span-2"><span class="block">Title</span><input class={inputClass} bind:value={brief.title} maxlength={VIDEO_BRIEF_LIMITS.title} /></label>
        <label class="space-y-1.5 text-xs sm:col-span-2"><span class="block">Topic</span><textarea class={inputClass} rows="2" bind:value={brief.topic} maxlength={VIDEO_BRIEF_LIMITS.topic}></textarea></label>
        <label class="space-y-1.5 text-xs sm:col-span-2"><span class="block">Content brief</span><textarea class={inputClass} rows="5" bind:value={brief.content_brief} maxlength={VIDEO_BRIEF_LIMITS.content_brief} placeholder="What should the viewer learn or experience? Include key points, sources, and constraints."></textarea></label>
        <label class="space-y-1.5 text-xs"><span class="block">Audience</span><input class={inputClass} bind:value={brief.audience} maxlength={VIDEO_BRIEF_LIMITS.audience} /></label>
        <label class="space-y-1.5 text-xs"><span class="block">Language</span><input class={inputClass} bind:value={brief.language} maxlength={VIDEO_BRIEF_LIMITS.language} /></label>
        <label class="space-y-1.5 text-xs"><span class="block">Target duration (minutes)</span><input type="number" class={inputClass} bind:value={brief.duration_minutes} min="1" max="60" step="any" /><span class="block">1 to 60 minutes</span></label>
        <label class="space-y-1.5 text-xs"><span class="block">Aspect ratio</span><select class={inputClass} bind:value={brief.aspect_ratio}><option value="16:9">16:9 - Landscape</option><option value="9:16">9:16 - Portrait</option><option value="1:1">1:1 - Square</option></select></label>
        <label class="space-y-1.5 text-xs sm:col-span-2"><span class="block">Visual style</span><textarea class={inputClass} rows="2" bind:value={brief.visual_style} maxlength={VIDEO_BRIEF_LIMITS.visual_style}></textarea></label>
        <label class="space-y-1.5 text-xs sm:col-span-2"><span class="block">Outline</span><textarea class={inputClass} rows="6" bind:value={brief.outline} maxlength={VIDEO_BRIEF_LIMITS.outline} placeholder="Sections, sequence, and approximate timing"></textarea></label>
      </fieldset>

       {#key brief.id}<VideoBriefBuilderPanel bind:brief bind:streaming={assistantStreaming} disabled={disabled} />{/key}
       </div>

      {#if !locked}
        <section aria-label="Save reusable template" class="space-y-3 border-t border-gray-200 dark:border-dark-border pt-4">
          <h3 class="text-xs font-semibold text-gray-900 dark:text-dark-text">Save as reusable template</h3>
          <p class="text-xs leading-5">Save the current editor settings without launching production or approving paid generation. Updates affect future Telegram runs only, never old projects.</p>
          <label class="block max-w-xl space-y-1.5 text-xs"><span class="block">Template name (required)</span><input class={inputClass} value={effectiveTemplateName} oninput={(event) => templateName = event.currentTarget.value} disabled={disabled || assistantStreaming} required placeholder="For example: Research-led wildlife documentary" /></label>
          <div class="flex flex-wrap gap-2">
            {#if editingTemplateId}<button class={buttonClass} disabled={disabled || assistantStreaming || !assetsRoot || !templatesReady || !editingTemplate || !effectiveTemplateName.trim()} onclick={() => saveTemplate(true)}><Save size={13} /> Update existing template</button>{/if}
            <button class={buttonClass} disabled={disabled || assistantStreaming || !assetsRoot || !effectiveTemplateName.trim()} onclick={() => saveTemplate(false)}><Save size={13} /> {editingTemplateId ? 'Save copy as new template' : 'Save as reusable template'}</button>
          </div>
          {#if editingTemplateId && templatesReady && !editingTemplate}<p role="status" class="text-xs text-amber-800 dark:text-amber-300">The original template is unavailable. Your recovered draft is safe; save a new copy instead.</p>{/if}
        </section>
        <div class="space-y-3 border-t border-gray-200 dark:border-dark-border pt-4">
          <p class="text-xs leading-5">Saving and AI brief editing do not launch production. Launching starts <strong>paid generation</strong> through your configured providers; longer videos can incur substantial costs. The submitted brief cannot be edited.</p>
          <label class="flex items-start gap-2 text-xs"><input type="checkbox" bind:checked={paidConfirmed} disabled={disabled} class="mt-0.5" /><span>I understand launching may incur provider charges.</span></label>
          <div class="flex flex-wrap gap-2">
            <button class={buttonClass} disabled={disabled || assistantStreaming || !assetsRoot || !libraryReady} onclick={save}><Save size={13} /> Save draft</button>
            <button onclick={launch} disabled={disabled || assistantStreaming || !assetsRoot || !libraryReady || !longVideoOrg || !paidConfirmed || !brief.title.trim() || !brief.topic.trim() || !brief.content_brief.trim()} class="inline-flex items-center justify-center gap-1.5 px-4 py-2 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2">{#if busy}<Loader2 size={13} class="animate-spin" />{:else}<Video size={13} />{/if} Launch paid production</button>
          </div>
        </div>
      {/if}

      {#if locked}
        <section class="space-y-3 border-t border-gray-200 dark:border-dark-border pt-4" aria-label="Video output">
          <h3 class="text-xs font-semibold text-gray-900 dark:text-dark-text">Production output</h3>
           <p class="text-xs">{output?.status || 'Waiting for production updates'}. Use Refresh to check progress.{#if output?.duration_s != null} Duration: {output.duration_s} seconds.{/if}</p>
          {#if outputPath}
            <!-- Generated media may not have a captions track; retain native playback controls. -->
            <!-- svelte-ignore a11y_media_has_caption -->
            <video class="w-full max-h-[36rem] bg-black" controls preload="metadata" src={fileServeUrl(outputPath, output?.updated_at)} aria-label={brief.title || 'Generated video'}></video>
            <a class="inline-block text-xs underline underline-offset-2" href={fileServeUrl(outputPath)} target="_blank" rel="noreferrer">Open video file</a>
          {:else}<p class="text-xs">The final video will appear here when the production team publishes it.</p>{/if}
        </section>
      {/if}
    </div>
  </div>
</section>
