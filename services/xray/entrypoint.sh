#!/bin/sh
ip route del default 2>/dev/null
GW=$(ip route | grep 'default via' | head -1 | awk '{print $3}' 2>/dev/null)
if [ -n "$GW" ]; then
  ip route add default via "$GW" dev eth1 2>/dev/null
else
  ip route add default via 172.20.0.1 dev eth1 2>/dev/null
fi
exec xray -config /etc/xray/config.json "$@"
