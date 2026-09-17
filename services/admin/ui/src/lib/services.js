// -check
// 与 docker-compose.yml 的服务清单对齐；缺失的条目会在
// Services/Logs 页面直接显示原始服务 id（如 music_server）。
const SERVICE_LABELS = {
  admin: 'Admin',
  media_server: 'Media Server',
  music_server: 'Music Server',
  netease_api: 'Netease API',
  nginx: 'Nginx',
  xray: 'Xray',
  'xray-forward': 'Xray Forward',
};

export function serviceDisplayName(name) {
  return SERVICE_LABELS[name] || name;
}
