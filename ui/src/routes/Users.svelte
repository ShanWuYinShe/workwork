<script>
import { onMount } from 'svelte';

let xrayClients = [];
let adminUsers = [];
let generatedJSON = '';
let loading = true;
let error = '';
let newUUID = '';
let newUser = '', newPass = '', pwUser = 'admin', pwPass = '';

onMount(async () => {
  try {
    const r = await fetch('/api/admin/users', { credentials: 'same-origin' });
    if (!r.ok) throw new Error('Auth required');
    const d = await r.json();
    xrayClients = d.xrayClients || [];
    adminUsers = d.adminUsers || [];
    generatedJSON = d.generatedJSON || '';
  } catch(e) { error = e.message; }
  finally { loading = false; }
});

async function addXray() {
  if (!newUUID) return;
  await fetch('/api/admin/users/xray/add', { method: 'POST', credentials: 'same-origin', headers: {'Content-Type':'application/json'}, body: JSON.stringify({uuid: newUUID}) });
  newUUID = '';
  location.reload();
}
async function deleteXray(uuid) {
  if (!confirm('Delete this client?')) return;
  await fetch('/api/admin/users/xray/delete', { method: 'POST', credentials: 'same-origin', headers: {'Content-Type':'application/json'}, body: JSON.stringify({uuid}) });
  location.reload();
}
async function addAdmin() {
  if (!newUser || !newPass) return;
  await fetch('/api/admin/users/admin/add', { method: 'POST', credentials: 'same-origin', headers: {'Content-Type':'application/json'}, body: JSON.stringify({username: newUser, password: newPass}) });
  newUser = ''; newPass = '';
  location.reload();
}
async function deleteAdmin(username) {
  if (!confirm(`Delete user ${username}?`)) return;
  await fetch('/api/admin/users/admin/delete', { method: 'POST', credentials: 'same-origin', headers: {'Content-Type':'application/json'}, body: JSON.stringify({username}) });
  location.reload();
}
async function changePw() {
  if (!pwUser || !pwPass) return;
  await fetch('/api/admin/users/password', { method: 'POST', credentials: 'same-origin', headers: {'Content-Type':'application/json'}, body: JSON.stringify({username: pwUser, password: pwPass}) });
  pwPass = '';
}
function genUUID() {
  newUUID = 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, c => (Math.random()*16|0).toString(16));
}
function copyConfig() {
  navigator.clipboard.writeText(generatedJSON);
}
</script>

<h1>Users</h1>
{#if loading}<p style="color:#64748b;">Loading...</p>{/if}
{#if error}<div style="background:#7f1d1d;color:#fca5a5;padding:0.75rem 1rem;border-radius:0.5rem;margin-bottom:1rem;">{error}</div>{/if}

<div class="card">
  <h2>Xray Clients</h2>
  {#if xrayClients.length}
  <table>
    <thead><tr><th>UUID</th><th>Protocols</th><th>Action</th></tr></thead>
    <tbody>
      {#each xrayClients as c}
      <tr>
        <td style="font-family:monospace;font-size:0.8rem">{c.id}</td>
        <td><span class="badge badge-green">reality</span> <span class="badge badge-gray">xhttp</span></td>
        <td><button class="btn btn-red btn-sm" on:click={() => deleteXray(c.id)}>Delete</button></td>
      </tr>
      {/each}
    </tbody>
  </table>
  {:else}<p style="color:#94a3b8;">No clients.</p>{/if}
</div>

<div class="card">
  <h2>Add Xray Client</h2>
  <div style="display:flex;gap:0.5rem;align-items:end;flex-wrap:wrap;">
    <div>
      <label>UUID</label>
      <input type="text" bind:value={newUUID} placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" style="width:350px;font-family:monospace;">
    </div>
    <button class="btn btn-green" on:click={addXray}>Add</button>
    <button class="btn btn-gray btn-sm" on:click={genUUID}>Generate</button>
  </div>
</div>

<div class="card">
  <h2>Client Configuration</h2>
  <p style="color:#94a3b8;font-size:0.8rem;margin-bottom:0.5rem;">SOCKS5 :10808 · HTTP :10809 · Balancer: reality ↔ xhttp</p>
  <textarea rows="20" readonly>{generatedJSON}</textarea>
  <button class="btn btn-blue btn-sm" on:click={copyConfig} style="margin-top:0.5rem;">Copy Config</button>
</div>

<div class="card">
  <h2>Admin Users</h2>
  {#if adminUsers.length}
  <table>
    <thead><tr><th>Username</th><th>Action</th></tr></thead>
    <tbody>
      {#each adminUsers as u}
      <tr><td>{u}</td><td><button class="btn btn-red btn-sm" on:click={() => deleteAdmin(u)}>Delete</button></td></tr>
      {/each}
    </tbody>
  </table>
  {:else}<p style="color:#94a3b8;">No users.</p>{/if}
</div>

<div class="card">
  <h2>Add Admin User</h2>
  <div style="display:flex;gap:0.5rem;align-items:end;flex-wrap:wrap;">
    <div><label>Username</label><input type="text" bind:value={newUser} style="width:200px;"></div>
    <div><label>Password</label><input type="password" bind:value={newPass} style="width:200px;"></div>
    <button class="btn btn-green" on:click={addAdmin}>Add</button>
  </div>
</div>

<div class="card">
  <h2>Change Password</h2>
  <div style="display:flex;gap:0.5rem;align-items:end;flex-wrap:wrap;">
    <div><label>Username</label><input type="text" bind:value={pwUser} style="width:200px;"></div>
    <div><label>New Password</label><input type="password" bind:value={pwPass} style="width:200px;"></div>
    <button class="btn btn-blue" on:click={changePw}>Change</button>
  </div>
</div>
