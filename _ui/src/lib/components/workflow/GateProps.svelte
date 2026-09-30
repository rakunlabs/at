<script lang="ts">
  let { data }: { data: Record<string, any> } = $props();
</script>

<div class="space-y-4 text-sm text-gray-900 dark:text-dark-text">
  <p class="text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary">Holds <span class="font-mono">data</span> until the branch connected to <span class="font-mono">signal</span> has finished in this run, then passes <span class="font-mono">data</span> on unchanged.</p>
  <p class="text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary">If the signal branch did not run (an inactive condition, or a failed step skipped by its error setting), this branch stops here too.</p>
  <label class="flex items-center gap-2 text-xs">
    <input type="checkbox" bind:checked={data.pass_signal} class="border-gray-300" />
    Also output the signal value as <span class="font-mono">signal</span>
  </label>
  <p class="border-t border-gray-200 pt-3 text-xs leading-relaxed text-gray-600 dark:border-dark-border dark:text-dark-text-secondary">Works inside a single run, including Loop items. To pause for a duration or an approval, use Wait / Approval.</p>
</div>
