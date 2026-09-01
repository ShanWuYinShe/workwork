#!/bin/sh
# 定时刷新 Cloudflare IP 段(cron 调用):内容变化才 reload,失败保留旧配置。
# CF 新增网段后,若 set_real_ip_from 未覆盖,来自新网段的连接 $remote_addr 保持
# 为 CF 边缘 IP → fail2ban 会封禁边缘 IP,大面积误伤真实用户。
set -eu

CONF=/etc/nginx/cloudflare-ips.conf
BEFORE=$(mktemp /tmp/cf-ips-before.XXXXXX)
trap 'rm -f "$BEFORE"' EXIT

[ -f "$CONF" ] && cp "$CONF" "$BEFORE"

# fetch 失败时其自身已保证保留旧文件(仅 exit 1),无需在此重试。
/usr/local/bin/fetch-cloudflare-ips.sh >/dev/null 2>&1 || exit 0

if [ ! -f "$BEFORE" ] || ! cmp -s "$BEFORE" "$CONF"; then
  echo "[cf-ips] ranges changed, reloading nginx"
  nginx -s reload
fi
