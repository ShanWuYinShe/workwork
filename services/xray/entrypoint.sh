#!/bin/sh
set -e
# IPv4: route through Docker bridge (auto NAT). The bridge subnet can change
# when the network is recreated, so detect it dynamically instead of hardcoding.
#
# 接口选择:ipvlan 接口持有公网 IPv6(global scope),egress bridge 只有
# IPv4(link-local IPv6 或无)。遍历接口跳过有 global IPv6 的接口(ipvlan),
# 选有 IPv4 地址的接口——不能假设 eth0:Docker 多网络连接顺序随机,接口
# 编号(eth0/eth1)不固定。也支持 XRAY_EGRESS_IFACE / XRAY_EGRESS_GATEWAY
# 显式覆盖。
BRIDGE_IF="${XRAY_EGRESS_IFACE:-}"
if [ -z "$BRIDGE_IF" ]; then
	for iface_path in /sys/class/net/*; do
		iface="$(basename "$iface_path")"
		[ "$iface" = "lo" ] && continue
		ip -6 addr show dev "$iface" scope global 2>/dev/null | grep -q 'inet6' && continue
		if ip -4 addr show dev "$iface" 2>/dev/null | grep -q 'inet '; then
			BRIDGE_IF="$iface"
			break
		fi
	done
fi
if [ -n "$BRIDGE_IF" ]; then
	SUBNET=$(ip -o -4 addr show dev "$BRIDGE_IF" | awk '{print $4}')
	if [ -z "$SUBNET" ]; then
		echo "ERROR: no IPv4 address on egress interface $BRIDGE_IF" >&2
		exit 1
	fi
	if [ -n "$XRAY_EGRESS_GATEWAY" ]; then
		GW="$XRAY_EGRESS_GATEWAY"
	else
		# Bridge gateway is always the .1 address in the subnet (e.g. 172.18.0.1).
		GW=$(echo "$SUBNET" | awk -F'[./]' '{print $1"."$2"."$3".1"}')
	fi
	ip route replace default via "$GW" dev "$BRIDGE_IF"
	# 校验默认路由真正生效;失败即退出,避免"healthcheck 通过但代理不可达"
	# 的静默故障(ip route get 只查路由表,不实际发包)。
	if ! ip route get 1.1.1.1 >/dev/null 2>&1; then
		echo "ERROR: default route via $GW dev $BRIDGE_IF not working" >&2
		exit 1
	fi
	echo "xray egress: default via $GW dev $BRIDGE_IF"
else
	echo "WARNING: no IPv4 bridge interface found, default route not set" >&2
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
