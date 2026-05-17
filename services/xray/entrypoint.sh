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
exec xray -config /etc/xray/config.json "$@"
