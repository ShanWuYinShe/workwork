<script>
  import { onMount } from 'svelte';
  import {
    getCurrentUser,
    getUsers,
    addAdminUser,
    deleteAdminUser,
    changePassword,
    setUserRole,
  } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';
  import { showConfirm } from '../lib/confirm.svelte.js';
  import { toast } from '../lib/toast.svelte.js';

  let adminUsers = $state([]);
  let loading = $state(true);
  let isAdmin = $state(false);
  let currentUsername = $state('');
  let newUser = $state(''),
    newPass = $state(''),
    newRole = $state('viewer');
  let pwUser = $state(''),
    oldPass = $state(''),
    pwPass = $state('');
  let showNewPass = $state(false);
  let showOldPass = $state(false);
  let showPwPass = $state(false);

  onMount(async () => {
    try {
      const d = await getUsers();
      adminUsers = d.adminUsers || [];
      const me = await getCurrentUser();
      isAdmin = me?.role === 'admin';
      currentUsername = me?.username || '';
      pwUser = currentUsername;
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      loading = false;
    }
  });

  async function addUser() {
    if (!newUser || !newPass) return;
    try {
      await addAdminUser(newUser, newPass, newRole);
      toast(t('userAdded', { user: newUser }), 'success');
      newUser = '';
      newPass = '';
      newRole = 'viewer';
      const d = await getUsers();
      adminUsers = d.adminUsers || [];
    } catch (e) {
      toast(e.message, 'error');
    }
  }

  async function deleteUser(username) {
    if (!(await showConfirm(t('confirmDeleteUser', { username })))) return;
    try {
      await deleteAdminUser(username);
      toast(t('userDeleted', { user: username }), 'success');
      const d = await getUsers();
      adminUsers = d.adminUsers || [];
    } catch (e) {
      toast(e.message, 'error');
    }
  }

  async function changePw() {
    if (!pwUser || !pwPass) return;
    try {
      await changePassword(pwUser, oldPass, pwPass);
      toast(t('passwordChanged', { user: pwUser }), 'success');
      oldPass = '';
      pwPass = '';
    } catch (e) {
      toast(e.message, 'error');
    }
  }

  async function changeRole(username, role) {
    const roleLabel = role === 'admin' ? t('Admin') : t('Viewer');
    if (!(await showConfirm(t('confirmRoleChange', { username, role: roleLabel })))) return;
    try {
      await setUserRole(username, role);
      toast(t('roleSetTo', { user: username, role }), 'success');
      const d = await getUsers();
      adminUsers = d.adminUsers || [];
    } catch (e) {
      toast(e.message, 'error');
    }
  }

  function EyeIcon({ open }) {
    return `<svg class="eye-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/>${open ? '<line x1="1" y1="1" x2="23" y2="23"/>' : ''}</svg>`;
  }
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">{t('users')}</h1>
  </div>

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
          ><tr><th>{t('username')}</th><th>{t('role')}</th><th>{t('created')}</th><th>{t('actions')}</th></tr
          ></thead
        >
        <tbody>
          {#each adminUsers as u}
            <tr>
              <td>
                <div class="user-line">
                  <div
                    class={u.role === 'admin' ? 'table-avatar table-avatar-admin' : 'table-avatar'}
                  >
                    {u.username[0].toUpperCase()}
                  </div>
                  <span class="cell-strong">{u.username}</span>
                </div>
              </td>
              <td>
                <span class="badge badge-{u.role === 'admin' ? 'blue' : 'gray'}">{u.role}</span>
              </td>
              <td class="td-muted">{u.created_at ? u.created_at.slice(0, 10) : '—'}</td>
              <td>
                <div class="td-actions">
                  {#if u.username === currentUsername}
                    <span class="badge badge-gray">{t('currentUser')}</span>
                  {:else if u.role !== 'admin'}
                    <button
                      class="btn btn-primary btn-sm"
                      onclick={() => changeRole(u.username, 'admin')}>{t('setAdmin')}</button
                    >
                    <button class="btn btn-danger btn-sm" onclick={() => deleteUser(u.username)}
                      >{t('delete')}</button
                    >
                  {:else}
                    <button
                      class="btn btn-ghost btn-sm"
                      onclick={() => changeRole(u.username, 'viewer')}>{t('setViewer')}</button
                    >
                    <button class="btn btn-danger btn-sm" onclick={() => deleteUser(u.username)}
                      >{t('delete')}</button
                    >
                  {/if}
                </div>
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
        <input id="add-user" type="text" bind:value={newUser} class="input-w-sm" />
      </div>
      <div class="form-group password-field">
        <label for="add-pass">{t('newPassword')}</label>
        <div class="password-input">
          <input
            id="add-pass"
            type={showNewPass ? 'text' : 'password'}
            bind:value={newPass}
            class="input-w-sm"
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
        <input id="pw-user" type="text" bind:value={pwUser} class="input-w-md" />
      </div>
      {#if !isAdmin}
      <div class="form-group">
        <label for="pw-old">{t('oldPassword')}</label>
        <div class="password-input">
          <input
            id="pw-old"
            type={showOldPass ? 'text' : 'password'}
            bind:value={oldPass}
            class="input-w-md"
          />
          <button
            class="eye-btn"
            onclick={() => (showOldPass = !showOldPass)}
            aria-label={showOldPass ? t('hidePassword') : t('showPassword')}
          >
            {@html EyeIcon({ open: showOldPass })}
          </button>
        </div>
      </div>
      {/if}
      <div class="form-group">
        <label for="pw-pass">{t('newPassword')}</label>
        <div class="password-input">
          <input
            id="pw-pass"
            type={showPwPass ? 'text' : 'password'}
            bind:value={pwPass}
            class="input-w-md"
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
