#!/bin/sh
# IPv4: route through Docker bridge (auto NAT), IPv6: via ipvlan (auto L3)
BRIDGE_IF=$(ip -o -4 addr show | awk '/172\.20\./{print $2; exit}')
[ -n "$BRIDGE_IF" ] && ip route replace default via 172.20.0.1 dev "$BRIDGE_IF" 2>/dev/null
exec xray -config /etc/xray/config.json "$@"
