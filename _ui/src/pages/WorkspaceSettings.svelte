<script lang="ts">
  import { onMount } from 'svelte';
  import { workspaceAPI, acceptInvitation, createInvitation, type Member, type Invitation } from '../lib/api/workspaces';
  import { identityAPI } from '../lib/api/identity';
  import { authErrorMessage } from '../lib/api/auth';
  import { workspaceState, can } from '../lib/store/workspace.svelte';
  import { workspaceTransport, switchWorkspace } from '../lib/api/transport';
  import { isNativeAdmin, storeAuth } from '../lib/store/auth.svelte';
  import { downloadSecret } from '../lib/helper/recovery';
  import { storeNavbar } from '../lib/store/store.svelte';
  storeNavbar.title = 'Workspace';
  const id = workspaceTransport.selected; const path = `workspaces/${encodeURIComponent(id)}`;
  let workspace = $state(workspaceState.items.find(w => w.id === id));
  let members = $state<Member[]>([]); let invitations = $state<Invitation[]>([]); let error = $state(''); let notice = $state(''); let busy = $state(false);
  let name = $state(''); let owner = $state(storeAuth.identity?.subject || ''); let memberID = $state(''); let role = $state('member');
  let target = $state(''); let targetKind = $state('user_id'); let inviteRole = $state('member'); let days = $state(7); let invitationToken = $state(''); let acceptToken = $state('');
  let mayManage = $derived(isNativeAdmin() || can('members.manage'));
  let mayEdit = $derived(isNativeAdmin() || can('workspace.write'));
  let roleOptions = $derived(['viewer','member','admin','owner'].slice(0, isNativeAdmin() ? 4 : ['viewer','member','admin','owner'].indexOf(workspaceState.access?.role || '') + 1));
  async function load() { if (!id || !mayManage) return; busy = true; error = ''; try { const [m, i] = await Promise.all([workspaceAPI.get(`${path}/members`), workspaceAPI.get(`${path}/invitations`)]); members = m.data.items || []; invitations = i.data.items || []; } catch (e) { error = authErrorMessage(e, 'Could not load workspace members.'); } finally { busy = false; } }
  onMount(() => { void load(); return () => { invitationToken = acceptToken = ''; }; });
  async function run(fn: () => Promise<void>) { if (busy) return; busy = true; error = notice = ''; try { await fn(); notice = 'Changes saved.'; } catch (e) { error = authErrorMessage(e, 'The workspace change could not be saved. Check your permission and retry.'); } finally { busy = false; } }
  async function setMember(user: string, role: string, status: string) { await workspaceAPI.put(`${path}/members/${encodeURIComponent(user)}`, { role, status }); await load(); }
</script>
<div class="settings-page settings-form"><header><h1 class="text-2xl font-semibold">{workspace?.name || 'Workspace access'}</h1><p class="settings-note mt-2">{workspace ? 'Manage membership and invitations for the selected workspace.' : 'Your account is ready. Join a workspace to start working.'}</p></header>
  {#if error}<p role="alert" class="settings-error">{error}</p>{/if}{#if notice}<p role="status" class="settings-note">{notice}</p>{/if}
  <section class="settings-section"><h2 class="text-lg font-semibold">Join a workspace</h2><p class="settings-note">Paste an invitation token from a workspace owner. Invitations are bound to your account or a verified linked email address.</p><form class="space-y-4 max-w-lg" onsubmit={e => { e.preventDefault(); void run(async () => { const membership = await acceptInvitation(acceptToken); acceptToken = ''; switchWorkspace(membership.workspace_id); }); }}><label>Invitation token<input type="password" bind:value={acceptToken} required autocomplete="off" /></label><button class="settings-button" disabled={busy}>Accept invitation</button></form></section>
  {#if workspace}
    <section class="settings-section"><h2 class="text-lg font-semibold">Workspace details</h2>
      <form class="space-y-4" onsubmit={e => { e.preventDefault(); void run(async () => { workspace = (await workspaceAPI.put(path, { name: workspace!.name, archived: workspace!.archived, execution_enabled: workspace!.execution_enabled })).data; }); }}>
        <label>Name<input bind:value={workspace.name} required disabled={!mayEdit} /></label><label><input type="checkbox" bind:checked={workspace.execution_enabled} disabled={!mayEdit} />Execution enabled</label><label><input type="checkbox" bind:checked={workspace.archived} disabled={!isNativeAdmin() && !can('workspace.archive')} />Archived</label>
        <p class="settings-note">Disabling execution stops model and execution admission. Membership administration remains available.</p>{#if mayEdit}<button class="settings-primary" disabled={busy}>Save workspace</button>{/if}
      </form>
    </section>
    {#if mayManage}
    <section class="settings-section"><div class="flex justify-between gap-3"><h2 class="text-lg font-semibold">Members</h2><button class="settings-button" disabled={busy} onclick={load}>Reload members</button></div>
      <p class="settings-note">Roles supply baseline permissions. Explicit denies also apply to workspace owners and administrators. The last active owner cannot be removed.</p>
      <ul class="divide-y divide-gray-200 dark:divide-dark-border">{#each members as m}<li class="py-4 flex flex-wrap items-end gap-3"><div class="flex-1 min-w-48"><code class="break-all text-xs">{m.user_id}</code><p class="settings-note">{m.status} · membership version {m.version}</p></div><label>Role<select bind:value={m.role}><option>viewer</option><option>member</option><option>admin</option><option>owner</option></select></label><button class="settings-button" disabled={busy} onclick={() => run(() => setMember(m.user_id, m.role, 'active'))}>Save / activate</button><button class="settings-button" disabled={busy || m.status === 'revoked'} onclick={() => run(() => setMember(m.user_id, m.role, 'revoked'))}>Revoke</button></li>{/each}</ul>
      <form class="space-y-4" onsubmit={e => { e.preventDefault(); void run(async () => { await setMember(memberID, role, 'active'); memberID = ''; }); }}><h3 class="font-semibold">Admit an existing account</h3><div class="grid sm:grid-cols-2 gap-4"><label>User ID<input bind:value={memberID} required /></label><label>Role<select bind:value={role}>{#each roleOptions as option}<option>{option}</option>{/each}</select></label></div><p class="settings-note">You can grant only roles within your current permission ceiling. Resource restrictions and denies may further limit delegation.</p><button class="settings-button" disabled={busy}>Add member</button></form>
    </section>
    <section class="settings-section"><h2 class="text-lg font-semibold">Invitations</h2>
      <form class="space-y-4" onsubmit={e => { e.preventDefault(); void run(async () => { const issued = await createInvitation(id, { role: inviteRole, [targetKind]: target, expires_at: new Date(Date.now() + days * 86400000).toISOString() }); invitationToken = issued.token; target = ''; await load(); }); }}><div class="grid sm:grid-cols-2 gap-4"><label>Target type<select bind:value={targetKind}><option value="user_id">Existing user ID</option><option value="email">Verified email address</option></select></label><label>{targetKind === 'email' ? 'Email address' : 'User ID'}<input type={targetKind === 'email' ? 'email' : 'text'} bind:value={target} required /></label><label>Role<select bind:value={inviteRole}>{#each roleOptions as option}<option>{option}</option>{/each}</select></label><label>Expires in days<input type="number" min="1" max="7" bind:value={days} required /></label></div><button class="settings-primary" disabled={busy}>Create invitation</button></form>
      {#if invitationToken}<div class="space-y-3"><p class="settings-note">This token is shown once. Share privately with the invited person.</p><label>Invitation token<input readonly value={invitationToken} onclick={e => e.currentTarget.select()} /></label><button class="settings-button" onclick={() => downloadSecret('at-workspace-invitation.txt', invitationToken)}>Download invitation</button><button class="settings-button" onclick={() => invitationToken = ''}>Dismiss token</button></div>{/if}
      <ul class="divide-y divide-gray-200 dark:divide-dark-border">{#each invitations as i}<li class="py-3"><strong>{i.email || i.user_id}</strong><p class="settings-note">{i.role} · {i.consumed ? 'Accepted' : Date.parse(i.expires_at) < Date.now() ? 'Expired' : 'Pending'} · expires {new Date(i.expires_at).toLocaleString()}</p></li>{/each}</ul>
    </section>{/if}
  {/if}
  {#if isNativeAdmin()}<section class="settings-section"><h2 class="text-lg font-semibold">Create a workspace</h2><form class="space-y-4" onsubmit={e => { e.preventDefault(); void run(async () => { const { data } = await workspaceAPI.post('workspaces', { name, owner_id: owner }); switchWorkspace(data.id); }); }}><label>Workspace name<input bind:value={name} required /></label><label>Initial owner user ID<input bind:value={owner} required /></label><button class="settings-primary" disabled={busy}>Create workspace</button></form></section>{/if}
</div>
