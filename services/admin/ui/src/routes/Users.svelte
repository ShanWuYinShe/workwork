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
  import { eyeIcon } from '../lib/eye-icon.js';

  let adminUsers = $state([]);
  let loading = $state(true);
  let isAdmin = $state(false);
  let currentUsername = $state('');
  let newUser = $state(''),
    newPass = $state(''),
    newRole = $state('viewer');
  let addingUser = $state(false);
  let changingPw = $state(false);
  let pwUser = $state(''),
    oldPass = $state(''),
    pwPass = $state('');
  let showNewPass = $state(false);
  let showOldPass = $state(false);
  let showPwPass = $state(false);
  let addUserError = $state('');
  let pwError = $state('');

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

  // 字段级校验：与后端 IsValidUsername（^[a-zA-Z0-9_-]+$，≤64）对齐，
  // 空字段给出明确提示而不是静默 return。
  function validateNewUser() {
    const name = newUser.trim();
    if (!name) return t('errUsernameRequired');
    if (!/^[a-zA-Z0-9_-]+$/.test(name) || name.length > 64) return t('errUsernameFormat');
    if (!newPass) return t('errPasswordRequired');
    return '';
  }

  function validatePwForm() {
    if (!pwUser.trim()) return t('errUsernameRequired');
    if (!isAdmin && !oldPass) return t('errOldPasswordRequired');
    if (!pwPass) return t('errPasswordRequired');
    return '';
  }

  async function addUser() {
    addUserError = validateNewUser();
    if (addUserError || addingUser) return;
    addingUser = true;
    try {
      await addAdminUser(newUser.trim(), newPass, newRole);
      toast(t('userAdded', { user: newUser.trim() }), 'success');
      newUser = '';
      newPass = '';
      newRole = 'viewer';
      const d = await getUsers();
      adminUsers = d.adminUsers || [];
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      addingUser = false;
    }
  }

  async function deleteUser(username) {
    if (!(await showConfirm(t('confirmDeleteUser', { username }), { danger: true }))) return;
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
    pwError = validatePwForm();
    if (pwError || changingPw) return;
    changingPw = true;
    try {
      await changePassword(pwUser.trim(), oldPass, pwPass);
      toast(t('passwordChanged', { user: pwUser }), 'success');
      oldPass = '';
      pwPass = '';
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      changingPw = false;
    }
  }

  async function changeRole(username, role) {
    const roleLabel = role === 'admin' ? t('Admin') : t('Viewer');
    if (!(await showConfirm(t('confirmRoleChange', { username, role: roleLabel })))) return;
    try {
      await setUserRole(username, role);
      toast(t('roleSetTo', { user: username, role: roleLabel }), 'success');
      const d = await getUsers();
      adminUsers = d.adminUsers || [];
    } catch (e) {
      toast(e.message, 'error');
    }
  }
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">{t('users')}</h1>
  </div>

  {#if isAdmin}
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
        <div class="table-wrap">
          <table>
            <thead
              ><tr
                ><th>{t('username')}</th><th>{t('role')}</th><th>{t('created')}</th><th
                  >{t('actions')}</th
                ></tr
              ></thead
            >
            <tbody>
              {#each adminUsers as u}
                <tr>
                  <td>
                    <div class="user-line">
                      <div
                        class={u.role === 'admin'
                          ? 'table-avatar table-avatar-admin'
                          : 'table-avatar'}
                      >
                        {(u.username || '?')[0].toUpperCase()}
                      </div>
                      <span class="cell-strong">{u.username}</span>
                    </div>
                  </td>
                  <td>
                    <span class="badge badge-{u.role === 'admin' ? 'blue' : 'gray'}"
                      >{u.role === 'admin' ? t('Admin') : t('Viewer')}</span
                    >
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
        </div>
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
          <input
            id="add-user"
            type="text"
            bind:value={newUser}
            class="input-w-sm"
            aria-invalid={addUserError ? 'true' : undefined}
            aria-describedby={addUserError ? 'add-user-error' : undefined}
          />
        </div>
        <div class="form-group password-field">
          <label for="add-pass">{t('newPassword')}</label>
          <div class="password-input">
            <input
              id="add-pass"
              type={showNewPass ? 'text' : 'password'}
              bind:value={newPass}
              class="input-w-sm"
              aria-invalid={addUserError ? 'true' : undefined}
            />
            <button
              class="eye-btn"
              onclick={() => (showNewPass = !showNewPass)}
              aria-label={showNewPass ? t('hidePassword') : t('showPassword')}
            >
              {@html eyeIcon(showNewPass)}
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
        <button class="btn btn-primary" onclick={addUser} disabled={addingUser}
          >{addingUser ? t('saving') : t('add')}</button
        >
      </div>
      {#if addUserError}
        <p class="field-error" id="add-user-error" role="alert">{addUserError}</p>
      {/if}
    </div>
  {/if}

  <div class="card">
    <div class="card-header">
      <div class="card-title">{t('changePassword')}</div>
    </div>
    <div class="form-row">
      <div class="form-group">
        <label for="pw-user">{t('username')}</label>
        <input
          id="pw-user"
          type="text"
          bind:value={pwUser}
          class="input-w-md"
          aria-invalid={pwError ? 'true' : undefined}
          aria-describedby={pwError ? 'pw-error' : undefined}
        />
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
              {@html eyeIcon(showOldPass)}
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
            {@html eyeIcon(showPwPass)}
          </button>
        </div>
      </div>
      <button class="btn btn-primary" onclick={changePw} disabled={changingPw}
        >{changingPw ? t('saving') : t('change')}</button
      >
    </div>
    {#if pwError}
      <p class="field-error" id="pw-error" role="alert">{pwError}</p>
    {/if}
  </div>
</div>
