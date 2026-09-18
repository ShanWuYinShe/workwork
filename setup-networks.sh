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

# 文档前缀守卫:2001:db8::/32 是 RFC 3849 保留的示例前缀,用它建网容器全部
# 正常启动、healthcheck 全绿,但公网 IPv6 完全不通——此前静默回落极易误用。
# 确实只想做本地演练时设 FORCE=1 跳过。
case "${IPV6_PREFIX}" in
  2001:db8*)
    if [ "${FORCE:-0}" != "1" ]; then
      echo "ERROR: IPV6_SUBNET '${IPV6_SUBNET}' uses the RFC 3849 documentation prefix (2001:db8::/32)." >&2
      echo "Containers would come up healthy but public IPv6 would be dead. Fill a real" >&2
      echo "prefix in .env (IPV6_SUBNET=...) or set FORCE=1 to override." >&2
      exit 1
    fi
    echo "WARNING: FORCE=1, proceeding with documentation prefix ${IPV6_SUBNET}" >&2
    ;;
esac

create_network() {
  local name=$1; shift
  if docker network ls --format '{{.Name}}' | grep -q "^${name}$"; then
    # 已存在也校验 subnet 一致:此前只按名字跳过,subnet/parent 被人改过的
    # 网络会与 compose 期待值静默漂移。
    local existing_subnet
    existing_subnet=$(docker network inspect "${name}" --format '{{range .IPAM.Config}}{{.Subnet}}{{end}}')
    if [ -n "${existing_subnet}" ] && [ "${existing_subnet}" != "${IPV6_SUBNET}" ]; then
      echo "ERROR: network ${name} exists with subnet ${existing_subnet}, expected ${IPV6_SUBNET}." >&2
      echo "Remove the stale network first: docker network rm ${name}" >&2
      exit 1
    fi
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
