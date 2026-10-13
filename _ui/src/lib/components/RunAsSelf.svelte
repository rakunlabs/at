<script lang="ts">
  import { storeAuth } from '@/lib/store/auth.svelte';

  interface Props { checked: boolean; hint?: string }
  let { checked = $bindable(), hint = "Runs use this account's current permissions. Untick to choose another account after creating it." }: Props = $props();
</script>

<label class="flex items-start gap-2 pt-3 text-sm text-dark-text">
  <input type="checkbox" bind:checked class="mt-0.5" />
  <span>
    Run as my account{storeAuth.identity?.name ? ` (${storeAuth.identity.name})` : ''}
    <span class="block text-xs text-dark-text-muted">{hint}</span>
  </span>
</label>
