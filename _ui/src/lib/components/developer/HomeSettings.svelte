<script lang="ts">
  import { onMount } from 'svelte';
  import { LoaderCircle, Upload } from 'lucide-svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    getDeveloperHome, resetDeveloperHome, updateDeveloperHome, uploadDeveloperHomeFile, type DeveloperHome,
  } from '@/lib/api/developer-spaces';

  interface Props {
    /** Whether the space container is running; mount changes restart it. */
    running: boolean;
    /** Called after a change that needs the container recreated. */
    onrestart: () => Promise<void> | void;
  }

  let { running, onrestart }: Props = $props();

  let home = $state<DeveloperHome | null>(null);
  let enabled = $state(false);
  let path = $state('');
  let saving = $state(false);
  let uploadPath = $state('');
  let uploadMode = $state('600');
  let uploading = $state(false);
  let fileInput = $state<HTMLInputElement>();

  const changed = $derived(!!home && (enabled !== home.enabled || (path.trim() || home.default_path) !== home.path));

  onMount(async () => {
    try {
      home = await getDeveloperHome();
      enabled = home.enabled;
      path = home.path;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not load home settings', 'alert');
    }
  });

  async function save() {
    saving = true;
    try {
      home = await updateDeveloperHome({ enabled, path: path.trim() });
      path = home.path;
      if (running) {
        addToast('Home saved. Restarting the container to mount it.', 'info');
        await onrestart();
      } else {
        addToast('Home saved. It is mounted the next time the space starts.', 'info');
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not save home settings', 'alert');
    } finally {
      saving = false;
    }
  }

  async function upload(event: Event) {
    const file = (event.currentTarget as HTMLInputElement).files?.[0];
    (event.currentTarget as HTMLInputElement).value = '';
    if (!file) return;
    uploading = true;
    try {
      const result = await uploadDeveloperHomeFile(file, uploadPath.trim(), uploadMode.trim());
      addToast(`Saved ${result.path} (mode ${result.mode})`, 'info');
      uploadPath = '';
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not upload into the home', 'alert');
    } finally {
      uploading = false;
    }
  }

  async function reset() {
    const answer = prompt('This permanently deletes every file in your home (SSH keys, dotfiles, tool settings) in all workspaces. Type "delete" to confirm.');
    if (answer !== 'delete') return;
    try {
      await resetDeveloperHome();
      addToast('Home deleted. The space starts with an empty home.', 'info');
      if (running) await onrestart();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not delete the home', 'alert');
    }
  }
</script>

<div class="mt-2 border-t border-dark-border pt-2">
  <div class="flex flex-wrap items-end gap-2">
    <label class="flex items-center gap-1.5 py-1.5">
      <input type="checkbox" bind:checked={enabled} disabled={!home} />
      Persistent home
    </label>
    <label class="min-w-48 flex-1">Mount at
      <input bind:value={path} disabled={!home || !enabled} placeholder={home?.default_path ?? '/root'} spellcheck="false" autocomplete="off" class="mt-1 block w-full border border-dark-border bg-dark-base px-2 py-1.5 font-mono text-sm text-dark-text disabled:opacity-50" />
    </label>
    <button type="button" onclick={save} disabled={!changed || saving} class="inline-flex items-center gap-1.5 border border-dark-border px-3 py-1.5 font-medium disabled:opacity-50 text-dark-text hover:bg-dark-elevated">
      {#if saving}<LoaderCircle size={13} class="animate-spin motion-reduce:animate-none" />{/if}{running ? 'Save home & restart' : 'Save home'}
    </button>
  </div>

  {#if home?.enabled}
    <div class="mt-2 flex flex-wrap items-end gap-2">
      <label class="min-w-48 flex-1">Upload to (relative to home)
        <input bind:value={uploadPath} placeholder=".ssh/id_ed25519 (empty: file name)" spellcheck="false" autocomplete="off" class="mt-1 block w-full border border-dark-border bg-dark-base px-2 py-1.5 font-mono text-sm text-dark-text" />
      </label>
      <label class="w-20">Mode
        <input bind:value={uploadMode} placeholder="600" inputmode="numeric" class="mt-1 block w-full border border-dark-border bg-dark-base px-2 py-1.5 font-mono text-sm text-dark-text" />
      </label>
      <button type="button" onclick={() => fileInput?.click()} disabled={uploading} class="inline-flex items-center gap-1.5 border border-dark-border px-3 py-1.5 disabled:opacity-50 text-dark-text hover:bg-dark-elevated">
        {#if uploading}<LoaderCircle size={13} class="animate-spin motion-reduce:animate-none" />{:else}<Upload size={13} />{/if}Upload file
      </button>
      <input bind:this={fileInput} type="file" class="hidden" onchange={upload} />
      <button type="button" onclick={reset} class="px-2 py-1.5 text-red-400 hover:bg-dark-elevated">Delete home</button>
    </div>
  {/if}

  <p class="mt-1.5 text-dark-text-muted">
    Your home is one volume for your account, shared by your spaces in every workspace. It is mounted at the path above and set as <code class="font-mono">$HOME</code>,
    so SSH keys, <code class="font-mono">.gitconfig</code> and tool settings survive image and limit changes. Put files there with the upload above or from the terminal.
    Agents in the space run commands as the same user and can read these files; keep only what you are willing to expose to them.
  </p>
</div>
