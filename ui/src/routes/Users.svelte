<script>
  import { onMount } from 'svelte';
  import {
    getUsers,
    addAdminUser,
    deleteAdminUser,
    changePassword,
    setUserRole,
    setModelPoolUser,
  } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';

  let adminUsers = $state([]);
  let loading = $state(true);
  let error = $state('');
  let message = $state('');
  let newUser = $state(''),
    newPass = $state(''),
    newRole = $state('viewer');
  let pwUser = $state(''),
    pwPass = $state('');
  let showNewPass = $state(false);
  let showPwPass = $state(false);

  onMount(async () => {
    try {
      const d = await getUsers();
      adminUsers = d.adminUsers || [];
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  });

  async function addUser() {
    if (!newUser || !newPass) return;
    error = '';
    message = '';
    try {
      await addAdminUser(newUser, newPass, newRole);
      message = t('userAdded', { user: newUser });
      newUser = '';
      newPass = '';
      newRole = 'viewer';
      const d = await getUsers();
      adminUsers = d.adminUsers || [];
    } catch (e) {
      error = e.message;
    }
  }

  async function deleteUser(username) {
    if (!confirm(t('confirmDeleteUser', { username }))) return;
    error = '';
    try {
      await deleteAdminUser(username);
      const d = await getUsers();
      adminUsers = d.adminUsers || [];
    } catch (e) {
      error = e.message;
    }
  }

  async function changePw() {
    if (!pwUser || !pwPass) return;
    error = '';
    message = '';
    try {
      await changePassword(pwUser, pwPass);
      message = t('passwordChanged', { user: pwUser });
      pwPass = '';
    } catch (e) {
      error = e.message;
    }
  }

  async function changeRole(username, role) {
    error = '';
    message = '';
    try {
      await setUserRole(username, role);
      message = t('roleSetTo', { user: username, role });
      const d = await getUsers();
      adminUsers = d.adminUsers || [];
    } catch (e) {
      error = e.message;
    }
  }

  async function togglePool(username, granted) {
    error = '';
    message = '';
    try {
      await setModelPoolUser(username, granted);
      message = granted ? t('poolAccessGranted', { user: username }) : t('poolAccessRevoked', { user: username });
      const d = await getUsers();
      adminUsers = d.adminUsers || [];
    } catch (e) {
      error = e.message;
    }
  }

  function EyeIcon({ open }) {
    return `<svg style="width:16px;height:16px" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/>${open ? '<line x1="1" y1="1" x2="23" y2="23"/>' : ''}</svg>`;
  }
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">{t('users')}</h1>
  </div>

  {#if error}<div class="toast toast-error">{error}</div>{/if}
  {#if message}<div class="toast toast-success">{message}</div>{/if}

  <div class="card">
    <div class="card-header">
      <div class="card-title">
        {t('adminUsers')}
        {#if adminUsers.length}<span class="card-title-count">{adminUsers.length}</span>{/if}
      </div>
    </div>
    {#if loading}
      <div class="loading-skeleton" style="height:120px;"></div>
    {:else if adminUsers.length}
      <table>
        <thead
          ><tr><th>{t('username')}</th><th>{t('role')}</th><th>{t('pool')}</th><th>{t('created')}</th><th>{t('actions')}</th></tr
          ></thead
        >
        <tbody>
          {#each adminUsers as u}
            <tr>
              <td>
                <div style="display:flex;align-items:center;gap:0.5rem;">
                  <div
                    style="width:28px;height:28px;border-radius:6px;display:flex;align-items:center;justify-content:center;font-size:0.7rem;font-weight:700;{u.role ===
                    'admin'
                      ? 'background:linear-gradient(135deg,#2563eb,#7c3aed);color:#fff;'
                      : 'background:var(--c-bg-surface);color:var(--c-text-secondary);'}"
                  >
                    {u.username[0].toUpperCase()}
                  </div>
                  <span style="font-weight:500;">{u.username}</span>
                </div>
              </td>
              <td>
                <span class="badge badge-{u.role === 'admin' ? 'blue' : 'gray'}">{u.role}</span>
              </td>
              <td>
                {#if u.pool_access}
                  <span class="badge badge-purple"><span class="badge-dot"></span>pool</span>
                {:else}
                  <span class="badge badge-gray">—</span>
                {/if}
              </td>
              <td class="td-muted">{u.created_at ? u.created_at.slice(0, 10) : '—'}</td>
              <td>
                {#if u.role !== 'admin'}
                  <div class="td-actions">
                    <button
                      class="btn btn-primary btn-sm"
                      onclick={() => changeRole(u.username, 'admin')}>{t('setAdmin')}</button
                    >
                    {#if u.pool_access}
                      <button
                        class="btn btn-ghost btn-sm"
                        onclick={() => togglePool(u.username, false)}
                        title={t('noPool')}>{t('noPool')}</button
                      >
                    {:else}
                      <button
                        class="btn btn-sm"
                        style="background:#7c3aed;color:#fff;"
                        onclick={() => togglePool(u.username, true)}
                        title={t('grantPool')}>{t('grantPool')}</button
                      >
                    {/if}
                    <button class="btn btn-danger btn-sm" onclick={() => deleteUser(u.username)}
                      >{t('delete')}</button
                    >
                  </div>
                {:else}
                  <span style="color:var(--c-text-muted);font-size:0.78rem;">—</span>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {:else}
      <div class="empty-state">{t('noUsers')}</div>
    {/if}
  </div>

  <div class="card">
    <div class="card-header">
      <div class="card-title">{t('addUser')}</div>
    </div>
    <div class="form-row">
      <div class="form-group">
        <label for="add-user">{t('username')}</label>
        <input id="add-user" type="text" bind:value={newUser} style="width:160px;" />
      </div>
      <div class="form-group" style="position:relative;">
        <label for="add-pass">{t('newPassword')}</label>
        <div style="position:relative;">
          <input
            id="add-pass"
            type={showNewPass ? 'text' : 'password'}
            bind:value={newPass}
            style="width:160px;padding-right:32px;"
          />
          <button
            class="eye-btn"
            onclick={() => (showNewPass = !showNewPass)}
            aria-label={showNewPass ? t('hidePassword') : t('showPassword')}
          >
            {@html EyeIcon({ open: showNewPass })}
          </button>
        </div>
      </div>
      <div class="form-group">
        <label for="add-role">{t('role')}</label>
        <select id="add-role" bind:value={newRole}>
          <option value="viewer">{t('Viewer')}</option>
          <option value="admin">{t('Admin')}</option>
        </select>
      </div>
      <button class="btn btn-success" onclick={addUser}>{t('add')}</button>
    </div>
  </div>

  <div class="card">
    <div class="card-header">
      <div class="card-title">{t('changePassword')}</div>
    </div>
    <div class="form-row">
      <div class="form-group">
        <label for="pw-user">{t('username')}</label>
        <input id="pw-user" type="text" bind:value={pwUser} style="width:180px;" />
      </div>
      <div class="form-group">
        <label for="pw-pass">{t('newPassword')}</label>
        <div style="position:relative;">
          <input
            id="pw-pass"
            type={showPwPass ? 'text' : 'password'}
            bind:value={pwPass}
            style="width:180px;padding-right:32px;"
          />
          <button
            class="eye-btn"
            onclick={() => (showPwPass = !showPwPass)}
            aria-label={showPwPass ? t('hidePassword') : t('showPassword')}
          >
            {@html EyeIcon({ open: showPwPass })}
          </button>
        </div>
      </div>
      <button class="btn btn-primary" onclick={changePw}>{t('change')}</button>
    </div>
  </div>
</div>
