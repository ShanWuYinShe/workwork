#!/usr/bin/env bash
# 服务器部署入口：同步 main 与子模块后，按两次提交间的文件变更推导受影响服务，
# 执行最小化 build + up。路径 → 服务映射的唯一来源是本脚本（AGENTS.md/README 只描述行为，不复述映射表）。
#
# 用法:
#   scripts/deploy-changed.sh          # 完整流程: 校验工作区 → pull → 子模块同步 → 按变更部署
#   scripts/deploy-changed.sh <old>    # 复跑模式: 跳过 pull，从 <old> 到 HEAD 的变更重新推导
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

# WEBHOOK_URL:优先取调用方环境,否则从 .env 单独提取（docker compose 能读到
# .env 但 shell 不会,部署失败通知需要它;只提取单变量,避免 source 整个 .env
# 踩到非 shell 安全的行）。
if [ -z "${WEBHOOK_URL:-}" ] && [ -f .env ]; then
  WEBHOOK_URL="$(grep -E '^WEBHOOK_URL=' .env 2>/dev/null | head -1 | cut -d= -f2- | tr -d "\"'" || true)"
fi

# 外发部署失败通知(与 admin 事件通知同源;未配置为 no-op)。构建失败此前
# set -e 直接退出、无任何告警——cron 触发的部署夜间挂掉无人知晓。
notify_deploy_failure() {
  local reason=$1
  if [ -n "${WEBHOOK_URL:-}" ]; then
    local payload
    payload=$(printf '{"event":"deploy.failure","summary":"部署失败:%s","details":{"services":"%s","commit":"%s"},"time":"%s"}' \
      "$reason" "${services:-}" "${new:-$(git rev-parse --short HEAD 2>/dev/null || echo '?')}" "$(date -u +%Y-%m-%dT%H:%M:%SZ)")
    curl -fsS --max-time 5 -H 'Content-Type: application/json' -d "$payload" "$WEBHOOK_URL" >/dev/null 2>&1 ||
      echo "⚠ webhook 通知发送失败（不影响部署流程）" >&2
  fi
}

# ---------- 并发部署防护 ----------
# cron 与人工同时触发会交错执行 build/up。锁经 fd 9 在下方 re-exec 时保持
# 持有;macOS 无 flock CLI(仅服务器 Linux 有),缺席时降级为只告警不拦截。
lockfile="${TMPDIR:-/tmp}/workwork-deploy.lock"
exec 9>"$lockfile"
if command -v flock >/dev/null 2>&1; then
  if ! flock -n 9; then
    echo "错误: 另一个 deploy-changed 正在进行(锁: $lockfile),拒绝并发部署" >&2
    exit 1
  fi
else
  echo "⚠ flock 不可用，跳过并发部署防护（仅本地复跑会出现）"
fi

# ---------- 输入与前置校验 ----------
old=${1:-}
if [ -n "$old" ]; then
  echo "复跑模式: 从 $old 推导变更（跳过 pull）"
else
  if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
    echo "错误: 工作区有未提交改动，拒绝部署（先 commit 或确认清理）" >&2
    exit 1
  fi
  old=$(git rev-parse HEAD)
  git pull --ff-only origin main
  # pull 可能替换本脚本文件:若继续执行,bash 会按旧字节偏移读新内容,
  # 执行错乱(实测 admin 漏建、版本导出失效)。以磁盘上的新版本重新执行
  # 自己——转入复跑模式($1 已设,跳过 pull,无递归风险)。
  exec bash "$0" "$old"
fi
new=$(git rev-parse HEAD)

# 子模块必须先于任何 build/up: compose 把子模块内文件挂载/用作 build context，
# 缺失的挂载源会被 Docker 自动建成目录（首次部署死锁的经典诱因）。
git submodule update --init --recursive

if [ "$old" = "$new" ] && [ -z "$(git submodule status | grep '^+' || true)" ]; then
  echo "已是最新，无需部署"
  exit 0
fi

changed=$(git diff --name-only "$old" "$new")
echo
echo "=== 变更 ($old..$new) ==="
printf '%s\n' "$changed" | sed 's/^/  /'
echo

# ---------- 路径 → 服务映射 ----------
services=""
add_svc() {
  if ! printf '%s\n' $services | grep -qx "$1"; then
    services="$services $1"
  fi
}

# 子模块指针变化 → 该产品仓的容器镜像全部重建
if printf '%s\n' "$changed" | grep -qx 'services/media_center'; then
  add_svc media_server
  add_svc vod_extractor
fi
if printf '%s\n' "$changed" | grep -qx 'services/music_player'; then
  add_svc music_server
fi

if printf '%s\n' "$changed" | grep -q '^services/netease_api/'; then
  add_svc netease_api
fi
if printf '%s\n' "$changed" | grep -q '^services/admin/'; then
  add_svc admin
fi
if printf '%s\n' "$changed" | grep -q '^services/nginx/'; then
  add_svc nginx
fi
if printf '%s\n' "$changed" | grep -q '^services/xray/'; then
  add_svc xray
  add_svc xray-forward
fi

# ---------- 提示性事项（不进入自动部署范围） ----------
if printf '%s\n' "$changed" | grep -q '^services/media_center/media_app'; then
  echo "⚠ media_app 源码变更: APK 需手动构建上传（见 AGENTS.md media_app 条目）"
fi
if printf '%s\n' "$changed" | grep -q '^docker-compose.yml$\|^.env.example$'; then
  echo "⚠ compose/env 变更: 请人工确认受影响服务后执行 make up <svc...>"
fi

# 版本号烙进镜像(经健康端点暴露);子模块刚同步,describe 即本次部署版本。
# 先赋值再 export(SC2155):export 与赋值同行时,命令替换的失败码会被
# export 的成功掩盖,退化逻辑依赖不了 $?。
GIT_VERSION="$(git describe --always --tags --dirty 2>/dev/null || echo dev)"
MEDIA_CENTER_VERSION="$(git -C services/media_center describe --tags --always --dirty 2>/dev/null || echo dev)"
MUSIC_PLAYER_VERSION="$(git -C services/music_player describe --tags --always --dirty 2>/dev/null || echo dev)"
export GIT_VERSION MEDIA_CENTER_VERSION MUSIC_PLAYER_VERSION
# 不可变镜像 tag：同一产物可回滚；回滚 = IMAGE_TAG=<旧tag> up -d。
export IMAGE_TAG="$GIT_VERSION"
echo "版本: workwork=$GIT_VERSION media_center=$MEDIA_CENTER_VERSION music_player=$MUSIC_PLAYER_VERSION (IMAGE_TAG=$IMAGE_TAG)"

# ---------- 执行最小部署 ----------
if [ -z "${services// /}" ]; then
  echo "无受影响容器服务"
  exit 0
fi

echo
echo "=== 受影响服务:$services ==="
# 记录部署前各服务镜像(含 tag)，部署后打印精确回滚命令。
# 用临时文件而不用关联数组：保持 bash 3 兼容（macOS 本地复跑）。
prev_file="$(mktemp)"
trap 'rm -f "$prev_file"' EXIT
for svc in $services; do
  cid="$(docker compose ps -q "$svc" 2>/dev/null || true)"
  if [ -n "$cid" ]; then
    echo "$svc $(docker inspect "$cid" --format '{{.Config.Image}}' 2>/dev/null || true)" >>"$prev_file"
  fi
done
# shellcheck disable=SC2086
if ! docker compose build $services; then
  notify_deploy_failure "docker compose build 失败"
  exit 1
fi
# shellcheck disable=SC2086
if ! docker compose up -d --no-deps $services; then
  notify_deploy_failure "docker compose up 失败"
  exit 1
fi
echo
docker compose ps

# ---------- 部署后自校验：等 healthy + 核对版本指纹（不通过即自动回滚） ----------
echo
echo "=== 健康校验 ==="
fail=""
for svc in $services; do
  ok=""
  for _ in $(seq 1 30); do
    state="$(docker inspect "$(docker compose ps -q "$svc")" --format '{{.State.Health.Status}}' 2>/dev/null || echo none)"
    if [ "$state" = "healthy" ]; then ok=1; break; fi
    sleep 5
  done
  if [ -z "$ok" ]; then echo "!! $svc 150s 内未 healthy"; fail="$fail $svc"; fi
done

# 版本指纹：健康端点自报的构建期烙入版本必须等于本次期望版本。不一致说明
# 容器仍在跑旧镜像（build 失败/up 未生效），同样是校验失败。三个服务健康
# JSON 同形：{"status":"ok","version":"..."}。
expected_version() {
  case "$1" in
    admin) echo "$GIT_VERSION" ;;
    media_server) echo "$MEDIA_CENTER_VERSION" ;;
    music_server) echo "$MUSIC_PLAYER_VERSION" ;;
    *) return 0 ;;
  esac
}
health_version() {
  case "$1" in
    admin) url="http://localhost:8318/api/admin/health" ;;
    media_server) url="http://localhost:8787/health" ;;
    music_server) url="http://localhost:8790/health" ;;
    *) return 0 ;;
  esac
  docker compose exec -T "$1" wget -qO- "$url" 2>/dev/null |
    grep -o '"version":"[^"]*"' | head -1 | cut -d'"' -f4 || true
}
echo
echo "=== 版本指纹核对 ==="
for svc in $services; do
  exp="$(expected_version "$svc")"
  [ -z "$exp" ] && continue
  got="$(health_version "$svc")"
  echo "  $svc: 期望 $exp，实际 ${got:-<无响应>}"
  if [ "$got" != "$exp" ]; then
    echo "!! $svc 版本指纹不匹配"
    fail="$fail $svc"
  fi
done

if [ -n "$fail" ]; then
  echo "!! 部署校验未通过:$fail"
  echo "=== 自动回滚（到部署前镜像） ==="
  rolled_back=1
  for svc in $fail; do
    prev_image="$(awk -v s="$svc" '$1==s{print $2}' "$prev_file")"
    if [ -z "$prev_image" ]; then
      echo "  $svc: 部署前未运行，无回滚目标（人工处理: docker compose up -d --no-deps $svc 或 down）"
      rolled_back=""
      continue
    fi
    prev_tag="${prev_image##*:}"
    if [ "$prev_tag" = "$IMAGE_TAG" ]; then
      # 复跑同一 commit 时新旧 tag 相同，重建已覆盖回滚目标
      echo "  $svc: 前后镜像 tag 相同($IMAGE_TAG)，旧镜像已被覆盖，无法自动回滚"
      rolled_back=""
      continue
    fi
    echo "  $svc: 回滚 → $prev_image"
    if IMAGE_TAG="$prev_tag" docker compose up -d --no-deps "$svc"; then
      echo "  $svc 已回滚"
    else
      echo "  !! $svc 回滚失败，需人工介入: IMAGE_TAG=$prev_tag docker compose up -d --no-deps $svc"
      rolled_back=""
    fi
  done
  if [ -n "$rolled_back" ]; then
    echo "全部失败服务已回滚到部署前版本"
  else
    echo "存在未能自动回滚的服务，见上方日志（旧 tag 镜像须仍在本地:勿对它们 docker image prune）"
  fi
  # 外发失败通知（WEBHOOK_URL,与 admin 事件通知同源;未配置为 no-op）。
  notify_deploy_failure "部署校验未通过:$fail"
  exit 1
fi
echo "部署校验通过 ✅（回滚：IMAGE_TAG=<旧tag> docker compose up -d --no-deps$services）"
