#!/bin/bash
set -eu

# Auto-detect default network interface, fallback to eth0
DEFAULT_IFACE=$(ip -o -4 route show default | awk '{print $5}' | head -1)
PARENT_IFACE="${PARENT_IFACE:-${DEFAULT_IFACE:-eth0}}"

if [ -z "${IPV6_SUBNET:-}" ] && [ -f .env ]; then
  IPV6_SUBNET=$(grep -E '^IPV6_SUBNET=' .env | tail -1 | cut -d= -f2- | tr -d "\"'" || true)
fi
IPV6_SUBNET="${IPV6_SUBNET:-2001:db8:2623::/64}"
IPV6_PREFIX="${IPV6_SUBNET%/64}"
IPV6_PREFIX="${IPV6_PREFIX%::}"
IPV6_STATIC_RANGE="${IPV6_PREFIX}:1::/80"

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
  --subnet="${IPV6_SUBNET}" --ip-range="${IPV6_STATIC_RANGE}"

echo "Done. Now run: docker compose up -d"
