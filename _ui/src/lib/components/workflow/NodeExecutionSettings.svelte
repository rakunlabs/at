<script lang="ts">
  let { data, nodeType, disabled = false }: { data: Record<string, any>; nodeType: string; disabled?: boolean } = $props();
  let attempts = $derived(data.execution?.max_attempts ?? 1);
  function update(patch: Record<string, unknown>) {
    data.execution = { max_attempts: 1, retry_delay_ms: 1000, backoff: 'fixed', on_error: 'stop', ...data.execution, ...patch };
  }
</script>

<fieldset {disabled} class="space-y-4 text-sm text-gray-900 dark:text-dark-text">
  <div><label for="node-max-attempts" class="block">Maximum attempts</label>
    <select id="node-max-attempts" value={attempts} onchange={event => update({ max_attempts: Number(event.currentTarget.value) })} class="mt-1 w-full border border-gray-300 bg-white p-2 text-sm dark:border-dark-border-subtle dark:bg-dark-elevated">
      {#each [1, 2, 3, 4, 5] as count}<option value={count}>{count === 1 ? '1 — no retries' : `${count} attempts`}</option>{/each}
    </select>
  </div>
  <p class="text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary">Includes the first attempt. Only transient HTTP/network failures are retried. Each retry repeats the entire node, including any tool calls or external sends.</p>
  {#if attempts > 1}
    <div><label for="node-retry-delay" class="block">Retry delay (milliseconds)</label>
      <input id="node-retry-delay" type="number" min="0" max="30000" step="100" value={data.execution?.retry_delay_ms ?? 1000} oninput={event => update({ retry_delay_ms: Number(event.currentTarget.value) })} class="mt-1 w-full border border-gray-300 bg-white p-2 dark:border-dark-border-subtle dark:bg-dark-elevated" />
    </div>
    <div><label for="node-retry-backoff" class="block">Delay strategy</label>
      <select id="node-retry-backoff" value={data.execution?.backoff ?? 'fixed'} onchange={event => update({ backoff: event.currentTarget.value })} class="mt-1 w-full border border-gray-300 bg-white p-2 dark:border-dark-border-subtle dark:bg-dark-elevated">
        <option value="fixed">Fixed delay</option><option value="exponential">Exponential backoff</option>
      </select>
    </div>
    <p class="text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary">Backoff is capped at 30 seconds. An upstream Retry-After can extend the wait up to 5 minutes; longer requests stop retries. Cancellation interrupts the wait.</p>
  {/if}
  <div><label for="node-error-policy" class="block">When the node fails</label>
    <select id="node-error-policy" value={data.execution?.on_error ?? 'stop'} onchange={event => update({ on_error: event.currentTarget.value })} class="mt-1 w-full border border-gray-300 bg-white p-2 dark:border-dark-border-subtle dark:bg-dark-elevated">
      <option value="stop">Stop workflow</option>
      <option value="continue">Continue — skip the failed branch</option>
      <option value="error_output">Send error to failure output</option>
    </select>
  </div>
  {#if data.execution?.on_error === 'error_output'}
    <p class="text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary">Apply to expose the failure port. It sends the error message, node identity, attempt count and input to your recovery step. Normal outputs stay inactive on failure.</p>
  {:else if data.execution?.on_error === 'continue'}
    <p class="text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary">Independent branches continue. This node sends no output, so dependent steps are skipped unless another active input reaches them.</p>
  {/if}
  <p class="text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary">Configuration, input-mapping and permission errors still stop execution. Stop/cancellation is never converted into an error branch.</p>
  {#if nodeType === 'http_request'}
    <p class="border-t border-gray-200 pt-3 text-xs leading-relaxed text-gray-600 dark:border-dark-border dark:text-dark-text-secondary">With common retries enabled, transient HTTP statuses (408, 429 and 5xx except 501) are failures and legacy HTTP retry is bypassed. Other response statuses keep the node's success/error/always routing.</p>
  {/if}
</fieldset>
