// @ts-check
// 密码可见性切换图标（Users 页改密表单等处共用）。
// 返回 SVG 字符串，配合 {@html} 使用；open=true 时叠加斜线表示当前明文。
export function eyeIcon(open) {
  return `<svg class="eye-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/>${open ? '<line x1="1" y1="1" x2="23" y2="23"/>' : ''}</svg>`;
}
