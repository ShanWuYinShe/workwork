<script>
  let { type = 'error', visible, onclose } = $props();

  let dismissing = $state(false);

  function dismiss() {
    if (onclose) onclose();
  }

  function handleAnimationEnd(e) {
    if (e.animationName === 'toastOut' && onclose) onclose();
  }
</script>

{#if visible}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div
    class="toast toast-{type}"
    role="alert"
    onclick={dismiss}
    onkeydown={(e) => e.key === 'Enter' && dismiss()}
    tabindex="0"
    onanimationend={handleAnimationEnd}
  >
    <slot />
    <button class="toast-close" onclick={dismiss} aria-label="Close">&times;</button>
  </div>
{/if}

<style>
  :global(.toast) {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.6rem 1rem;
    border-radius: var(--radius);
    font-size: 0.8rem;
    font-weight: 500;
    animation:
      toastIn 0.25s ease,
      toastOut 0.3s ease 5s forwards;
    pointer-events: auto;
    max-width: 400px;
    box-shadow: 0 4px 24px rgba(0, 0, 0, 0.4);
    cursor: pointer;
  }
  :global(.toast-error) {
    background: #7f1d1d;
    color: #fca5a5;
    border: 1px solid rgba(239, 68, 68, 0.2);
  }
  :global(.toast-success) {
    background: #064e3b;
    color: #6ee7b7;
    border: 1px solid rgba(34, 197, 94, 0.2);
  }
  .toast-close {
    background: none;
    border: none;
    color: inherit;
    font-size: 1.1rem;
    cursor: pointer;
    padding: 0 0.2rem;
    opacity: 0.6;
    line-height: 1;
    flex-shrink: 0;
  }
  .toast-close:hover {
    opacity: 1;
  }
  @keyframes toastIn {
    from {
      opacity: 0;
      transform: translateY(-8px) scale(0.96);
    }
    to {
      opacity: 1;
      transform: translateY(0) scale(1);
    }
  }
  @keyframes toastOut {
    to {
      opacity: 0;
      transform: translateY(-8px);
      pointer-events: none;
    }
  }
</style>
