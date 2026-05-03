#!/bin/sh
ip route del default 2>/dev/null
ip route add default via 172.20.0.1 dev eth1
exec xray -config /etc/xray/config.json "$@"
