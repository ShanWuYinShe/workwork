#!/usr/bin/env bash
# 服务器部署入口：同步 main 与子模块后，按两次提交间的文件变更推导受影响服务，
# 执行最小化 build + up。路径映射与 AGENTS.md「构建 & 部署」保持一致，两处须同步修改。
#
# 用法:
#   scripts/deploy-changed.sh          # 完整流程: 校验工作区 → pull → 子模块同步 → 按变更部署
#   scripts/deploy-changed.sh <old>    # 复跑模式: 跳过 pull，从 <old> 到 HEAD 的变更重新推导
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

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

if printf '%s\n' "$changed" | grep -q '^services/media_center/media_server/'; then
  add_svc media_server
fi
if printf '%s\n' "$changed" | grep -q '^services/media_center/vod_extractor/'; then
  add_svc vod_extractor
fi
if printf '%s\n' "$changed" | grep -q '^services/music_player/music_server/'; then
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
export GIT_VERSION="$(git describe --always --tags --dirty 2>/dev/null || echo dev)"
export MEDIA_CENTER_VERSION="$(git -C services/media_center describe --tags --always --dirty 2>/dev/null || echo dev)"
export MUSIC_PLAYER_VERSION="$(git -C services/music_player describe --tags --always --dirty 2>/dev/null || echo dev)"
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
docker compose build $services
# shellcheck disable=SC2086
docker compose up -d --no-deps $services
echo
docker compose ps

# ---------- 部署后自校验：等 healthy + 核对版本指纹 ----------
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
# 版本指纹：各服务健康端点自报构建期烙入的版本（与 compose healthcheck 同端点）。
docker compose exec -T admin wget -qO- http://localhost:8318/api/admin/health 2>/dev/null | head -c 300 || true; echo
if printf '%s\n' $services | grep -qx media_server; then
  docker compose exec -T media_server wget -qO- http://localhost:8787/health 2>/dev/null | head -c 300 || true; echo
fi
if printf '%s\n' $services | grep -qx music_server; then
  docker compose exec -T music_server wget -qO- http://localhost:8790/health 2>/dev/null | head -c 300 || true; echo
fi
if [ -n "$fail" ]; then
  echo "!! 部署校验未通过:$fail"
  echo "回滚（部署前镜像）："
  cat "$prev_file" | sed 's/^/  /'
  echo "执行：逐个 IMAGE_TAG=<旧tag> docker compose up -d --no-deps <svc>"
  echo "（旧 tag 镜像须仍在本地：勿对它们 docker image prune）"
  exit 1
fi
echo "部署校验通过 ✅（回滚：IMAGE_TAG=<旧tag> docker compose up -d --no-deps$services）"
