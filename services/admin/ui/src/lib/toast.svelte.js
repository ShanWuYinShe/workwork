// @ts-check
let _id = 0;
let toasts = $state([]);

const MAX_TOASTS = 5;

export function toast(message, type = 'info', duration = 5000) {
  const id = ++_id;
  // 堆叠上限：连环报错时不至于叠满整屏，挤掉最旧的。
  toasts = [...toasts.slice(-(MAX_TOASTS - 1)), { id, type, message, duration }];
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
