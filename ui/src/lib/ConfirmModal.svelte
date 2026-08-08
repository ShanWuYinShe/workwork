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
    <button
      class="modal-backdrop"
      type="button"
      aria-label={t('cancel')}
      onclick={() => confirmResponse(false)}
    ></button>
    <div class="modal" bind:this={dialog} role="dialog" aria-modal="true" tabindex="-1">
      <p class="modal-message">{confirmState.message}</p>
      <div class="modal-actions">
        <button class="btn btn-secondary" onclick={() => confirmResponse(false)}
          >{t('cancel')}</button
        >
        <button class="btn btn-primary" onclick={() => confirmResponse(true)}>{t('ok')}</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .modal-overlay {
    position: fixed;
    inset: 0;
    background: rgba(2, 6, 15, 0.65);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
    backdrop-filter: blur(4px);
    animation: overlayIn 0.15s ease;
  }
  @keyframes overlayIn {
    from {
      opacity: 0;
    }
    to {
      opacity: 1;
    }
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
    background: linear-gradient(180deg, var(--c-bg-elevated), var(--c-bg));
    border: 1px solid var(--c-border-strong);
    border-radius: var(--radius-lg, 12px);
    padding: 1.5rem;
    max-width: 420px;
    width: 90%;
    box-shadow: var(--shadow-lg);
    animation: modalIn 0.2s cubic-bezier(0.25, 0.46, 0.45, 0.94);
  }
  @keyframes modalIn {
    from {
      opacity: 0;
      transform: translateY(8px) scale(0.98);
    }
    to {
      opacity: 1;
      transform: translateY(0) scale(1);
    }
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
