#!/bin/sh
set -e
# IPv4: route through Docker bridge (auto NAT). The bridge subnet can change
# when the network is recreated, so detect it dynamically instead of hardcoding.
# Bridge gateway is always the .1 address in the subnet (e.g. 172.18.0.1).
BRIDGE_IF=$(ip -o -4 addr show | awk '/172\./ && !/127\./{print $2; exit}')
if [ -n "$BRIDGE_IF" ]; then
	SUBNET=$(ip -o -4 addr show dev "$BRIDGE_IF" | awk '{print $4}')
	GW=$(echo "$SUBNET" | awk -F'[./]' '{print $1"."$2"."$3".1"}')
	ip route replace default via "$GW" dev "$BRIDGE_IF" 2>/dev/null
fi

# ── root 阶段：网络 + 文件权限准备 ──────────────────────────
# 源配置只读挂载，复制到 tmpfs 并转交 appuser 读取；
# socket 目录通过 CAP_CHOWN 转交 appuser，不做 chmod（默认 755 已满足需求：
# owner rwx → appuser 可创建 socket，group r-x → nginx 可遍历连接）。
CONFIG_COPY=/tmp/xray-config.json
cp /etc/xray/config.json "$CONFIG_COPY"
chmod 0400 "$CONFIG_COPY"
chown appuser:xraysocket "$CONFIG_COPY"
mkdir -p /run/xray
chown appuser:xraysocket /run/xray

# ── appuser 阶段：清理残留 socket + 启动 xray ────────────────
# rm 由 appuser 执行：appuser 拥有目录（owner rwx）和 socket 文件，
# 可直接清理，无需 FOWNER / DAC_OVERRIDE 等额外 capability。
exec su-exec appuser /bin/sh -c '
	rm -f /run/xray/xrxh.socket
	exec xray -config /tmp/xray-config.json "$@"' xray "$@"
