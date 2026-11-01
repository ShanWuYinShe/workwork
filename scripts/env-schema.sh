#!/usr/bin/env bash
# 环境变量门禁：以 scripts/env-schema.tsv 为单一事实源，校验
#   1. 代码读取的环境变量都已登记；
#   2. compose 插值 / .env.example 没有未登记变量；
#   3. 三处默认值一致（schema / compose 的 ${KEY:-...} / .env.example 活动行）；
#   4. 端口、IPv6、token 字符集、枚举等取值格式合法。
#
# 用法: scripts/env-schema.sh check
set -euo pipefail

ROOT="${ENV_SCHEMA_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
SCHEMA="$ROOT/scripts/env-schema.tsv"
COMPOSE="$ROOT/docker-compose.yml"
EXAMPLE="$ROOT/.env.example"
errors=0

fail() {
  echo "✗ $*" >&2
  errors=$((errors + 1))
}

rows() { grep -vE '^[[:space:]]*#|^[[:space:]]*$' "$SCHEMA"; }
# tab 是 IFS 空白符，连续 tab 会被 read 合并，空字段因此左移；改用不可打印
# 的单元分隔符保留空字段。
rows_sep() { rows | awk -F'\t' 'BEGIN { OFS="\x1f" } { print $1, $2, $3, $4, $5 }'; }
compose_sep() {
  grep -oE '\$\{[A-Z0-9_]+(:-[^}]*)?\}' "$COMPOSE" |
    sed -E 's/^\$\{([A-Z0-9_]+)(:-([^}]*))?\}$/\1\t\3/' |
    awk -F'\t' 'BEGIN { OFS="\x1f" } { print $1, $2 }' | sort -u
}
schema_has() { rows | cut -f1 | grep -qx "$1"; }
schema_field() { rows | awk -F'\t' -v k="$1" -v c="$2" '$1==k { print $c; exit }'; }

validate() {
  local key="$1" type="$2" value="$3" where="$4"
  [ -n "$value" ] || return 0
  case "$type" in
    secret|string) ;;
    port)
      if ! [[ "$value" =~ ^[0-9]+$ ]] || [ "$value" -lt 1 ] || [ "$value" -gt 65535 ]; then
        fail "$where: $key=$value 不是合法端口（1-65535）"
      fi
      ;;
    int)
      [[ "$value" =~ ^[0-9]+$ ]] || fail "$where: $key=$value 不是非负整数"
      ;;
    ipv6)
      if ! [[ "$value" =~ ^[0-9A-Fa-f:]+$ ]] || [[ "$value" != *:* ]]; then
        fail "$where: $key=$value 不是合法 IPv6 地址"
      fi
      ;;
    ipv6_cidr)
      [[ "$value" =~ ^[0-9A-Fa-f:]+/[0-9]{1,3}$ ]] ||
        fail "$where: $key=$value 不是合法 IPv6 CIDR"
      ;;
    token)
      [[ "$value" =~ ^[A-Za-z0-9._~-]+$ ]] ||
        fail "$where: $key 含非法字符（只允许 [A-Za-z0-9._~-]，会被渲染进 nginx 配置）"
      ;;
    username)
      [[ "$value" =~ ^[A-Za-z0-9_-]{1,64}$ ]] || fail "$where: $key=$value 不是合法用户名"
      ;;
    host)
      if [[ "$value" == *[[:space:]]* ]] || [[ "$value" == */* ]]; then
        fail "$where: $key=$value 必须是裸主机名/IP（不含 scheme、路径或空白）"
      fi
      ;;
    domain)
      [[ "$value" =~ ^[A-Za-z0-9.-]+$ ]] || fail "$where: $key=$value 不是合法域名"
      ;;
    host_or_ip|hosts|domains)
      [[ "$value" != *[[:space:]]* ]] || fail "$where: $key 不能包含空白"
      ;;
    cidrs)
      local item
      local IFS=,
      for item in $value; do
        [[ "$item" == */* ]] || fail "$where: $key 的每一项都必须是 CIDR: $item"
      done
      ;;
    prefixes)
      local item
      local IFS=,
      for item in $value; do
        [[ "$item" == /* ]] || fail "$where: $key 的每一项都必须以 / 开头: $item"
      done
      ;;
    enum:h2,h3)
      case "$value" in h2|h3) ;; *) fail "$where: $key=$value 只允许 h2 或 h3" ;; esac
      ;;
    url)
      [[ "$value" =~ ^https?://[^[:space:]]+$ ]] || fail "$where: $key=$value 不是 http(s) URL"
      ;;
    path)
      [[ "$value" != *[[:space:]]* ]] || fail "$where: $key 路径不能包含空白"
      ;;
    project)
      [[ "$value" =~ ^[A-Za-z0-9_-]+$ ]] || fail "$where: $key=$value 不是合法 compose 项目名"
      ;;
    version)
      [[ "$value" =~ ^[0-9]+(\.[0-9]+)*$ ]] || fail "$where: $key=$value 不是合法版本号"
      ;;
    *)
      fail "$where: $key 使用了未知校验类型 $type"
      ;;
  esac
}

dups="$(rows | cut -f1 | sort | uniq -d)"
[ -z "$dups" ] || fail "env-schema.tsv 存在重复键: $(echo "$dups" | tr '\n' ' ')"

# 未登记 = 拼错键名或新增变量漏登记；两者都会让配置静默失效。
while IFS= read -r key; do
  [ -n "$key" ] || continue
  schema_has "$key" || fail "代码读取的环境变量未登记: $key"
done < <(grep -rhoE 'GetEnv\("[A-Z0-9_]+"\)' "$ROOT/services/admin" --include='*.go' |
  sed -E 's/.*GetEnv\("([A-Z0-9_]+)"\).*/\1/' | sort -u)

while IFS=$'\x1f' read -r key default; do
  [ -n "$key" ] || continue
  schema_has "$key" || fail "docker-compose.yml 插值未登记: $key"
  [ -n "$default" ] || continue
  case "$default" in *'${'*) continue ;; esac
  validation="$(schema_field "$key" 4)"
  if [ "$validation" = "secret" ]; then continue; fi
  want="$(schema_field "$key" 2)"
  # `-` 表示默认值由 versions.env 主本管理（sync-versions.sh 校验），
  # env-schema 只负责登记与格式校验，避免成为第三个版本事实源。
  [ "$want" = "-" ] && want=""
  if [ -n "$want" ] && [ "$default" != "$want" ]; then
    fail "compose 默认值漂移: $key=${default}（schema=${want}）"
  fi
  validate "$key" "$validation" "$default" "compose 默认值"
done < <(compose_sep)

# 仅由容器/部署脚本注入、不面向 .env 的变量，不要求出现在 .env.example。
internal_only() {
  case "$1" in
    IMAGE_TAG | GIT_VERSION | DOCKER_SOCKET | LISTEN | ALLOWED_COMPOSE_PROJECTS) return 0 ;;
    *) return 1 ;;
  esac
}

example_keys() {
  grep -E '^[[:space:]]*#?[[:space:]]?[A-Z0-9_]+=' "$EXAMPLE" |
    sed -E 's/^[[:space:]]*#?[[:space:]]?([A-Z0-9_]+)=.*/\1/'
}
while IFS= read -r key; do
  [ -n "$key" ] || continue
  schema_has "$key" || fail ".env.example 存在未登记变量: $key"
done < <(example_keys | sort -u)

while IFS= read -r key; do
  [ -n "$key" ] || continue
  if internal_only "$key"; then continue; fi
  example_keys | grep -qx "$key" || fail ".env.example 缺少变量说明: $key"
done < <(rows | cut -f1)

# .env.example 活动行：默认值必须与 schema 一致，取值必须合法。
while IFS=$'\x1f' read -r key default required validation _consumers; do
  [ -n "$key" ] || continue
  value="$(grep -E "^${key}=" "$EXAMPLE" | head -1 | cut -d= -f2- || true)"
  if [ -n "$value" ]; then
    value="${value%\"}"
    value="${value#\"}"
    value="${value%\'}"
    value="${value#\'}"
    validate "$key" "$validation" "$value" ".env.example"
    if [ -n "$default" ] && [ "$default" != "-" ] && [ "$validation" != "secret" ] && [ "$value" != "$default" ]; then
      fail ".env.example 默认值漂移: $key=$value（schema=$default）"
    fi
  elif [ "$required" = "yes" ] || [ "$required" = "first-boot" ]; then
    fail ".env.example: 缺少必需变量 $key"
  fi
done < <(rows_sep)

if [ "$errors" -gt 0 ]; then
  echo "env-schema: 发现 $errors 处问题。" >&2
  exit 1
fi
echo "env-schema: 全部一致 ✓"
