// @ts-check
// 与 docker-compose.yml 的服务清单对齐；缺失的条目会在
// Services/Logs 页面直接显示原始服务 id（如 music_server）。
const SERVICE_LABELS = {
  admin: 'Admin',
  media_server: 'Media Server',
  music_server: 'Music Server',
  netease_api: 'Netease API',
  nginx: 'Nginx',
  xray: 'Xray',
};

export function serviceDisplayName(name) {
  return SERVICE_LABELS[name] || name;
}

/**
 * 服务状态徽章:class 与 i18n 文案 key。
 * running 但 health 为 unhealthy 的容器显示黄色"健康检查失败"——
 * healthcheck 连续失败的服务此前在面板上一直是绿色,出事前无感知。
 * @param {{ state: string, health?: string }} svc
 * @param {(key: string) => string} t i18n 翻译函数
 * @returns {{ cls: string, label: string }}
 */
export function serviceStateBadge(svc, t) {
  if (svc.state === 'running') {
    if (svc.health === 'unhealthy') {
      return { cls: 'badge-yellow', label: t('stateUnhealthy') };
    }
    if (svc.health === 'starting') {
      return { cls: 'badge-yellow', label: t('stateHealthStarting') };
    }
    return { cls: 'badge-green', label: t('stateRunning') };
  }
  return { cls: 'badge-red', label: t('stateStopped') };
}
