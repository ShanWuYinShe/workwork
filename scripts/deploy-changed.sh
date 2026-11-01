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
  WEBHOOK_URL="$(grep -E '^WEBHOOK_URL=' .env 2>/dev/null | tail -1 | cut -d= -f2- | tr -d "\"'" || true)"
fi
# tail -1: 与 Docker Compose 的 dotenv 语义一致（后行覆盖），否则 .env 里重复
# 定义时脚本读第一行、compose 读最后一行，版本核对会连错端口并误回滚。
# 目标为 ops-portal webhook 网关时须持登记证书(默认 ~/.webhook-caller/,
# ALERT_MTLS_CERT/KEY 可覆盖;证书缺失且目标为普通机器人时照常无证推送)。
ALERT_MTLS_CERT="${ALERT_MTLS_CERT:-$HOME/.webhook-caller/cert.pem}"
ALERT_MTLS_KEY="${ALERT_MTLS_KEY:-$HOME/.webhook-caller/key.pem}"

# ADMIN_PORT 同理:部署后版本指纹核对直连容器端口,docker compose 能读到 .env
# 但 shell 不会;不提取则改端口后此处仍硬连旧值导致版本核对误报。
if [ -z "${ADMIN_PORT:-}" ] && [ -f .env ]; then
  ADMIN_PORT="$(grep -E '^ADMIN_PORT=' .env 2>/dev/null | tail -1 | cut -d= -f2- | tr -d "\"'" || true)"
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
# DEPLOY_LOCK_HELD 标记"锁已由父进程持有"：re-exec 的子进程跳过重新打开
# 锁文件（重新 exec 9> 会先关闭旧 fd，造成锁短暂释放的窗口）。
lockfile="${TMPDIR:-/tmp}/workwork-deploy.lock"
if [ "${DEPLOY_LOCK_HELD:-}" != "1" ]; then
  exec 9>"$lockfile"
  if command -v flock >/dev/null 2>&1; then
    if ! flock -n 9; then
      echo "错误: 另一个 deploy-changed 正在进行(锁: $lockfile),拒绝并发部署" >&2
      exit 1
    fi
  else
    echo "⚠ flock 不可用，跳过并发部署防护（仅本地复跑会出现）"
  fi
  export DEPLOY_LOCK_HELD=1
fi

# 成功部署的 commit 基线。不能用 git HEAD 当基线：部署失败并回滚后 HEAD
# 已经前移，下一次 diff 会从失败提交开始，失败批次的改动永久漏部署。
deployed_marker=".wework/deployed-commit"

# ---------- 输入与前置校验 ----------
# 分支断言:部署只允许发生在 main 上。在 origin/main 的祖先分支上 pull
# --ff-only 会静默快进该分支指针并照常部署,操作者却以为在别的分支
# (复跑模式同样要求 main:HEAD 即部署对象)。
current_branch="$(git branch --show-current)"
if [ "$current_branch" != "main" ]; then
  echo "错误: 当前分支 $current_branch 非 main,拒绝部署(切到 main 后重试)" >&2
  exit 1
fi
# 显式拉取 tag：git pull 只保证分支，实测服务器上 v2.0.0 tag 未被自动跟随，
# `git describe --tags` 落到 v1.2.0-11-g...，镜像版本烙印与发布 tag 不一致。
# 不带 --force：本地 tag 与远端冲突时让部署失败，而不是静默改写发布标记。
git fetch --tags origin
old=${1:-}
# .env 必须是普通文件：compose 用 `./.env:/app/.env:ro` 单文件挂载，缺失时
# Docker 会把 source 建成目录，之后 `cp .env.example .env` 报 "Is a directory"，
# 首次部署/灾备恢复直接死锁。此处提前失败并给出可执行修复。
if [ ! -f .env ] || [ -d .env ]; then
  echo "错误: .env 缺失或不是普通文件（当前: $(test -d .env && echo 目录 || echo 不存在)）。" >&2
  echo "      先执行: cp .env.example .env && chmod 600 .env，并填写 AUTH_PASS/IPV6_SUBNET 等必填项。" >&2
  exit 1
fi
if [ -n "$old" ]; then
  echo "复跑模式: 从 $old 推导变更（跳过 pull）"
else
  if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
    echo "错误: 工作区有未提交改动，拒绝部署（先 commit 或确认清理）" >&2
    exit 1
  fi
  if [ -f "$deployed_marker" ]; then
    marker="$(tr -d '[:space:]' <"$deployed_marker")"
    if [ -n "$marker" ] && git cat-file -e "${marker}^{commit}" 2>/dev/null; then
      old="$marker"
      echo "基线: 上次成功部署的 commit $old"
    else
      echo "⚠ $deployed_marker 记录的 commit '$marker' 不存在，回退到 HEAD 作为基线" >&2
      old=$(git rev-parse HEAD)
    fi
  else
    old=$(git rev-parse HEAD)
  fi
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
# 服务清单唯一来源是 compose；硬编码会在新增服务时静默漏部署、删除服务时
# 让 `docker compose build <已删除服务>` 永久失败。
ALL_SERVICES="$(./scripts/compose-services.sh)"
if [ -z "${ALL_SERVICES// /}" ]; then
  echo "错误: 无法从 docker-compose.yml 解析服务列表" >&2
  exit 1
fi
add_svc() {
  if ! printf '%s\n' $services | grep -qx "$1"; then
    services="$services $1"
  fi
}

# 发布 tag 变化（v2.0.0 → v2.0.1）不会体现在 changed 文件里，但四个镜像的
# 版本烙印都必须更新：否则健康端点仍报旧版本，回滚 tag 也对不上实际镜像。
# 用「最近 tag」比较而非 describe 全串：普通未打 tag 的提交仍只做最小部署。
old_tag="$(git describe --tags --abbrev=0 "$old" 2>/dev/null || true)"
new_tag="$(git describe --tags --abbrev=0 "$new" 2>/dev/null || true)"
if [ -n "$new_tag" ] && [ "$old_tag" != "$new_tag" ]; then
  echo "⚠ 发布 tag ${old_tag:-<none>} → $new_tag：全量重建以更新镜像版本烙印"
  for svc in $ALL_SERVICES; do add_svc "$svc"; done
fi

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
add_config_reload() {
  case " $config_reload " in
    *" $1 "*) ;;
    *) config_reload="$config_reload $1" ;;
  esac
}
if printf '%s\n' "$changed" | grep -q '^services/admin/configs/xray\.json\.tmpl$'; then
  add_config_reload xray
fi
if printf '%s\n' "$changed" | grep -q '^services/admin/configs/nginx\.conf\.tmpl$'; then
  add_config_reload nginx
fi

# 编排层变更无法按服务精确推导：compose 里的 env/卷/资源/健康检查可能影响
# 任意服务。此前只打印一句"请人工确认"就继续，services 为空时直接 exit 0，
# 这类修复永远不落地（bd2668c 只改 docker-compose.yml 就是实例）。改为按
# 全量服务处理。
if printf '%s\n' "$changed" | grep -qx 'docker-compose.yml'; then
  echo "⚠ docker-compose.yml 变更：编排层无法精确推导，按全量服务处理"
  for svc in $ALL_SERVICES; do add_svc "$svc"; done
fi
# .env.example 是示例文档，不是运行时输入；运行时 .env 被 gitignore，改它
# 不会出现在 git diff 里，需要人工 make up。这里明确区分，避免把示例变更
# 当成需要部署的配置变更。
if printf '%s\n' "$changed" | grep -qx '.env.example'; then
  echo "ℹ .env.example 变更（示例文档）：不触发部署；若改的是实际 .env，请手动 make up <svc...>"
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
  # 没有容器要动，但基线仍要推进：否则下一次 diff 会从更早的基线重复列出
  # 这批纯文档/脚本变更，且运维无法从 deployed-commit 判断部署进度。
  if ! mkdir -p "$(dirname "$deployed_marker")" || ! printf '%s\n' "$new" >"$deployed_marker"; then
    echo "⚠ 无法写入部署基线 $deployed_marker" >&2
  fi
  exit 0
fi

echo
echo "=== 受影响服务:$services ==="
# 记录部署前各服务镜像(含 tag)，部署后打印精确回滚命令。
# 用临时文件而不用关联数组：保持 bash 3 兼容（macOS 本地复跑）。
prev_file="$(mktemp)"
# 部署前快照 .wework/generated/：admin 启动时会先把模板渲染到这里。若新
# 模板渲染出的配置没通过 nginx -t / xray -test，运行中的进程还能靠内存旧
# 配置活着，但下一次任意 restart/宿主重启会加载坏配置直接下线。校验失败时
# 恢复快照，把这个"定时炸弹"拆掉。
generated_dir="$PWD/.wework/generated"
generated_backup=""
if [ -d "$generated_dir" ]; then
  generated_backup="$(mktemp -d)"
  if ! cp -a "$generated_dir/." "$generated_backup/" 2>/dev/null; then
    echo "⚠ .wework/generated 快照失败，校验失败时将无法恢复旧配置" >&2
    rm -rf "$generated_backup"
    generated_backup=""
  fi
fi
cleanup_deploy_temps() {
  rm -f "$prev_file"
  if [ -n "${generated_backup:-}" ]; then
    rm -rf "$generated_backup"
  fi
}
trap cleanup_deploy_temps EXIT
restore_generated_configs() {
  if [ -n "${generated_backup:-}" ] && [ -d "$generated_backup" ]; then
    echo "=== 恢复部署前的运行时配置（.wework/generated） ==="
    rm -rf "$generated_dir"
    mkdir -p "$generated_dir"
    cp -a "$generated_backup/." "$generated_dir/"
    echo "  已恢复，避免下次重启加载未通过校验的配置"
  fi
}

# wait_healthy 返回 0=healthy，1=超时。调用方决定如何记录失败。
wait_healthy() {
  local svc="$1" tries="${2:-30}" state
  for _ in $(seq 1 "$tries"); do
    state="$(docker inspect "$(docker compose ps -q "$svc")" --format '{{.State.Health.Status}}' 2>/dev/null || echo none)"
    if [ "$state" = "healthy" ]; then
      return 0
    fi
    sleep 5
  done
  return 1
}

# 失败路径专用：把 .wework/generated 恢复成部署前快照后，用旧配置重启
# xray/nginx 并等 healthy。只恢复文件不重启，盘上仍是坏配置的"定时炸弹"
# ——下一次任意 restart/宿主重启就会加载它。返回 1 表示有消费者没能恢复，
# 调用方只告警、不掩盖原始失败。
restart_config_consumers_with_restored() {
  [ -n "${generated_backup:-}" ] || return 0
  [ -n "${config_reload// /}" ] || return 0
  echo "=== 用部署前配置恢复运行时消费者:$config_reload ==="
  # shellcheck disable=SC2086
  if ! docker compose restart $config_reload; then
    echo "!! 用部署前配置重启$config_reload 失败，需人工处理" >&2
    return 1
  fi
  local rc=0
  for svc in $config_reload; do
    if wait_healthy "$svc"; then
      echo "  $svc 已加载部署前配置并 healthy"
    else
      echo "!! $svc 用部署前配置重启后 150s 内未 healthy" >&2
      rc=1
    fi
  done
  return "$rc"
}
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
  if ! wait_healthy "$svc"; then
    echo "!! $svc 150s 内未 healthy"
    fail="$fail $svc"
  fi
done

# ---------- 渲染产物变化检测 ----------
# 只按模板路径判断会漏掉“模板没变但渲染结果变了”的部署：改渲染代码
# （services/admin 里的 xray/nginx 生成逻辑）、XHTTP_DOMAIN/ADMIN_PORT 等
# 环境变量、或 admin 生成客户端台账的顺序，都会让 generated 内容变化，
# 但 xray/nginx 仍跑旧配置。这里用部署前快照做内容比对，变了才校验+重启。
if [ -n "${generated_backup:-}" ] && [ -d "$generated_backup" ]; then
  for svc in nginx xray; do
    before="$generated_backup/$svc"
    after="$generated_dir/$svc"
    [ -d "$before" ] || continue
    if [ ! -d "$after" ] || ! diff -rq "$before" "$after" >/dev/null 2>&1; then
      echo "  检测到 $svc 渲染产物变化（模板/渲染代码/env 之一）"
      add_config_reload "$svc"
    fi
  done
fi

# ---------- 配置消费者重启前的语法校验 ----------
# admin healthy 即渲染产物已落盘(bind mount 实时可见),此时在容器内跑
# nginx -t / xray -test:坏配置在重启前被拦下。带着坏配置 restart 意味着
# 代理整体下线;校验失败则维持旧配置继续服务(运行中进程用的是内存里的旧
# 配置),部署报失败并外发通知,修复模板重新跑即可。
validate_failed=""
if printf '%s' "$config_reload" | grep -qw nginx; then
  echo
  echo "=== 配置校验:nginx -t ==="
  if docker compose exec -T nginx nginx -t; then
    echo "  nginx 配置校验通过"
  else
    validate_failed="$validate_failed nginx"
  fi
fi
if printf '%s' "$config_reload" | grep -qw xray; then
  echo
  echo "=== 配置校验:xray run -test ==="
  if docker compose exec -T xray xray run -test -c /etc/xray/config.json >/dev/null 2>&1; then
    echo "  xray 配置校验通过"
  else
    validate_failed="$validate_failed xray"
  fi
fi
if [ -n "${validate_failed// /}" ]; then
  # 坏配置已经由 admin 渲染到 .wework/generated/。先把 admin 回滚到部署前
  # 镜像（避免它的下一次启动再次渲染坏模板），再恢复部署前的配置快照。
  if printf '%s\n' "$services" | grep -qw admin; then
    prev_image="$(awk -v s=admin '$1==s{print $2}' "$prev_file")"
    if [ -n "$prev_image" ] && [ "${prev_image##*:}" != "$IMAGE_TAG" ]; then
      echo "  回滚 admin → $prev_image（避免再次渲染坏配置）"
      if IMAGE_TAG="${prev_image##*:}" docker compose up -d --no-deps admin; then
        echo "  admin 已回滚"
      else
        echo "  !! admin 回滚失败，需人工处理: IMAGE_TAG=${prev_image##*:} docker compose up -d --no-deps admin" >&2
      fi
    fi
  fi
  restore_generated_configs
  restart_config_consumers_with_restored || true
  notify_deploy_failure "配置校验失败($validate_failed),已恢复部署前配置"
  echo "错误:$validate_failed 新配置校验未通过,已恢复部署前配置。修复模板后重新跑 deploy-changed。" >&2
  exit 1
fi

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
    if wait_healthy "$svc"; then
      # 显式确认，否则"重启了但没生效"在日志里与成功无法区分。
      echo "  $svc 已加载新配置并 healthy"
    else
      echo "!! $svc 重启后 150s 内未 healthy"
      fail="$fail $svc"
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
    # 但同一次部署里它也可能因 compose/版本文件变更被重建,那种情况仍要回滚镜像。
    if ! printf '%s\n' "$services" | grep -qw "$svc"; then
      case " $config_reload " in
        *" $svc "*)
          echo "  $svc: 配置消费者仅重启未换镜像，无镜像可回滚（人工: docker compose restart $svc）"
          rolled_back=""
          continue
          ;;
      esac
    fi
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
  # 任何失败都可能留下"语法合法但语义错误"的新配置；admin 半途渲染更是直接
  # 留下半成品。快照语义是部署前状态，与哪个服务失败无关，因此始终恢复。
  restore_generated_configs
  restart_config_consumers_with_restored || true
  if [ -n "$rolled_back" ]; then
    echo "全部失败服务已回滚到部署前版本"
  else
    echo "存在未能自动回滚的服务，见上方日志（旧 tag 镜像须仍在本地:勿对它们 docker image prune）"
  fi
  # 外发失败通知（WEBHOOK_URL,与 admin 事件通知同源;未配置为 no-op）。
  notify_deploy_failure "部署校验未通过:$fail"
  exit 1
fi
# 只有完整成功才推进基线。失败路径保持旧基线，下一次会把同一批变更重新
# 部署（而不是因为 HEAD 已前移而永久跳过）。基线落盘失败不能让一次成功
# 的部署变成非零退出，只告警（下次会重复部署一批已上线的变更）。
if ! mkdir -p "$(dirname "$deployed_marker")" || ! printf '%s\n' "$new" >"$deployed_marker"; then
  echo "⚠ 无法写入部署基线 $deployed_marker；下次部署可能重复处理已上线的变更" >&2
fi
echo "部署校验通过 ✅（回滚：IMAGE_TAG=<旧tag> docker compose up -d --no-deps$services）"
