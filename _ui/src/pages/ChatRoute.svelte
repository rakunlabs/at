<script lang="ts">
  import { isNativeAdmin, storeAuth } from '@/lib/store/auth.svelte';
  import Chat from './Chat.svelte';
  import PersonalChat from './PersonalChat.svelte';
  let { params = {} }: { params?: { id?: string } } = $props();
</script>

{#if isNativeAdmin()}
  {#key `${storeAuth.identity?.subject}:${params.id || ''}`}
    <PersonalChat id={params.id || ''} owner={storeAuth.identity!.subject} />
  {/key}
{:else if !storeAuth.identity && !params.id}
  <!-- App mounts routes without an identity only in legacy auth mode. -->
  <Chat />
{:else}
  <p class="p-6">Personal chat requires a native administrator account. <a class="underline" href="#/playground">Open Playground</a></p>
{/if}
