<script lang="ts">
  import { untrack } from 'svelte';

  let { data, providers = [] }: { data: Record<string, any>; providers?: any[]; workflow?: any } = $props();

  let decisionProviders = $derived(providers.filter(p => p.config?.type === 'systemone'));
  let selectedProvider = $derived(providers.find(p => p.key === data.provider));
  let availableModels = $derived<string[]>(
    selectedProvider?.config?.models?.length
      ? selectedProvider.config.models
      : ['auto', 'english', 'multilingual', 'typed-decisions']
  );

  const example = {
    department: {
      type: 'choice',
      instructions: 'Which team should handle this?',
      criteria: { billing: 'invoices, payments, refunds', technical: 'bugs, outages', other: 'everything else' },
    },
    urgency: { type: 'score', instructions: 'How urgent is this?', criteria: ['not urgent', 'soon', 'blocking'] },
  };

  // The question set is edited as JSON text and stored as an object once it
  // parses, so the saved graph stays structured. The text is re-seeded when a
  // different node's data object is shown in the same panel.
  function toText(questions: unknown): string {
    if (typeof questions === 'string') return questions;
    const q = questions && typeof questions === 'object' && Object.keys(questions).length ? questions : example;
    return JSON.stringify(q, null, 2);
  }
  let questionsText = $state('');
  let questionsError = $state('');
  let seededFor: Record<string, any> | null = null;
  $effect.pre(() => {
    if (seededFor === data) return;
    seededFor = data;
    questionsText = untrack(() => toText(data.questions));
    questionsError = '';
  });

  function onQuestionsInput() {
    try {
      const parsed = JSON.parse(questionsText);
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('must be an object');
      data.questions = parsed;
      questionsError = '';
    } catch (e: any) {
      questionsError = `Invalid JSON: ${e?.message || e}`;
    }
  }
</script>

<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Decision provider</span>
    <select
      bind:value={data.provider}
      class="mt-0.5 w-full px-2 py-1 text-xs border focus:outline-none focus:ring-1 focus:ring-dark-border-subtle border-dark-border bg-dark-surface"
    >
      <option value="">Select provider</option>
      {#each decisionProviders as p}
        <option value={p.key}>{p.key}</option>
      {/each}
    </select>
  </label>
  {#if decisionProviders.length === 0}
    <p class="mt-1 text-[10px] text-amber-600">No provider of type systemone. Add one on the Providers page (preset "Laya / System 1 decisions").</p>
  {/if}
</div>
<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Checkpoint</span>
    <select
      bind:value={data.model}
      class="mt-0.5 w-full px-2 py-1 text-xs border focus:outline-none focus:ring-1 focus:ring-dark-border-subtle border-dark-border bg-dark-surface"
    >
      <option value="">Provider default</option>
      {#each availableModels as m}
        <option value={m}>{m}</option>
      {/each}
    </select>
  </label>
</div>
<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Questions (JSON)</span>
    <textarea
      bind:value={questionsText}
      oninput={onQuestionsInput}
      rows={10}
      spellcheck="false"
      class="mt-0.5 w-full px-2 py-1 text-xs font-mono border focus:outline-none focus:ring-1 focus:ring-dark-border-subtle resize-y border-dark-border bg-dark-surface"
    ></textarea>
  </label>
  {#if questionsError}
    <p class="mt-1 text-[10px] text-red-600">{questionsError}</p>
  {:else}
    <p class="mt-1 text-[10px] text-dark-text-muted">
      Types: <code>choice</code> (criteria object), <code>score</code> (criteria list), <code>noul</code> (yes probability). For yes/no, a two-option choice is more reliable than noul.
    </p>
  {/if}
</div>
<div class="grid grid-cols-2 gap-2">
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Escalate below</span>
    <input
      type="number"
      min="0"
      max="1"
      step="0.05"
      bind:value={data.min_confidence}
      class="mt-0.5 w-full px-2 py-1 text-xs border focus:outline-none focus:ring-1 focus:ring-dark-border-subtle border-dark-border bg-dark-surface"
      placeholder="0 = off"
    />
  </label>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Max tokens</span>
    <input
      type="number"
      min="0"
      step="256"
      bind:value={data.max_len}
      class="mt-0.5 w-full px-2 py-1 text-xs border focus:outline-none focus:ring-1 focus:ring-dark-border-subtle border-dark-border bg-dark-surface"
      placeholder="service default"
    />
  </label>
</div>
<p class="text-[10px] text-dark-text-muted">Fit the threshold on your own labelled data; shipped checkpoints are over-confident until calibrated.</p>
<!-- Port descriptions -->
<div class="border-t border-dark-border pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div>
        <span class="text-[11px] font-mono font-medium text-dark-text">state</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Text or JSON the questions are about (required)</span>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div>
        <span class="text-[11px] font-mono font-medium text-dark-text">decided</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Every answer met the threshold</span>
      </div>
      <div>
        <span class="text-[11px] font-mono font-medium text-dark-text">escalate</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— At least one answer was below it</span>
      </div>
      <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"{ answers, model, low_confidence: string[], state, usage }"}</div>
      <div class="text-[10px] text-dark-text-muted ml-2">Read answers with a pointer such as <code>/answers/department/choice</code> (e.g. in a Switch).</div>
    </div>
  </div>
</div>
