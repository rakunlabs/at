<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { rotateKey } from '@/lib/api/admin';
  import { Settings, RotateCw, Eye, EyeOff } from 'lucide-svelte';

  storeNavbar.title = 'Settings';

  // ─── State ───
  let adminToken = $state('');
  let encryptionKey = $state('');
  let confirmKey = $state('');
  let rotating = $state(false);
  let showAdminToken = $state(false);
  let showEncryptionKey = $state(false);
  let disableEncryption = $state(false);

  // ─── Actions ───
  async function handleRotate() {
    if (!adminToken.trim()) {
      addToast('Admin token is required', 'alert');
      return;
    }

    if (!disableEncryption) {
      if (!encryptionKey.trim()) {
        addToast('New encryption key is required (or check "Disable encryption")', 'alert');
        return;
      }
      if (encryptionKey !== confirmKey) {
        addToast('Encryption keys do not match', 'alert');
        return;
      }
    }

    rotating = true;
    try {
      const msg = await rotateKey(adminToken.trim(), disableEncryption ? '' : encryptionKey);
      addToast(msg || 'Encryption key rotated successfully');
      // Clear form on success
      encryptionKey = '';
      confirmKey = '';
      disableEncryption = false;
    } catch (e: any) {
      const status = e?.response?.status;
      const message = e?.response?.data?.message || e?.message || 'Failed to rotate key';
      if (status === 401) {
        addToast('Invalid admin token', 'alert');
      } else if (status === 403) {
        addToast('Admin token not configured on server', 'alert');
      } else {
        addToast(message, 'alert');
      }
    } finally {
      rotating = false;
    }
  }
</script>

<svelte:head>
  <title>AT | Settings</title>
</svelte:head>

<div class="p-6 max-w-5xl mx-auto">
  <!-- Header -->
  <div class="flex items-center gap-2 mb-4">
    <Settings size={16} class="text-gray-500" />
    <h2 class="text-sm font-medium text-gray-900">Settings</h2>
  </div>

  <!-- Rotate Encryption Key -->
  <div class="border border-gray-200 bg-white shadow-sm">
    <div class="px-4 py-3 border-b border-gray-200 flex items-center gap-2">
      <RotateCw size={14} class="text-gray-500" />
      <h3 class="text-sm font-medium text-gray-900">Rotate Encryption Key</h3>
    </div>

    <div class="p-4">
      <p class="text-sm text-gray-600 leading-relaxed mb-4">
        Re-encrypts all stored provider API keys with a new encryption passphrase.
        When clustering is enabled, the new key is broadcast to all peers after rotation.
      </p>

      <!-- Admin Token -->
      <div class="grid grid-cols-4 gap-3 mb-3">
        <label for="admin-token" class="text-xs text-gray-600 py-2">Admin Token</label>
        <div class="col-span-3 relative">
          <input
            id="admin-token"
            type={showAdminToken ? 'text' : 'password'}
            bind:value={adminToken}
            placeholder="Server admin_token"
            class="w-full border border-gray-200 px-2.5 py-1.5 pr-8 text-sm focus:outline-none focus:border-gray-400 font-mono"
          />
          <button
            type="button"
            onclick={() => (showAdminToken = !showAdminToken)}
            class="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600"
            tabindex={-1}
          >
            {#if showAdminToken}
              <EyeOff size={14} />
            {:else}
              <Eye size={14} />
            {/if}
          </button>
        </div>
      </div>

      <!-- Disable Encryption Checkbox -->
      <div class="grid grid-cols-4 gap-3 mb-3">
        <span class="text-xs text-gray-600 py-2"></span>
        <div class="col-span-3 flex items-center gap-2">
          <input
            id="disable-encryption"
            type="checkbox"
            bind:checked={disableEncryption}
            class="accent-gray-900"
          />
          <label for="disable-encryption" class="text-xs text-gray-600 select-none">
            Disable encryption (store credentials as plaintext)
          </label>
        </div>
      </div>

      {#if !disableEncryption}
        <!-- New Encryption Key -->
        <div class="grid grid-cols-4 gap-3 mb-3">
          <label for="encryption-key" class="text-xs text-gray-600 py-2">New Encryption Key</label>
          <div class="col-span-3 relative">
            <input
              id="encryption-key"
              type={showEncryptionKey ? 'text' : 'password'}
              bind:value={encryptionKey}
              placeholder="New encryption passphrase"
              class="w-full border border-gray-200 px-2.5 py-1.5 pr-8 text-sm focus:outline-none focus:border-gray-400 font-mono"
            />
            <button
              type="button"
              onclick={() => (showEncryptionKey = !showEncryptionKey)}
              class="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600"
              tabindex={-1}
            >
              {#if showEncryptionKey}
                <EyeOff size={14} />
              {:else}
                <Eye size={14} />
              {/if}
            </button>
          </div>
        </div>

        <!-- Confirm Encryption Key -->
        <div class="grid grid-cols-4 gap-3 mb-4">
          <label for="confirm-key" class="text-xs text-gray-600 py-2">Confirm Key</label>
          <div class="col-span-3">
            <input
              id="confirm-key"
              type="password"
              bind:value={confirmKey}
              placeholder="Confirm new encryption passphrase"
              class="w-full border border-gray-200 px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400 font-mono"
            />
          </div>
        </div>
      {/if}

      <div class="flex items-center gap-3">
        <button
          onclick={handleRotate}
          disabled={rotating}
          class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 transition-colors disabled:opacity-50"
        >
          <RotateCw size={12} class={rotating ? 'animate-spin' : ''} />
          {rotating ? 'Rotating...' : 'Rotate Key'}
        </button>
      </div>

      <p class="mt-3 text-xs text-gray-400 leading-relaxed">
        The admin token must match the <code class="font-mono bg-gray-100 px-1 py-0.5 text-gray-600">admin_token</code> configured in the server settings.
        If no admin token is configured on the server, this operation will be rejected.
      </p>
    </div>
  </div>
</div>
