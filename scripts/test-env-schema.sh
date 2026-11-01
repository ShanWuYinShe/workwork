#!/usr/bin/env bash
# env-schema.sh 行为测试：正例必须通过，四类漂移必须被拦下。
# 用临时目录隔离修改，不触碰工作区。
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

seed() {
  rm -rf "$tmp/scripts" "$tmp/services" "$tmp/docker-compose.yml" "$tmp/.env.example"
  mkdir -p "$tmp/scripts" "$tmp/services/admin"
  cp "$REPO/scripts/env-schema.tsv" "$tmp/scripts/"
  cp "$REPO/docker-compose.yml" "$tmp/"
  cp "$REPO/.env.example" "$tmp/"
  printf 'package main\nfunc _probe() { _ = GetEnv("ADMIN_PORT") }\n' >"$tmp/services/admin/probe.go"
}

run() {
  ENV_SCHEMA_ROOT="$tmp" bash "$REPO/scripts/env-schema.sh" >"$tmp/out" 2>&1
}

expect_ok() {
  seed
  if ! run; then
    echo "✗ 正例应通过却失败:" >&2
    cat "$tmp/out" >&2
    exit 1
  fi
  echo "  ✓ 正例通过"
}

expect_fail() {
  local name="$1" pattern="$2"
  if run; then
    echo "✗ $name: 应失败却通过" >&2
    exit 1
  fi
  if ! grep -q "$pattern" "$tmp/out"; then
    echo "✗ $name: 失败原因不符合预期（期望含 '$pattern'）:" >&2
    cat "$tmp/out" >&2
    exit 1
  fi
  echo "  ✓ $name 被拦下"
}

expect_ok

seed
sed -i.bak 's/^ADMIN_PORT=8318$/ADMIN_PORT=99999/' "$tmp/.env.example"
expect_fail "非法端口" "不是合法端口"

seed
printf 'UNKNOWN_KEY=x\n' >>"$tmp/.env.example"
expect_fail "未登记变量" "未登记变量: UNKNOWN_KEY"

seed
sed -i.bak 's/ADMIN_PORT:-8318/ADMIN_PORT:-9999/' "$tmp/docker-compose.yml"
expect_fail "compose 默认值漂移" "compose 默认值漂移: ADMIN_PORT=9999"

seed
sed -i.bak '/^#CERT_DIR=\/app\/certs$/d' "$tmp/.env.example"
expect_fail "缺少变量说明" "缺少变量说明: CERT_DIR"

echo "env-schema 行为测试通过 ✓"
