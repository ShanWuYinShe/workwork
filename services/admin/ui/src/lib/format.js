// @ts-check
// 全站统一的时间/大小格式化工具。
// 此前各页面各自实现（Downloads/Transfer/MusicLibrary/MediaCenter 各一份），
// 行为不一致：日期有 UTC slice 也有 toLocaleString，大小单位有 MB 也有 MiB。
// 统一约定：时间一律转本地时区显示；大小一律用 MB/KB 十进制标注（二进制换算）。

/** ISO 时间字符串 → 本地时区 "YYYY-MM-DD HH:mm"；无效输入原样返回。 */
export function formatDateTime(value) {
  if (!value) return '—';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return String(value);
  const pad = (n) => String(n).padStart(2, '0');
  return (
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ` +
    `${pad(d.getHours())}:${pad(d.getMinutes())}`
  );
}

/** ISO 时间字符串 → 本地时区 "YYYY-MM-DD"。 */
export function formatDate(value) {
  return formatDateTime(value).slice(0, 10);
}

/** 字节数 → 人类可读大小（B/KB/MB/GB）。 */
export function formatSize(bytes) {
  const n = Number(bytes);
  if (!Number.isFinite(n) || n < 0) return '—';
  if (n >= 1 << 30) return (n / (1 << 30)).toFixed(1) + ' GB';
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + ' MB';
  if (n >= 1 << 10) return (n / (1 << 10)).toFixed(0) + ' KB';
  return n + ' B';
}
