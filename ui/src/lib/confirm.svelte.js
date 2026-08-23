let resolveCallback = null;

export const confirmState = $state({
  show: false,
  message: '',
  danger: false,
});

/**
 * showConfirm(message, { danger })
 * danger: true 时确认按钮显示为红色（删除 / 重置等破坏性操作），
 * 让用户在按下前从颜色上意识到操作后果。
 */
export function showConfirm(message, { danger = false } = {}) {
  return new Promise((resolve) => {
    confirmState.show = true;
    confirmState.message = message;
    confirmState.danger = danger;
    resolveCallback = resolve;
  });
}

export function confirmResponse(ok) {
  confirmState.show = false;
  confirmState.message = '';
  confirmState.danger = false;
  if (resolveCallback) {
    resolveCallback(ok);
    resolveCallback = null;
  }
}
