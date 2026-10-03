<script lang="ts">
  import { Trash2, Plus } from 'lucide-svelte';
  import { createTraceScore, deleteTraceScore, type TraceScore, type ScoreDataType } from '@/lib/api/traces';
  import { addToast } from '@/lib/store/toast.svelte';
  import { formatScore, formatTraceTime } from '@/lib/helper/trace-view';

  interface Props {
    traceID: string;
    /** Restrict to one observation; empty shows trace-level scores. */
    observationID?: string;
    scores: TraceScore[];
    /** Known score names, offered as suggestions. */
    names?: string[];
    onchange: () => void;
  }
  let { traceID, observationID = '', scores, names = [], onchange }: Props = $props();

  const visible = $derived(scores.filter((s) => (s.observation_id || '') === observationID));
  let name = $state('');
  let dataType = $state<ScoreDataType>('numeric');
  let numeric = $state<number | string | null>('');
  let bool = $state(true);
  let category = $state('');
  let comment = $state('');
  let saving = $state(false);

  async function add(e: Event) {
    e.preventDefault();
    if (!name.trim()) return;
    saving = true;
    try {
      await createTraceScore(traceID, {
        name: name.trim(),
        data_type: dataType,
        observation_id: observationID || undefined,
        value: dataType === 'numeric' ? Number(numeric) : dataType === 'boolean' ? (bool ? 1 : 0) : undefined,
        string_value: dataType === 'categorical' ? category : undefined,
        comment: comment || undefined,
      });
      numeric = category = comment = '';
      onchange();
    } catch (err: any) {
      addToast(err?.response?.data?.message || 'Could not save score', 'alert');
    } finally {
      saving = false;
    }
  }

  async function remove(score: TraceScore) {
    try {
      await deleteTraceScore(score.id);
      onchange();
    } catch (err: any) {
      addToast(err?.response?.data?.message || 'Could not delete score', 'alert');
    }
  }
</script>

<div class="space-y-3">
  {#if visible.length}
    <div class="grid grid-cols-[repeat(auto-fill,minmax(9rem,1fr))] gap-2">
      {#each visible as score (score.id)}
        <div class="group border border-gray-200 bg-white p-2 dark:border-dark-border dark:bg-dark-surface">
          <div class="flex items-center justify-between gap-1">
            <span class="truncate text-[11px] text-gray-500 dark:text-dark-text-muted" title={score.name}>{score.name}</span>
            <button onclick={() => remove(score)} class="invisible text-gray-400 hover:text-red-600 group-hover:visible" title="Delete score"><Trash2 size={11} /></button>
          </div>
          <div class="font-mono text-base font-semibold text-gray-900 dark:text-dark-text">{formatScore(score)}</div>
          <div class="text-[10px] text-gray-400 dark:text-dark-text-muted">{score.source === 'api' ? 'API' : 'Annotation'} · {formatTraceTime(score.created_at)}</div>
          {#if score.comment}<p class="mt-1 whitespace-pre-wrap break-words text-[11px] text-gray-600 dark:text-dark-text-secondary">{score.comment}</p>{/if}
        </div>
      {/each}
    </div>
  {:else}
    <p class="text-xs text-gray-400 dark:text-dark-text-muted">No scores {observationID ? 'on this observation' : 'on this trace'} yet.</p>
  {/if}

  <form onsubmit={add} class="space-y-2 border border-gray-200 bg-gray-50 p-2 dark:border-dark-border dark:bg-dark-base">
    <div class="text-[10px] font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-text-muted">Add {observationID ? 'observation' : 'trace'} score</div>
    <div class="flex flex-wrap gap-2">
      <input bind:value={name} list="trace-score-names" placeholder="Name (e.g. correctness)" required maxlength="64" class="min-w-0 flex-1 border border-gray-300 bg-white px-2 py-1 text-xs dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text" />
      <datalist id="trace-score-names">{#each names as n}<option value={n}></option>{/each}</datalist>
      <select bind:value={dataType} class="border border-gray-300 bg-white px-2 py-1 text-xs dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text">
        <option value="numeric">Numeric</option>
        <option value="boolean">Boolean</option>
        <option value="categorical">Category</option>
      </select>
      {#if dataType === 'numeric'}
        <input bind:value={numeric} type="number" step="any" required placeholder="Value" class="w-24 border border-gray-300 bg-white px-2 py-1 text-xs dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text" />
      {:else if dataType === 'boolean'}
        <select bind:value={bool} class="border border-gray-300 bg-white px-2 py-1 text-xs dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text">
          <option value={true}>true 👍</option>
          <option value={false}>false 👎</option>
        </select>
      {:else}
        <input bind:value={category} required maxlength="128" placeholder="Category" class="w-28 border border-gray-300 bg-white px-2 py-1 text-xs dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text" />
      {/if}
    </div>
    <textarea bind:value={comment} rows="2" maxlength="4000" placeholder="Comment (optional)" class="w-full border border-gray-300 bg-white px-2 py-1 text-xs dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text"></textarea>
    <button type="submit" disabled={saving || !name.trim()} class="inline-flex items-center gap-1 border border-gray-300 bg-white px-2.5 py-1 text-xs text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text-secondary dark:hover:bg-dark-highest"><Plus size={12} /> Add score</button>
  </form>
</div>
