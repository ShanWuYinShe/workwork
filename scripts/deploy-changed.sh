#!/usr/bin/env bash
# 服务器部署入口：同步 main 后，按两次提交间的文件变更推导受影响服务，
# 执行最小化 build + up。路径 → 服务映射的唯一来源是本脚本（AGENTS.md/README 只描述行为，不复述映射表）。
#
# 用法:
#   scripts/deploy-changed.sh          # 完整流程: 校验工作区 → pull → 按变更部署
#   scripts/deploy-changed.sh <old>    # 复跑模式: 跳过 pull，从 <old> 到 HEAD 的变更重新推导
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

# WEBHOOK_URL:优先取调用方环境,否则从 .env 单独提取（docker compose 能读到
# .env 但 shell 不会,部署失败通知需要它;只提取单变量,避免 source 整个 .env
# 踩到非 shell 安全的行）。
if [ -z "${WEBHOOK_URL:-}" ] && [ -f .env ]; then
  WEBHOOK_URL="$(grep -E '^WEBHOOK_URL=' .env 2>/dev/null | head -1 | cut -d= -f2- | tr -d "\"'" || true)"
fi
# 目标为 ops-portal webhook 网关时须持登记证书(默认 ~/.webhook-caller/,
# ALERT_MTLS_CERT/KEY 可覆盖;证书缺失且目标为普通机器人时照常无证推送)。
ALERT_MTLS_CERT="${ALERT_MTLS_CERT:-$HOME/.webhook-caller/cert.pem}"
ALERT_MTLS_KEY="${ALERT_MTLS_KEY:-$HOME/.webhook-caller/key.pem}"

# ADMIN_PORT 同理:部署后版本指纹核对直连容器端口,docker compose 能读到 .env
# 但 shell 不会;不提取则改端口后此处仍硬连旧值导致版本核对误报。
if [ -z "${ADMIN_PORT:-}" ] && [ -f .env ]; then
  ADMIN_PORT="$(grep -E '^ADMIN_PORT=' .env 2>/dev/null | head -1 | cut -d= -f2- | tr -d "\"'" || true)"
fi
ADMIN_PORT="${ADMIN_PORT:-8318}"

# 外发部署失败通知(与 admin 事件通知同源;未配置为 no-op)。构建失败此前
# set -e 直接退出、无任何告警——cron 触发的部署夜间挂掉无人知晓。
notify_deploy_failure() {
  local reason=$1
  if [ -n "${WEBHOOK_URL:-}" ]; then
    local payload
    payload=$(printf '{"event":"deploy.failure","summary":"部署失败:%s","details":{"services":"%s","commit":"%s"},"time":"%s"}' \
      "$reason" "${services:-}" "${new:-$(git rev-parse --short HEAD 2>/dev/null || echo '?')}" "$(date -u +%Y-%m-%dT%H:%M:%SZ)")
    if [ -r "$ALERT_MTLS_CERT" ] && [ -r "$ALERT_MTLS_KEY" ]; then
      curl -fsS --max-time 5 --cert "$ALERT_MTLS_CERT" --key "$ALERT_MTLS_KEY" \
        -H 'Content-Type: application/json' -d "$payload" "$WEBHOOK_URL" >/dev/null 2>&1 ||
        echo "⚠ webhook 通知发送失败（不影响部署流程）" >&2
    else
      curl -fsS --max-time 5 -H 'Content-Type: application/json' -d "$payload" "$WEBHOOK_URL" >/dev/null 2>&1 ||
        echo "⚠ webhook 通知发送失败（不影响部署流程）" >&2
    fi
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
# 分支断言:部署只允许发生在 main 上。在 origin/main 的祖先分支上 pull
# --ff-only 会静默快进该分支指针并照常部署,操作者却以为在别的分支
# (复跑模式同样要求 main:HEAD 即部署对象)。
current_branch="$(git branch --show-current)"
if [ "$current_branch" != "main" ]; then
  echo "错误: 当前分支 $current_branch 非 main,拒绝部署(切到 main 后重试)" >&2
  exit 1
fi
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

if [ "$old" = "$new" ]; then
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
ALL_SERVICES="admin docker-proxy xray nginx"
add_svc() {
  if ! printf '%s\n' $services | grep -qx "$1"; then
    services="$services $1"
  fi
}

# 版本文件变更 → 全部服务重建(基础镜像统一升级的最小安全集)
if printf '%s\n' "$changed" | grep -q '^versions.env$'; then
  for svc in $ALL_SERVICES; do add_svc "$svc"; done
fi
if printf '%s\n' "$changed" | grep -q '^services/admin/'; then
  add_svc admin
fi
if printf '%s\n' "$changed" | grep -q '^services/docker_proxy/'; then
  # admin 的 docker API 白名单代理;改代理必须同时重启 admin(重建 DOCKER_HOST 连接)。
  add_svc docker-proxy
  add_svc admin
fi
if printf '%s\n' "$changed" | grep -q '^services/nginx/'; then
  add_svc nginx
fi
if printf '%s\n' "$changed" | grep -q '^services/xray/'; then
  add_svc xray
fi

# admin 的配置模板渲染到 .wework/generated/ 后由 xray/nginx 只读挂载消费,
# 而这两个容器只在启动时读一次:只重建 admin 就是"渲染了但没人重新加载",
# 新配置静默不生效(典型:改 xray.json.tmpl 的 XHTTP 参数后代理仍跑旧参数)。
# 不能与 admin 同批 up——那是 write-then-read 竞态,xray 可能在 admin 渲染
# 完成前先启动。因此只登记,待下方 admin healthy(渲染完成)后单独 restart。
config_reload=""
if printf '%s\n' "$changed" | grep -q '^services/admin/configs/xray\.json\.tmpl$'; then
  config_reload="$config_reload xray"
fi
if printf '%s\n' "$changed" | grep -q '^services/admin/configs/nginx\.conf\.tmpl$'; then
  config_reload="$config_reload nginx"
fi

if printf '%s\n' "$changed" | grep -q '^docker-compose.yml$\|^.env.example$'; then
  echo "⚠ compose/env 变更: 请人工确认受影响服务后执行 make up <svc...>"
fi

# 版本号烙进镜像(经健康端点暴露),describe 即本次部署版本。
# 先赋值再 export(SC2155):export 与赋值同行时,命令替换的失败码会被
# export 的成功掩盖,退化逻辑依赖不了 $?。
GIT_VERSION="$(git describe --always --tags --dirty 2>/dev/null || echo dev)"
export GIT_VERSION
# 不可变镜像 tag：同一产物可回滚；回滚 = IMAGE_TAG=<旧tag> up -d。
export IMAGE_TAG="$GIT_VERSION"
echo "版本: workwork=$GIT_VERSION (IMAGE_TAG=$IMAGE_TAG)"

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
for svc in $services $config_reload; do
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

# ---------- 重启运行时配置消费者 ----------
# 上面的健康校验已保证 admin 渲染完 .wework/generated/;此时再 restart
# xray/nginx 才能读到新配置(不与 admin 同批 up 的原因见映射段注释)。
if [ -n "${config_reload// /}" ]; then
  echo
  echo "=== 重启运行时配置消费者:$config_reload ==="
  # shellcheck disable=SC2086
  if ! docker compose restart $config_reload; then
    notify_deploy_failure "docker compose restart$config_reload 失败"
    exit 1
  fi
  for svc in $config_reload; do
    ok=""
    for _ in $(seq 1 30); do
      state="$(docker inspect "$(docker compose ps -q "$svc")" --format '{{.State.Health.Status}}' 2>/dev/null || echo none)"
      if [ "$state" = "healthy" ]; then ok=1; break; fi
      sleep 5
    done
    if [ -z "$ok" ]; then
      echo "!! $svc 重启后 150s 内未 healthy"
      fail="$fail $svc"
    else
      # 显式确认，否则"重启了但没生效"在日志里与成功无法区分。
      echo "  $svc 已加载新配置并 healthy"
    fi
  done
fi

# 版本指纹：健康端点自报的构建期烙入版本必须等于本次期望版本。不一致说明
# 容器仍在跑旧镜像（build 失败/up 未生效），同样是校验失败。当前仅 admin
# 暴露带版本的健康端点 {"status":"ok","version":"..."}；nginx(/nginx-health
# 204)、xray(socket+pgrep)、docker-proxy(/_ping) 仅做 healthy 存活校验。
expected_version() {
  case "$1" in
    admin) echo "$GIT_VERSION" ;;
    *) return 0 ;;
  esac
}
health_version() {
  case "$1" in
    admin) url="http://localhost:${ADMIN_PORT}/api/admin/health" ;;
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
    # 配置消费者只做了 restart(镜像未变),没有可回滚的镜像;若按镜像回滚处理,
    # 会误报"旧镜像已被覆盖,无法自动回滚",把人引向错误的人工操作。
    case " $config_reload " in
      *" $svc "*)
        echo "  $svc: 配置消费者仅重启未换镜像，无镜像可回滚（人工: docker compose restart $svc）"
        rolled_back=""
        continue
        ;;
    esac
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
