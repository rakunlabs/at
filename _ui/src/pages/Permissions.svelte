<script lang="ts">
  import { onMount } from 'svelte';
  import { workspaceAPI, type Bundle, type EffectiveAccess, type Mapping } from '../lib/api/workspaces';
  import { identityAPI } from '../lib/api/identity';
  import { workspaceState, can } from '../lib/store/workspace.svelte';
  import { isNativeAdmin, storeAuth } from '../lib/store/auth.svelte';
  import { storeNavbar } from '../lib/store/store.svelte';
  import { authErrorMessage } from '../lib/api/auth';

  storeNavbar.title = 'Permissions';

  let bundles = $state<Bundle[]>([]); let mappings = $state<Mapping[]>([]); let registry = $state<{key: string; platform_only: boolean}[]>([]);
  let effective = $state<EffectiveAccess | null>(workspaceState.access); let user = $state(storeAuth.identity?.subject || ''); let loadedUser = $state('');
  let grants = $state<string[]>([]); let denies = $state<string[]>([]); let editing = $state<Bundle | null>(null); let search = $state('');
  let error = $state(''); let notice = $state(''); let busy = $state(false); let provider = $state(''); let claimKind = $state('groups'); let claimValue = $state(''); let permission = $state('');
  // Editing a mapping in place keeps its id, so references and audit trails survive.
  let mappingID = $state(''); let admitRole = $state<'' | 'viewer' | 'member' | 'admin'>('');
  let loginProviders = $state<{id: string; label: string}[]>([]);
  // The explanation is the longest block on the page, so it folds away. It opens
  // where it is the only section that renders, and after an inspection, because
  // the button writes into it and would otherwise look inert.
  let explain = $state(false);
  let manage = $derived(isNativeAdmin() || can('permissions.manage'));
  let admitAllowed = $derived(isNativeAdmin() || can('members.manage'));
  // A disabled provider keeps working in existing mappings but is not offered,
  // so fall back to the raw identifier rather than silently rewriting it.
  let providerListed = $derived(!provider || loginProviders.some(p => p.id === provider));
  let capabilities = $derived(registry.filter(c => !c.platform_only && c.key.includes(search)));
  async function load() { if (!manage) return; busy = true; error = ''; try { const [b,c,m] = await Promise.all([workspaceAPI.get('permissions'), workspaceAPI.get('permissions/capabilities'), workspaceAPI.get('permission-mappings')]); bundles = b.data.items || []; registry = c.data.items || []; mappings = m.data.items || []; } catch (e) { error = authErrorMessage(e, 'Could not load workspace permissions.'); } finally { busy = false; }
    try { loginProviders = (await identityAPI.get('login-providers')).data || []; } catch { loginProviders = []; } }
  function editMapping(m?: Mapping) {
    mappingID = m?.id || ''; provider = m?.provider_id || ''; claimKind = m?.claim_kind || 'groups';
    claimValue = m?.claim_value || ''; permission = m?.permission_id || ''; admitRole = m?.admit_role || '';
  }
  onMount(() => { explain = !manage; void load(); });
  async function run(fn: () => Promise<void>) { if (busy) return; busy = true; error = notice = ''; try { await fn(); notice = 'Permissions updated.'; } catch (e) { error = authErrorMessage(e, 'Permission change failed. Reload and retry.'); } finally { busy = false; } }
  async function inspect() { busy = true; error = ''; loadedUser = ''; try { const [g,e] = await Promise.all([workspaceAPI.get(`user-permissions/${encodeURIComponent(user)}`), workspaceAPI.get(`users-effective/${encodeURIComponent(user)}`)]); grants = g.data.permission_ids || []; effective = e.data; denies = effective?.denied || []; loadedUser = user; explain = true; } catch (e) { effective = null; error = authErrorMessage(e, 'Could not inspect this workspace member.'); } finally { busy = false; } }
  function edit(bundle?: Bundle) { editing = bundle ? structuredClone($state.snapshot(bundle)) : {id:'',key:'',name:'',description:'',keys:[],key_patterns:{},resource_ids:{}}; }
  function toggleKey(key: string, checked: boolean) { if (!editing) return; editing.keys = checked ? [...editing.keys, key] : editing.keys.filter(k => k !== key); if (!checked) { delete editing.key_patterns[key]; delete editing.resource_ids?.[key]; } }
  function selector(key: string, kind: 'key_patterns' | 'resource_ids', value: string) { if (!editing) return; const entries = value.split('\n').map(v => v.trim()).filter(Boolean); const map = editing[kind] ||= {}; if (entries.length) map[key] = entries; else delete map[key]; }
</script>
<svelte:head><title>AT | Permissions</title></svelte:head>
<div class="settings-page settings-form"><header><h1 class="settings-title">Permissions</h1><p class="settings-subtitle">Reusable permission bundles, direct assignments and explicit per-user denies.</p></header>
{#if error}<p role="alert" class="settings-error">{error}</p>{/if}{#if notice}<p role="status" class="settings-note">{notice}</p>{/if}
{#if manage}
  <section class="settings-section"><div class="flex flex-wrap justify-between gap-3"><h2 class="settings-section-title">Permission bundles</h2><div class="flex gap-2"><button class="settings-button" disabled={busy} onclick={load}>Reload</button><button class="settings-button" disabled={busy} onclick={() => edit()}>New bundle</button></div></div>
    {#if !bundles.length}<p class="settings-note">No custom bundles. Each member's workspace role still supplies baseline access; bundles only add to it.</p>{/if}
    <ul class="settings-list">{#each bundles as b}<li class="flex flex-wrap justify-between gap-3"><div><strong>{b.name}</strong><p class="settings-note">{b.description || b.key} · {b.keys.length} capabilities</p></div><div class="flex gap-2"><button class="settings-button" disabled={busy} onclick={() => edit(b)}>Edit</button><button class="settings-button" disabled={busy} onclick={() => { if (confirm(`Delete permission bundle “${b.name}”?`)) void run(async () => { await workspaceAPI.delete(`permissions/${encodeURIComponent(b.id)}`); await load(); }); }}>Delete</button></div></li>{/each}</ul>
    {#if editing}<form class="settings-section" onsubmit={e => { e.preventDefault(); void run(async () => { const {id,...body} = editing!; if (id) await workspaceAPI.put(`permissions/${encodeURIComponent(id)}`, body); else await workspaceAPI.post('permissions', body); editing = null; await load(); }); }}>
      <h3 class="settings-subsection-title">{editing.id ? 'Edit bundle' : 'New bundle'}</h3><div class="grid sm:grid-cols-2 gap-3"><label>Key<input bind:value={editing.key} required /></label><label>Name<input bind:value={editing.name} required /></label></div><label>Description<input bind:value={editing.description} /></label>
      <label>Filter capability registry<input type="search" bind:value={search} /></label><p class="settings-note">Select known workspace capabilities. Empty selector fields mean unrestricted within this workspace. IDs and paths intersect within each grant; grants combine. Path globs are rooted and * does not cross /.</p>
      <div class="max-h-96 overflow-y-auto space-y-4">{#each capabilities as c}<div class="border-b border-gray-200 dark:border-dark-border pb-3"><label><input type="checkbox" checked={editing.keys.includes(c.key)} onchange={e => toggleKey(c.key, e.currentTarget.checked)} />{c.key}</label>{#if editing.keys.includes(c.key)}<div class="grid sm:grid-cols-2 gap-3 mt-3"><label>Resource IDs (one per line)<textarea rows="2" value={editing.resource_ids?.[c.key]?.join('\n') || ''} oninput={e => selector(c.key, 'resource_ids', e.currentTarget.value)}></textarea></label><label>Path patterns (one per line)<textarea rows="2" value={editing.key_patterns[c.key]?.join('\n') || ''} oninput={e => selector(c.key, 'key_patterns', e.currentTarget.value)}></textarea></label></div>{/if}</div>{/each}</div>
      <div class="flex gap-3"><button class="settings-primary" disabled={busy || !editing.keys.length}>Save bundle</button><button type="button" class="settings-button" onclick={() => editing = null}>Cancel</button></div>
    </form>{/if}
  </section>
  <section class="settings-section"><h2 class="settings-section-title">Member assignments and denies</h2><form class="flex flex-wrap items-end gap-3" onsubmit={e => { e.preventDefault(); void inspect(); }}><label class="flex-1">User ID<input bind:value={user} required /></label><button class="settings-button" disabled={busy}>Inspect access</button></form>
    {#if loadedUser === user && loadedUser}<div class="grid sm:grid-cols-2 gap-3"><fieldset class="space-y-3"><legend class="settings-subsection-title mb-3">Direct bundle assignments</legend>{#each bundles as b}<label><input type="checkbox" value={b.id} bind:group={grants} />{b.name}</label>{/each}<button class="settings-button" disabled={busy} onclick={() => run(async () => { await workspaceAPI.put(`user-permissions/${encodeURIComponent(user)}`, { permission_ids: grants }); await inspect(); })}>Save direct assignments</button></fieldset><fieldset class="space-y-3"><legend class="settings-subsection-title mb-3">Explicit denies</legend><p class="settings-note">Denies subtract after roles, direct bundles and provider mappings, including owner/admin grants.</p><div class="max-h-64 overflow-y-auto space-y-2">{#each registry.filter(c => !c.platform_only) as c}<label><input type="checkbox" value={c.key} bind:group={denies} />{c.key}</label>{/each}</div><button class="settings-button" disabled={busy} onclick={() => run(async () => { await workspaceAPI.put(`users-denied/${encodeURIComponent(user)}`, { capability_keys: denies }); await inspect(); })}>Save denies</button></fieldset></div>{/if}
  </section>
{/if}
<section class="settings-section"><details bind:open={explain}><summary class="settings-section-title cursor-pointer">Effective access explanation</summary><div class="space-y-4 mt-4">
  {#if effective}<p class="settings-note">User {effective.user_id} · {effective.role} · membership version {effective.membership_version}. Execution {effective.execution_enabled ? 'enabled' : 'disabled'}.</p><p class="settings-note">Capabilities permit actions only on matching resources. Sources below preserve pre-deny provenance; a listed source does not override a deny or disabled execution.</p>
    {#if effective.denied?.length}<p class="settings-error">Explicitly denied: {effective.denied.join(', ')}</p>{/if}
    <ul class="settings-list">{#each effective.sources || [] as source}<li class="space-y-1"><div class="flex flex-wrap justify-between gap-2"><strong>{source.capability}</strong><span>{effective.denied?.includes(source.capability) ? 'Denied' : effective.capabilities?.includes(source.capability) ? 'Allowed on matching resources' : 'Inactive'}</span></div><p class="settings-note break-all">Source: {source.source}{source.permission_id ? ` · bundle ${source.permission_id}` : ''}{source.provider_id ? ` · provider ${source.provider_id}` : ''}{source.mapping_id ? ` · mapping ${source.mapping_id}` : ''}</p><p class="settings-note break-all">Resource IDs: {source.resource_ids?.join(', ') || 'All in workspace'} · Paths: {source.path_patterns?.join(', ') || 'All in workspace'}</p></li>{/each}</ul>
  {:else}<p class="settings-note">Select a workspace or inspect a member to see resolved permissions.</p>{/if}
</div></details></section>
{#if manage}<section class="settings-section"><h2 class="settings-section-title">Provider-qualified mappings</h2><p class="settings-note">Map verified claims from one immutable provider ID to a bundle. Email is never an identity key. Nested role claims such as Keycloak's <code>realm_access.roles</code> are matched only after their paths are declared for that provider in Authentication settings.</p>
  <form class="space-y-4" onsubmit={e => { e.preventDefault(); void run(async () => { await workspaceAPI.post('permission-mappings', { ...(mappingID ? { id: mappingID } : {}), provider_id: provider, claim_kind: claimKind, claim_value: claimValue, permission_id: permission, admit_role: admitRole }); editMapping(); await load(); }); }}>
    <div class="grid sm:grid-cols-2 gap-3">
      <label>Identity provider
        {#if loginProviders.length && providerListed}
          <select bind:value={provider} required><option value="">Choose provider</option>{#each loginProviders as p}<option value={p.id}>{p.label} · {p.id}</option>{/each}</select>
        {:else}
          <input bind:value={provider} required spellcheck="false" />
        {/if}
        <span class="settings-note">{loginProviders.length ? 'Enabled providers only. A disabled provider keeps its mappings but never matches.' : 'No enabled provider is available; enter the immutable provider ID.'}</span>
      </label>
      <label>Claim kind<select bind:value={claimKind}><option>groups</option><option>roles</option><option>permissions</option><option>scope</option><option>scopes</option></select><span class="settings-note">roles and scopes match the provider's reported lists; groups, permissions and scope match the recorded claim of that name.</span></label>
      <label>Exact claim value<input bind:value={claimValue} required spellcheck="false" /></label>
      <label>Permission bundle<select bind:value={permission} required><option value="">Choose bundle</option>{#each bundles as b}<option value={b.id}>{b.name}</option>{/each}</select></label>
      <label>Workspace admission<select bind:value={admitRole} disabled={!admitAllowed}><option value="">Existing members only</option><option value="viewer">Add matching users as viewer</option><option value="member">Add matching users as member</option><option value="admin">Add matching users as admin</option></select></label>
    </div>
    <p class="settings-note">{admitAllowed ? 'Existing members only grants the bundle to somebody who is already a member. Any other choice admits a matching identity that has no membership at all: that role supplies baseline access and the bundle adds to it. An existing role is never changed, a revoked membership is never restored, owner is never admissible, and you cannot admit above your own role.' : 'Adding members requires the members.manage capability, because it creates workspace membership rather than granting capabilities to an existing member.'}</p>
    <div class="flex gap-3"><button class="settings-primary" disabled={busy}>{mappingID ? 'Save mapping' : 'Add mapping'}</button>{#if mappingID}<button type="button" class="settings-button" onclick={() => editMapping()}>Cancel edit</button>{/if}</div>
  </form>
  <ul class="settings-list">{#each mappings as m}<li class="flex flex-wrap justify-between gap-3"><div class="min-w-0"><p class="break-all">{m.provider_id} · {m.claim_kind} = {m.claim_value}</p><p class="settings-note">{bundles.find(b => b.id === m.permission_id)?.name || m.permission_id} · {m.admit_role ? `adds matching users as ${m.admit_role}` : 'existing members only'}</p></div><div class="flex gap-2"><button class="settings-button" disabled={busy} onclick={() => editMapping(m)}>Edit</button><button class="settings-button" disabled={busy} onclick={() => { if (confirm('Delete this provider mapping?')) void run(async () => { await workspaceAPI.delete(`permission-mappings/${encodeURIComponent(m.id)}`); if (mappingID === m.id) editMapping(); await load(); }); }}>Delete</button></div></li>{/each}</ul>
</section>{/if}
</div>
