<script>
  import { confirmState, confirmResponse } from './confirm.svelte.js';
  import { t } from './i18n.svelte.js';

  let dialog = $state(null);

  function onKeydown(e) {
    if (e.key === 'Escape') confirmResponse(false);
    if (e.key === 'Tab' && dialog) {
      const focusable = dialog.querySelectorAll('button:not([disabled])');
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    }
  }

  $effect(() => {
    if (confirmState.show) {
      requestAnimationFrame(() => dialog?.querySelector('button')?.focus());
    }
  });
</script>

<svelte:window onkeydown={onKeydown} />

{#if confirmState.show}
  <div class="modal-overlay">
    <button class="modal-backdrop" type="button" aria-label={t('cancel')} onclick={() => confirmResponse(false)}></button>
    <div class="modal" bind:this={dialog} role="dialog" aria-modal="true" tabindex="-1">
      <p class="modal-message">{confirmState.message}</p>
      <div class="modal-actions">
        <button class="btn btn-secondary" onclick={() => confirmResponse(false)}>{t('cancel')}</button>
        <button class="btn btn-primary" onclick={() => confirmResponse(true)}>{t('ok')}</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .modal-overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.6);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
    backdrop-filter: blur(2px);
  }
  .modal-backdrop {
    position: absolute;
    inset: 0;
    border: 0;
    padding: 0;
    background: transparent;
    cursor: default;
  }
  .modal {
    position: relative;
    z-index: 1;
    background: var(--c-bg-elevated);
    border: 1px solid var(--c-border-strong);
    border-radius: var(--radius, 8px);
    padding: 1.5rem;
    max-width: 420px;
    width: 90%;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.5);
  }
  .modal-message {
    margin: 0 0 1.25rem;
    font-size: 0.9rem;
    color: var(--c-text);
    line-height: 1.5;
  }
  .modal-actions {
    display: flex;
    gap: 0.5rem;
    justify-content: flex-end;
  }
</style>
