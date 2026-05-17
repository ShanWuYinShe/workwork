#!/bin/sh
set -e
# IPv4: route through Docker bridge (auto NAT). The bridge subnet can change
# when the network is recreated, so detect it dynamically instead of hardcoding.
BRIDGE_IF=$(ip -o -4 addr show | awk '/172\./ && !/127\./{print $2; exit}')
if [ -n "$BRIDGE_IF" ]; then
	GW=$(ip -o -4 route show dev "$BRIDGE_IF" | awk '/src/{print $NF; exit}')
	[ -n "$GW" ] && ip route replace default via "$GW" dev "$BRIDGE_IF" 2>/dev/null
fi
exec xray -config /etc/xray/config.json "$@"
