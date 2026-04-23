#!/bin/bash
set -e

PARENT_IFACE="${PARENT_IFACE:-eth0}"

create_network() {
  local name=$1 subnet=$2 ip_range=$3
  if docker network ls --format '{{.Name}}' | grep -q "^${name}$"; then
    echo "Network ${name} already exists, skipping."
  else
    docker network create -d ipvlan \
      --ipv6 \
      --subnet="${subnet}" \
      --ip-range="${ip_range}" \
      -o parent="${PARENT_IFACE}" \
      -o ipvlan_mode=l3 \
      "${name}"
    echo "Created network ${name}."
  fi
}

echo "Creating ipvlan networks (parent=${PARENT_IFACE})..."

# 单个 ipvlan 网络，所有外部容器共用
create_network ipvlan_docker \
  "2605:a141:2322:2623::/64" \
  "2605:a141:2322:2623:1::/80"

echo "Done. Now run: docker compose up -d"