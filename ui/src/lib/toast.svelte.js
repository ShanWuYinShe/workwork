let _id = 0;
let toasts = $state([]);

export function toast(message, type = 'info', duration = 5000) {
  const id = ++_id;
  toasts = [...toasts, { id, type, message, duration }];
  if (duration > 0) {
    setTimeout(() => dismiss(id), duration);
  }
  return id;
}

export function dismiss(id) {
  toasts = toasts.filter((t) => t.id !== id);
}

export function getToasts() {
  return toasts;
}
