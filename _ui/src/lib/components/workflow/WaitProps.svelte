<script lang="ts">
  let { data }: { data: Record<string, any> } = $props();
</script>

<div class="space-y-4 text-sm text-gray-900 dark:text-dark-text">
  <p class="text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary">Saves the workflow state and releases the worker. Completed steps are not repeated when this wait resumes. Runs are available under Saved runs.</p>
  <div><label for="wait-mode" class="block text-xs">Resume when</label>
    <select id="wait-mode" bind:value={data.mode} class="mt-1 w-full border border-gray-300 bg-white p-2 dark:border-dark-border-subtle dark:bg-dark-elevated"><option value="duration">A duration has elapsed</option><option value="approval">An approval is received</option></select>
  </div>
  {#if data.mode === 'approval'}
    <p class="text-xs text-gray-600 dark:text-dark-text-secondary">The initiator or a workspace administrator with execution access can approve.</p>
    <div><label for="wait-prompt" class="block text-xs">Approval instructions</label><textarea id="wait-prompt" bind:value={data.prompt} rows="4" placeholder="Review the generated content before publishing." class="mt-1 w-full border border-gray-300 bg-white p-2 text-sm dark:border-dark-border-subtle dark:bg-dark-elevated"></textarea></div>
    <div><label for="wait-expiry" class="block text-xs">Approval expires after (seconds)</label><input id="wait-expiry" type="number" min="1" max="2592000" step="1" bind:value={data.expires_seconds} class="mt-1 w-full border border-gray-300 bg-white p-2 dark:border-dark-border-subtle dark:bg-dark-elevated" /></div>
    <p class="text-xs text-gray-600 dark:text-dark-text-secondary">Default: 604800 seconds (7 days). Rejecting or expiring an approval ends the run.</p>
  {:else}
    <div><label for="wait-seconds" class="block text-xs">Duration (seconds)</label><input id="wait-seconds" type="number" min="1" max="2592000" step="1" bind:value={data.seconds} class="mt-1 w-full border border-gray-300 bg-white p-2 dark:border-dark-border-subtle dark:bg-dark-elevated" /></div>
  {/if}
  <p class="border-t border-gray-200 pt-3 text-xs leading-relaxed text-gray-600 dark:border-dark-border dark:text-dark-text-secondary">Maximum 30 days. Resumes with the original initiator's live permissions and the saved graph. Use JSON data or artifact references. Durable runs do not yet support Loop fan-out or a Wait inside a nested workflow call.</p>
</div>
