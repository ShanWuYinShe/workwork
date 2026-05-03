#!/bin/bash
set -e

PARENT_IFACE="${PARENT_IFACE:-eth0}"

create_network() {
  local name=$1; shift
  if docker network ls --format '{{.Name}}' | grep -q "^${name}$"; then
    echo "Network ${name} already exists, skipping."
  else
    docker network create -d ipvlan \
      --ipv6 \
      "$@" \
      -o parent="${PARENT_IFACE}" \
      -o ipvlan_mode=l3 \
      "${name}"
    echo "Created network ${name}."
  fi
}

echo "Creating ipvlan networks (parent=${PARENT_IFACE})..."

# Single ipvlan network shared by all external containers (IPv6 only)
# IPv6 subnet for Xray proxy
create_network ipvlan_docker \
  --subnet="2605:a141:2322:2623::/64" --ip-range="2605:a141:2322:2623:1::/80"

echo "Done. Now run: docker compose up -d"
