<script>
  import { getToasts, dismiss } from './toast.svelte.js';

  let toasts = $derived(getToasts());

  function handleAnimationEnd(e, id) {
    if (e.animationName === 'toastOut') dismiss(id);
  }

  const typeIcons = {
    success:
      '<path stroke-linecap="round" stroke-linejoin="round" d="M9 12.75L11.25 15 15 9.75M21 12a9 9 0 11-18 0 9 9 0 0118 0z"/>',
    error:
      '<path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z"/>',
    warning:
      '<path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z"/>',
    info: '<path stroke-linecap="round" stroke-linejoin="round" d="M11.25 11.25l.041-.02a.75.75 0 011.063.852l-.708 2.836a.75.75 0 001.063.853l.041-.021M21 12a9 9 0 11-18 0 9 9 0 0118 0zm-9-3.75h.008v.008H12V8.25z"/>',
  };
</script>

<!-- live region 常驻挂载：包在 {#if} 里会让容器与首条内容同帧出现，读屏器可能漏报零→一变化 -->
<div class="toast-container" role="status" aria-live="polite">
  {#if toasts.length}
    {#each toasts as t (t.id)}
      <button
        class="toast toast-{t.type}"
        type="button"
        onclick={() => dismiss(t.id)}
        onanimationend={(e) => handleAnimationEnd(e, t.id)}
        style="animation: toastIn 0.25s ease{t.duration > 0
          ? `, toastOut 0.3s ease ${Math.max(0, t.duration - 300)}ms forwards`
          : ''};"
      >
        <svg
          class="toast-icon"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.5"
        >
          {@html typeIcons[t.type] || typeIcons.info}
        </svg>
        <span class="toast-msg">{t.message}</span>
        <span class="toast-close" aria-hidden="true">&times;</span>
      </button>
    {/each}
  {/if}
</div>

<style>
  .toast-container {
    position: fixed;
    top: 1rem;
    right: 1rem;
    z-index: 1000;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    max-width: 420px;
    width: calc(100% - 2rem);
    pointer-events: none;
  }
  .toast {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.7rem 1rem;
    border-radius: var(--radius-lg);
    font-size: 0.8rem;
    font-weight: 500;
    text-align: left;
    pointer-events: auto;
    box-shadow: var(--shadow-lg);
    cursor: pointer;
    line-height: 1.4;
    backdrop-filter: blur(8px);
  }
  .toast-icon {
    width: 1.1rem;
    height: 1.1rem;
    flex-shrink: 0;
  }
  .toast-msg {
    flex: 1;
    min-width: 0;
  }
  .toast-close {
    background: none;
    border: none;
    color: inherit;
    font-size: 1.1rem;
    cursor: pointer;
    padding: 0 0.2rem;
    opacity: 0.5;
    line-height: 1;
    flex-shrink: 0;
  }
  .toast-close:hover {
    opacity: 1;
  }

  .toast-success {
    background: rgba(6, 82, 45, 0.92);
    color: #6ee7b7;
    border: 1px solid rgba(34, 197, 94, 0.3);
  }
  .toast-error {
    background: rgba(127, 29, 29, 0.92);
    color: #fca5a5;
    border: 1px solid rgba(239, 68, 68, 0.3);
  }
  .toast-warning {
    background: rgba(92, 61, 2, 0.92);
    color: #fde68a;
    border: 1px solid rgba(245, 158, 11, 0.3);
  }
  .toast-info {
    background: rgba(21, 41, 77, 0.92);
    color: var(--c-text-info);
    border: 1px solid rgba(59, 130, 246, 0.3);
  }

  @keyframes toastIn {
    from {
      opacity: 0;
      transform: translateX(20px) scale(0.96);
    }
    to {
      opacity: 1;
      transform: translateX(0) scale(1);
    }
  }
  @keyframes toastOut {
    to {
      opacity: 0;
      transform: translateX(20px);
      pointer-events: none;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    /* 覆盖内联 style 的入场/离场动画。注意 animation:none 会同时抑制
       animationend，离场淡出不会触发——自动消失依赖 toast.svelte.js 的
       setTimeout 兜底，这里只是让动效对 reduced-motion 用户立即完成。 */
    .toast {
      animation: none !important;
    }
  }
</style>
