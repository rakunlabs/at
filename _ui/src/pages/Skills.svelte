<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listSkills,
    createSkill,
    updateSkill,
    deleteSkill,
    type Skill,
    type SkillTool,
  } from '@/lib/api/skills';
  import { Plus, Pencil, Trash2, X, Save, RefreshCw, Wand2, Bot, Copy, ClipboardPaste } from 'lucide-svelte';
  import SkillBuilderPanel from '@/lib/components/SkillBuilderPanel.svelte';

  storeNavbar.title = 'Skills';

  // ─── State ───

  let skills = $state<Skill[]>([]);
  let loading = $state(true);
  let showForm = $state(false);
  let editingId = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);
  let showAIPanel = $state(false);

  // Form fields
  let formName = $state('');
  let formDescription = $state('');
  let formSystemPrompt = $state('');
  let formTools = $state<SkillTool[]>([]);
  let saving = $state(false);

  // Copy / Paste via system clipboard (works across browsers/machines)

  async function copySkill(skill: Skill) {
    const exportData = {
      name: skill.name,
      description: skill.description,
      system_prompt: skill.system_prompt,
      tools: skill.tools || [],
    };
    try {
      await navigator.clipboard.writeText(JSON.stringify(exportData, null, 2));
      addToast(`Copied "${skill.name}" to clipboard`);
    } catch {
      addToast('Failed to copy to clipboard', 'alert');
    }
  }

  async function pasteSkill() {
    try {
      const text = await navigator.clipboard.readText();
      const src = JSON.parse(text);
      if (!src.name || typeof src.name !== 'string') {
        addToast('Clipboard does not contain a valid skill', 'warn');
        return;
      }
      resetForm();
      formName = src.name + '_copy';
      formDescription = src.description || '';
      formSystemPrompt = src.system_prompt || '';
      formTools = (src.tools || []).map((t: any) => ({
        name: t.name || '',
        description: t.description || '',
        inputSchema: t.inputSchema || {},
        handler: t.handler || '',
        handler_type: t.handler_type || 'js',
      }));
      editingId = null;
      showForm = true;
    } catch {
      addToast('Nothing to paste — copy a skill first or check clipboard permissions', 'warn');
    }
  }

  // ─── Load ───

  async function load() {
    loading = true;
    try {
      skills = await listSkills();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load skills', 'alert');
    } finally {
      loading = false;
    }
  }

  load();

  // ─── Form ───

  function resetForm() {
    formName = '';
    formDescription = '';
    formSystemPrompt = '';
    formTools = [];
    editingId = null;
    showForm = false;
  }

  function openCreate() {
    resetForm();
    showForm = true;
  }

  function openEdit(skill: Skill) {
    resetForm();
    editingId = skill.id;
    formName = skill.name;
    formDescription = skill.description;
    formSystemPrompt = skill.system_prompt;
    formTools = (skill.tools || []).map((t) => ({ ...t }));
    showForm = true;
  }

  function openEditWithAI(skill: Skill) {
    openEdit(skill);
    showAIPanel = true;
  }

  async function handleSubmit() {
    if (!formName.trim()) {
      addToast('Skill name is required', 'warn');
      return;
    }

    saving = true;
    try {
      const payload: Partial<Skill> = {
        name: formName.trim(),
        description: formDescription.trim(),
        system_prompt: formSystemPrompt,
        tools: formTools.filter((t) => t.name.trim()),
      };

      if (editingId) {
        await updateSkill(editingId, payload);
        addToast(`Skill "${formName}" updated`);
      } else {
        await createSkill(payload);
        addToast(`Skill "${formName}" created`);
      }
      resetForm();
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save skill', 'alert');
    } finally {
      saving = false;
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteSkill(id);
      addToast('Skill deleted');
      deleteConfirm = null;
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete skill', 'alert');
    }
  }

  // ─── Tools Management ───

  function addTool() {
    const tool: SkillTool = { name: '', description: '', inputSchema: {}, handler: '', handler_type: 'js' };
    formTools = [...formTools, tool];
  }

  function removeTool(index: number) {
    formTools = formTools.filter((_, i) => i !== index);
  }

  function updateToolSchema(index: number, value: string) {
    try {
      formTools[index].inputSchema = JSON.parse(value);
    } catch {
      // Keep old value on invalid JSON
    }
  }
</script>

<svelte:head>
  <title>AT | Skills</title>
</svelte:head>

<div class="flex h-full">
  <!-- Main content -->
  <div class="flex-1 overflow-y-auto">
    <div class="p-6 max-w-5xl mx-auto">
      <!-- Header -->
      <div class="flex items-center justify-between mb-4">
        <div class="flex items-center gap-2">
          <Wand2 size={16} class="text-gray-500" />
          <h2 class="text-sm font-medium text-gray-900">Skills</h2>
          <span class="text-xs text-gray-400">({skills.length})</span>
        </div>
        <div class="flex items-center gap-2">
          <button
            onclick={() => { showAIPanel = !showAIPanel; }}
            class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium transition-colors {showAIPanel ? 'bg-blue-50 text-blue-700 border border-blue-200' : 'border border-gray-300 text-gray-700 hover:bg-gray-50'}"
            title="Toggle AI Skill Builder"
          >
            <Bot size={12} />
            AI Builder
          </button>
          <button
            onclick={load}
            class="p-1.5 hover:bg-gray-100 text-gray-400 hover:text-gray-600 transition-colors"
            title="Refresh"
          >
            <RefreshCw size={14} />
          </button>
          <button
            onclick={openCreate}
            class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 transition-colors"
          >
            <Plus size={12} />
            New Skill
          </button>
        </div>
      </div>

      <!-- Form -->
      {#if showForm}
        <div class="border border-gray-200 mb-6 bg-white shadow-sm overflow-hidden">
          <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 bg-gray-50">
            <div class="flex items-center gap-2">
              <span class="text-sm font-medium text-gray-900">
                {editingId ? `Edit: ${formName}` : 'New Skill'}
              </span>
              {#if !editingId}
                <button
                  type="button"
                  onclick={pasteSkill}
                  class="flex items-center gap-1 px-2 py-1 text-xs font-medium border border-gray-300 text-gray-600 hover:bg-gray-100 hover:text-gray-900 transition-colors"
                  title="Paste skill from clipboard"
                >
                  <ClipboardPaste size={12} />
                  Paste
                </button>
              {/if}
            </div>
            <button onclick={resetForm} class="p-1 hover:bg-gray-200 text-gray-400 hover:text-gray-600 transition-colors">
              <X size={14} />
            </button>
          </div>

          <form onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="p-4 space-y-4">
            <!-- Name -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-name" class="text-sm font-medium text-gray-700">Name</label>
              <input
                id="form-name"
                type="text"
                bind:value={formName}
                placeholder="e.g., web_search, code_review"
                class="col-span-3 border border-gray-300 px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
              />
            </div>

            <!-- Description -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-description" class="text-sm font-medium text-gray-700">Description</label>
              <input
                id="form-description"
                type="text"
                bind:value={formDescription}
                placeholder="What this skill does"
                class="col-span-3 border border-gray-300 px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
              />
            </div>

            <!-- System Prompt -->
            <div class="grid grid-cols-4 gap-3 items-start">
              <label for="form-system-prompt" class="text-sm font-medium text-gray-700 pt-1.5">System Prompt</label>
              <textarea
                id="form-system-prompt"
                bind:value={formSystemPrompt}
                rows={3}
                placeholder="Instructions for the agent when using this skill"
                class="col-span-3 border border-gray-300 px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 resize-y transition-colors"
              ></textarea>
            </div>

            <!-- Tools -->
            <div class="grid grid-cols-4 gap-3 items-start">
              <span class="text-sm font-medium text-gray-700 pt-1.5">Tools</span>
              <div class="col-span-3 space-y-3">
                {#each formTools as tool, i}
                  <div class="border border-gray-200 p-3 bg-gray-50/50 space-y-2">
                    <div class="flex items-center justify-between">
                      <span class="text-xs font-medium text-gray-500">Tool {i + 1}</span>
                      <button
                        type="button"
                        onclick={() => removeTool(i)}
                        class="p-1 hover:bg-red-50 text-gray-400 hover:text-red-600 transition-colors"
                        title="Remove tool"
                      >
                        <X size={12} />
                      </button>
                    </div>
                    <div class="space-y-2">
                      <input
                        type="text"
                        bind:value={tool.name}
                        placeholder="Tool name (e.g., search_web)"
                        class="w-full border border-gray-300 px-2.5 py-1 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
                      />
                      <input
                        type="text"
                        bind:value={tool.description}
                        placeholder="Tool description"
                        class="w-full border border-gray-300 px-2.5 py-1 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
                      />
                      <div>
                        <div class="text-xs text-gray-500 mb-0.5">Input Schema (JSON)</div>
                        <textarea
                          value={JSON.stringify(tool.inputSchema || {}, null, 2)}
                          oninput={(e) => updateToolSchema(i, (e.target as HTMLTextAreaElement).value)}
                          rows={3}
                          class="w-full border border-gray-300 px-2.5 py-1 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 resize-y transition-colors"
                          placeholder={'{\n  "type": "object",\n  "properties": { ... }\n}'}
                        ></textarea>
                      </div>
                      <div>
                        <div class="flex items-center gap-2 mb-0.5">
                          <span class="text-xs text-gray-500">Handler</span>
                          <select
                            bind:value={tool.handler_type}
                            class="text-xs border border-gray-300 px-1.5 py-0.5 rounded bg-white focus:outline-none focus:ring-1 focus:ring-gray-400"
                          >
                            <option value="js">JavaScript</option>
                            <option value="bash">Bash</option>
                          </select>
                        </div>
                        <textarea
                          bind:value={tool.handler}
                          rows={3}
                          class="w-full border border-gray-300 px-2.5 py-1 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 resize-y transition-colors"
                          placeholder={tool.handler_type === 'bash'
                            ? '#!/bin/bash\ncurl -s "$ARG_URL" | jq .'
                            : '// Access tool arguments as "args"\nvar result = httpGet(args.url);\nreturn result.body;'}
                        ></textarea>
                      </div>
                    </div>
                  </div>
                {/each}
                <button
                  type="button"
                  onclick={addTool}
                  class="flex items-center gap-1.5 text-sm text-gray-500 hover:text-gray-900 transition-colors"
                >
                  <Plus size={12} />
                  Add tool
                </button>
              </div>
            </div>

            <!-- Actions -->
            <div class="flex justify-end gap-2 pt-3 border-t border-gray-100">
              <button
                type="button"
                onclick={resetForm}
                class="px-3 py-1.5 text-sm border border-gray-300 hover:bg-gray-50 text-gray-700 transition-colors"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={saving}
                class="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-gray-900 text-white hover:bg-gray-800 transition-colors disabled:opacity-50"
              >
                <Save size={14} />
                {#if saving}
                  Saving...
                {:else}
                  {editingId ? 'Update' : 'Create'}
                {/if}
              </button>
            </div>
          </form>
        </div>
      {/if}

      <!-- Skill list -->
      <div class="border border-gray-200 bg-white shadow-sm overflow-hidden">
        {#if loading}
          <div class="px-4 py-10 text-center text-gray-400 text-sm">Loading...</div>
        {:else if skills.length === 0 && !showForm}
          <div class="px-4 py-10 text-center">
            <Wand2 size={24} class="mx-auto text-gray-300 mb-2" />
            <div class="text-gray-400 mb-1">No skills configured</div>
            <div class="text-xs text-gray-400 mb-3">Skills define reusable tool sets for agent workflows</div>
            <button
              onclick={openCreate}
              class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 transition-colors mx-auto"
            >
              <Plus size={12} />
              New Skill
            </button>
          </div>
        {:else if skills.length > 0}
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-gray-200 bg-gray-50">
                <th class="text-left px-4 py-2.5 font-medium text-gray-500 text-xs uppercase tracking-wider">Name</th>
                <th class="text-left px-4 py-2.5 font-medium text-gray-500 text-xs uppercase tracking-wider">Description</th>
                <th class="text-left px-4 py-2.5 font-medium text-gray-500 text-xs uppercase tracking-wider">Tools</th>
                <th class="text-right px-4 py-2.5 font-medium text-gray-500 text-xs uppercase tracking-wider w-32"></th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100">
              {#each skills as skill}
                <tr class="hover:bg-gray-50/50 transition-colors">
                  <td class="px-4 py-2.5 font-mono font-medium text-gray-900">{skill.name}</td>
                  <td class="px-4 py-2.5 text-xs text-gray-500 max-w-64 truncate" title={skill.description}>
                    {skill.description || '-'}
                  </td>
                  <td class="px-4 py-2.5 text-xs text-gray-500">
                    {#if skill.tools && skill.tools.length > 0}
                      <span class="px-2 py-0.5 bg-gray-100 text-gray-600 font-mono">
                        {skill.tools.length} tool{skill.tools.length !== 1 ? 's' : ''}
                      </span>
                      <span class="ml-1.5 text-gray-400">
                        {skill.tools.map((t) => t.name).join(', ')}
                      </span>
                    {:else}
                      <span class="text-gray-400">none</span>
                    {/if}
                  </td>
                  <td class="px-4 py-2.5 text-right">
                    <div class="flex justify-end gap-1">
                      <button
                        onclick={() => copySkill(skill)}
                        class="p-1.5 hover:bg-gray-100 text-gray-400 hover:text-gray-700 transition-colors"
                        title="Copy skill"
                      >
                        <Copy size={14} />
                      </button>
                      <button
                        onclick={() => openEditWithAI(skill)}
                        class="p-1.5 hover:bg-blue-50 text-gray-400 hover:text-blue-600 transition-colors"
                        title="Edit with AI"
                      >
                        <Bot size={14} />
                      </button>
                      <button
                        onclick={() => openEdit(skill)}
                        class="p-1.5 hover:bg-gray-100 text-gray-400 hover:text-gray-700 transition-colors"
                        title="Edit"
                      >
                        <Pencil size={14} />
                      </button>
                      {#if deleteConfirm === skill.id}
                        <button
                          onclick={() => handleDelete(skill.id)}
                          class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 transition-colors"
                        >
                          Confirm
                        </button>
                        <button
                          onclick={() => (deleteConfirm = null)}
                          class="px-2 py-1 text-xs border border-gray-300 hover:bg-gray-50 transition-colors"
                        >
                          Cancel
                        </button>
                      {:else}
                        <button
                          onclick={() => (deleteConfirm = skill.id)}
                          class="p-1.5 hover:bg-red-50 text-gray-400 hover:text-red-600 transition-colors"
                          title="Delete"
                        >
                          <Trash2 size={14} />
                        </button>
                      {/if}
                    </div>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
      </div>
    </div>
  </div>

  <!-- AI Panel (slides in from right) -->
  {#if showAIPanel}
    <SkillBuilderPanel
      onclose={() => { showAIPanel = false; }}
      bind:formName
      bind:formDescription
      bind:formSystemPrompt
      bind:formTools
      bind:editingId
      bind:showForm
      onSaved={load}
    />
  {/if}
</div>
