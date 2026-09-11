<script lang="ts">
  import RecentAuth from './RecentAuth.svelte';
  import { identityAPI } from '../api/identity';
  import { downloadSecret } from '../helper/recovery';
  let { userID, username }: { userID: string; username: string } = $props();
  let verifying = $state(false); let link = $state('');
  async function issue(proof: string) { const {data} = await identityAPI.post(`users/${encodeURIComponent(userID)}/recovery`, {proof}); const url = new URL('.', document.baseURI); url.hash = `recovery=${encodeURIComponent(data.ticket)}`; link = url.href; verifying = false; }
</script>
<div class="settings-form space-y-4 mt-3">
  {#if link}<section class="space-y-3"><h3 class="font-semibold">Recovery link for {username}</h3><p class="settings-note">Expires in 15 minutes. Share privately. Redemption replaces all sign-in methods and ends all sessions; membership and data remain.</p><label>One-time recovery link<input readonly value={link} onclick={e => e.currentTarget.select()} /></label><div class="flex gap-2"><button class="settings-button" onclick={() => downloadSecret('at-account-recovery.txt', link)}>Download recovery link</button><button class="settings-button" onclick={() => link = ''}>Dismiss</button></div></section>
  {:else if verifying}<RecentAuth purpose="recovery.issue" onproof={issue} oncancel={() => verifying = false} />
  {:else}<button class="settings-button" onclick={() => verifying = true}>Issue full recovery link</button>{/if}
</div>
