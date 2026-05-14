let resolveCallback = null;

export const confirmState = $state({
  show: false,
  message: '',
});

export function showConfirm(message) {
  return new Promise((resolve) => {
    confirmState.show = true;
    confirmState.message = message;
    resolveCallback = resolve;
  });
}

export function confirmResponse(ok) {
  confirmState.show = false;
  confirmState.message = '';
  if (resolveCallback) {
    resolveCallback(ok);
    resolveCallback = null;
  }
}
