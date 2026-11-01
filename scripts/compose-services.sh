#!/usr/bin/env bash
# 输出 compose 定义的服务名（空格分隔，单行）。Makefile 与服务推导脚本的唯一
# 来源；不要再用硬编码列表，否则新增/删除服务会静默漏部署。
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE="${1:-$ROOT/docker-compose.yml}"

awk '
  /^services:[[:space:]]*$/ { in_services = 1; next }
  in_services && /^[^[:space:]]/ { in_services = 0 }
  in_services && /^  [a-z][a-z0-9-]*:[[:space:]]*$/ {
    sub(/^  /, ""); sub(/:.*/, ""); print
  }
' "$COMPOSE" | paste -sd' ' -
