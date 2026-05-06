<script>
import { onMount } from 'svelte';
import { getUsers, addAdminUser, deleteAdminUser, changePassword, setUserRole } from '../lib/api.js';

let adminUsers = $state([]);
let loading = $state(true);
let error = $state('');
let message = $state('');
let newUser = $state(''), newPass = $state(''), newRole = $state('viewer');
let pwUser = $state(''), pwPass = $state('');
let showNewPass = $state(false);
let showPwPass = $state(false);

onMount(async () => {
  try {
    const d = await getUsers();
    adminUsers = d.adminUsers || [];
  } catch(e) { error = e.message; }
  finally { loading = false; }
});

async function addUser() {
  if (!newUser || !newPass) return;
  error = ''; message = '';
  try {
    await addAdminUser(newUser, newPass, newRole);
    message = `User ${newUser} added.`;
    newUser = ''; newPass = ''; newRole = 'viewer';
    const d = await getUsers();
    adminUsers = d.adminUsers || [];
  } catch(e) { error = e.message; }
}
async function deleteUser(username) {
  if (!confirm(`Delete user "${username}"?`)) return;
  error = '';
  try { await deleteAdminUser(username); const d = await getUsers(); adminUsers = d.adminUsers || []; }
  catch(e) { error = e.message; }
}
async function changePw() {
  if (!pwUser || !pwPass) return;
  error = ''; message = '';
  try {
    await changePassword(pwUser, pwPass);
    message = `Password changed for ${pwUser}.`;
    pwPass = '';
  } catch(e) { error = e.message; }
}
async function changeRole(username, role) {
  error = ''; message = '';
  try {
    await setUserRole(username, role);
    message = `Role for ${username} set to ${role}.`;
    const d = await getUsers();
    adminUsers = d.adminUsers || [];
  } catch(e) { error = e.message; }
}
</script>

<h1>Users</h1>
{#if loading}<p style="color:#64748b;">Loading...</p>{/if}
{#if error}<div class="toast toast-error">{error}</div>{/if}
{#if message}<div class="toast toast-success">{message}</div>{/if}

<div class="card">
  <h2>Admin Users ({adminUsers.length})</h2>
  {#if adminUsers.length}
  <table>
    <thead><tr><th>Username</th><th>Role</th><th>Created</th><th>Actions</th></tr></thead>
    <tbody>
      {#each adminUsers as u}
      <tr>
        <td style="font-weight:500;">{u.username}</td>
        <td>
          <span class="badge" class:badge-green={u.role === 'admin'} class:badge-red={u.role !== 'admin'}>
            {u.role}
          </span>
        </td>
        <td style="color:#94a3b8;font-size:0.8rem;">{u.created_at ? u.created_at.slice(0, 10) : '—'}</td>
        <td style="display:flex;gap:0.25rem;flex-wrap:wrap;">
          {#if u.role === 'admin'}
            <button class="btn btn-gray btn-sm" on:click={() => changeRole(u.username, 'viewer')}>Set viewer</button>
          {:else}
            <button class="btn btn-blue btn-sm" on:click={() => changeRole(u.username, 'admin')}>Set admin</button>
          {/if}
          <button class="btn btn-red btn-sm" on:click={() => deleteUser(u.username)}>Delete</button>
        </td>
      </tr>
      {/each}
    </tbody>
  </table>
  {:else}<p style="color:#64748b;">No admin users.</p>{/if}
</div>

<div class="card">
  <h2>Add User</h2>
  <div style="display:flex;gap:0.75rem;align-items:end;flex-wrap:wrap;">
    <div><label>Username</label><input type="text" bind:value={newUser} style="width:150px;"></div>
    <div style="position:relative;"><label>Password</label><input type={showNewPass ? 'text' : 'password'} bind:value={newPass} style="width:150px;padding-right:32px;"><button class="eye-btn" on:click={() => showNewPass = !showNewPass} title={showNewPass ? 'Hide' : 'Show'}><svg class="eye-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/>{#if showNewPass}<line x1="1" y1="1" x2="23" y2="23"/>{/if}</svg></button></div>
    <div>
      <label>Role</label>
      <select bind:value={newRole} style="background:#0f172a;border:1px solid #334155;color:#e2e8f0;padding:0.5rem 0.75rem;border-radius:0.375rem;font-size:0.875rem;">
        <option value="viewer">Viewer</option>
        <option value="admin">Admin</option>
      </select>
    </div>
    <button class="btn btn-green" on:click={addUser}>Add</button>
  </div>
</div>

<div class="card">
  <h2>Change Password</h2>
  <div style="display:flex;gap:0.75rem;align-items:end;flex-wrap:wrap;">
    <div><label>Username</label><input type="text" bind:value={pwUser} style="width:180px;"></div>
    <div style="position:relative;"><label>New Password</label><input type={showPwPass ? 'text' : 'password'} bind:value={pwPass} style="width:180px;padding-right:32px;"><button class="eye-btn" on:click={() => showPwPass = !showPwPass} title={showPwPass ? 'Hide' : 'Show'}><svg class="eye-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/>{#if showPwPass}<line x1="1" y1="1" x2="23" y2="23"/>{/if}</svg></button></div>
    <button class="btn btn-blue" on:click={changePw}>Change</button>
  </div>
</div>

<style>
  .toast { padding: 0.5rem 0.75rem; border-radius: 0.375rem; margin-bottom: 0.75rem; font-size: 0.875rem; animation: fadeIn 0.2s ease; }
  .toast-success { background: #064e3b; color: #6ee7b7; }
  .toast-error { background: #7f1d1d; color: #fca5a5; }
  @keyframes fadeIn { from { opacity: 0; transform: translateY(-4px); } to { opacity: 1; transform: translateY(0); } }
</style>
