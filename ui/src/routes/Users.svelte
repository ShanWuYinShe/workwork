<script>
import { onMount } from 'svelte';
import { getUsers, addAdminUser, deleteAdminUser, changePassword } from '../lib/api.js';

let adminUsers = [];
let loading = true;
let error = '';
let message = '';
let newUser = '', newPass = '';
let pwUser = '', pwPass = '';

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
    await addAdminUser(newUser, newPass);
    message = `User ${newUser} added.`;
    newUser = ''; newPass = '';
    location.reload();
  } catch(e) { error = e.message; }
}
async function deleteUser(username) {
  if (!confirm(`Delete user "${username}"?`)) return;
  error = '';
  try { await deleteAdminUser(username); location.reload(); }
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
</script>

<h1>Users</h1>
{#if loading}<p style="color:#64748b;">Loading...</p>{/if}
{#if error}<div style="background:#7f1d1d;color:#fca5a5;padding:0.75rem 1rem;border-radius:0.5rem;margin-bottom:1rem;">{error}</div>{/if}
{#if message}<div style="background:#064e3b;color:#6ee7b7;padding:0.75rem 1rem;border-radius:0.5rem;margin-bottom:1rem;">{message}</div>{/if}

<div class="card">
  <h2>Admin Users ({adminUsers.length})</h2>
  {#if adminUsers.length}
  <table>
    <thead><tr><th>Username</th><th>Actions</th></tr></thead>
    <tbody>
      {#each adminUsers as u}
      <tr>
        <td style="font-weight:500;">{u}</td>
        <td><button class="btn btn-red btn-sm" on:click={() => deleteUser(u)}>Delete</button></td>
      </tr>
      {/each}
    </tbody>
  </table>
  {:else}<p style="color:#64748b;">No admin users.</p>{/if}
</div>

<div class="card">
  <h2>Add User</h2>
  <div style="display:flex;gap:0.75rem;align-items:end;flex-wrap:wrap;">
    <div><label>Username</label><input type="text" bind:value={newUser} style="width:180px;"></div>
    <div><label>Password</label><input type="password" bind:value={newPass} style="width:180px;"></div>
    <button class="btn btn-green" on:click={addUser}>Add</button>
  </div>
</div>

<div class="card">
  <h2>Change Password</h2>
  <div style="display:flex;gap:0.75rem;align-items:end;flex-wrap:wrap;">
    <div><label>Username</label><input type="text" bind:value={pwUser} style="width:180px;"></div>
    <div><label>New Password</label><input type="password" bind:value={pwPass} style="width:180px;"></div>
    <button class="btn btn-blue" on:click={changePw}>Change</button>
  </div>
</div>
