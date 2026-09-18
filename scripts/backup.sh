#!/usr/bin/env bash
# workwork 数据备份：把灾备恢复所需的一切打成带时间戳的 tar 包。
#
# 覆盖内容（缺哪个跳哪个，各自独立告警）：
#   .wework/    运行时数据全集：htpasswd+users.json(登录能力)、generated/(admin
#               生成的 nginx/xray/媒体配置)、music-library(曲库)、网易云/YouTube
#               cookies(凭证)、internal token（fail2ban 持久库单独打包，见下）
#   cert/       TLS 证书与私钥（存在时）
#   .env        环境变量（存在时）
#   named volume media_center_data：catalog.db 用 sqlite3 .backup 一致性快照
#               （活跃库直接 tar 会页级撕裂），其余文件照常 tar
#   services/nginx/fail2ban-data  fail2ban 持久库（撞库计数/封禁状态）
#
# 备份是敏感数据整体打包：归档权限 0600，异机传输用 BACKUP_RSYNC_TARGET。
# 恢复步骤与演练清单见 docs/operations.md。失败时向 WEBHOOK_URL 发
# backup.failure 事件（未配置为 no-op）——夜间 cron 失败无人看日志。
#
# 用法（服务器 cron 示例见 docs/operations.md）：
#   scripts/backup.sh                              # 备份到 BACKUP_DIR（默认 /var/backups/workwork）
#   BACKUP_DIR=/mnt/nas/workwork scripts/backup.sh
#   BACKUP_RSYNC_TARGET=user@nas:/backups scripts/backup.sh   # 打包后异机 rsync
#   BACKUP_RETENTION_DAYS=30 scripts/backup.sh
#   WORKWORK_ROOT=/tmp/fake-root scripts/backup.sh # 测试：指定仓库根
set -euo pipefail

ROOT="${WORKWORK_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}"
BACKUP_DIR="${BACKUP_DIR:-/var/backups/workwork}"
BACKUP_RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-14}"
BACKUP_RSYNC_TARGET="${BACKUP_RSYNC_TARGET:-}"
# 额外排除项（相对 ROOT 的路径，逗号分隔，如 ".wework/music-library"）
BACKUP_EXCLUDE="${BACKUP_EXCLUDE:-}"
STAMP="$(date +%Y%m%d-%H%M%S)"

# WEBHOOK_URL:优先取调用方环境,否则从 .env 单独提取(同 deploy-changed.sh)。
if [ -z "${WEBHOOK_URL:-}" ] && [ -f "$ROOT/.env" ]; then
  WEBHOOK_URL="$(grep -E '^WEBHOOK_URL=' "$ROOT/.env" 2>/dev/null | head -1 | cut -d= -f2- | tr -d "\"'" || true)"
fi
notify_backup_failure() {
  local rc=${1:-?}
  [ -n "${WEBHOOK_URL:-}" ] || return 0
  curl -fsS --max-time 5 -H 'Content-Type: application/json' \
    -d "$(printf '{"event":"backup.failure","summary":"backup.sh 失败(exit %s)","time":"%s"}' \
      "$rc" "$(date -u +%Y-%m-%dT%H:%M:%SZ)")" \
    "${WEBHOOK_URL}" >/dev/null 2>&1 ||
    echo "⚠ webhook 失败通知发送失败" >&2 || true
}
# 任何非零退出(含 set -e 中断)都外发失败通知;EXIT trap 尾部回传原退出码。
trap 'rc=$?; [ "$rc" -ne 0 ] && notify_backup_failure "$rc"; exit $rc' EXIT

mkdir -p "$BACKUP_DIR"
# 备份目录含全部凭据与配置，权限收紧；已存在时 chmod 不报错。
chmod 700 "$BACKUP_DIR" 2>/dev/null || true

echo "=== workwork 备份 $STAMP ==="
echo "仓库根: $ROOT"
echo "备份到: $BACKUP_DIR"

missing=""
parts=()

# 1) .wework/ 运行时数据（丢它 = 丢登录能力 + 配置 + 凭证）
exclude_args=()
if [ -n "$BACKUP_EXCLUDE" ]; then
  IFS=',' read -r -a _excludes <<<"$BACKUP_EXCLUDE"
  for e in "${_excludes[@]}"; do
    exclude_args+=(--exclude="${e#./}")
  done
fi
if [ -d "$ROOT/.wework" ]; then
  f="$BACKUP_DIR/workwork-wework-$STAMP.tar.gz"
  # ${arr[@]+...} 条件展开：bash 3.2(macOS) 下 set -u 对空数组直接报 unbound
  tar -czf "$f" -C "$ROOT" ${exclude_args[@]+"${exclude_args[@]}"} .wework
  parts+=("$f")
else
  echo "  ⚠ .wework/ 不存在（$ROOT/.wework），未备份运行时数据"
  missing="$missing .wework"
fi

# 2) cert/ 与 .env（本地开发环境通常没有，跳过属正常）
if [ -d "$ROOT/cert" ]; then
  f="$BACKUP_DIR/workwork-cert-$STAMP.tar.gz"
  tar -czf "$f" -C "$ROOT" cert
  parts+=("$f")
else
  echo "  跳过: cert/ 不存在"
fi
if [ -f "$ROOT/.env" ]; then
  f="$BACKUP_DIR/workwork-env-$STAMP.tar.gz"
  tar -czf "$f" -C "$ROOT" .env
  parts+=("$f")
else
  echo "  跳过: .env 不存在"
fi

# 3) named volume media_center_data。catalog.db 是 media_server 持续写入的
#    活跃 SQLite,tar 顺序拷贝 db/-wal/-shm 不在同一时点,恢复端可能页级撕裂;
#    一致性快照走 sqlite3 .backup(在线备份 API,恢复端拿到免 WAL 的完整库)。
#    快照成功时 tar 排除 db 文件(避免热拷贝被误作恢复源);快照失败时回退为
#    整卷 tar 并告警(恢复前必须 PRAGMA integrity_check)。volume 实际名字带
#    compose 项目前缀,按后缀匹配;docker 不可用(本地开发)时跳过。
if command -v docker >/dev/null 2>&1; then
  vol="$(docker volume ls --format '{{.Name}}' | grep -i 'media_center_data$' | head -1 || true)"
  if [ -n "$vol" ]; then
    snap_dir="$BACKUP_DIR/.sqlite-snap-$STAMP"
    mkdir -p "$snap_dir"
    # 挂载不加 :ro:WAL 模式下读者也要建 -shm,.backup 需要写侧通道。
    if docker run --rm -v "$vol:/data" -v "$snap_dir:/backup" alpine:3.24.1 \
        sh -c 'apk add --no-cache sqlite >/dev/null 2>&1; found=0
               for db in /data/*.db; do
                 [ -e "$db" ] || continue
                 sqlite3 "$db" ".backup \"/backup/$(basename "$db")\"" || exit 1
                 found=1
               done
               [ "$found" = 1 ]' ; then
      db_tar="$BACKUP_DIR/workwork-media_center_data-db-$STAMP.tar.gz"
      tar -czf "$db_tar" -C "$snap_dir" .
      rm -rf "$snap_dir"
      parts+=("$db_tar")
      db_exclude=(--exclude='*.db' --exclude='*.db-wal' --exclude='*.db-shm')
    else
      echo "  ⚠ sqlite3 一致性快照失败,catalog.db 回退为热拷贝 tar(恢复前须 integrity_check)" >&2
      missing="$missing media_center_data-sqlite-snapshot"
      db_exclude=()
    fi
    f="$BACKUP_DIR/workwork-media_center_data-$STAMP.tar.gz"
    docker run --rm -v "$vol:/data:ro" -v "$BACKUP_DIR:/backup" alpine:3.24.1 \
      tar czf "/backup/$(basename "$f")" -C /data ${db_exclude[@]+"${db_exclude[@]}"} .
    parts+=("$f")
  else
    echo "  跳过: 未找到 media_center_data volume（服务未部署？）"
    missing="$missing media_center_data"
  fi
else
  echo "  跳过: docker 不可用，未导出 media_center_data"
  missing="$missing media_center_data"
fi

# 3.5) fail2ban 持久库（services/nginx/fail2ban-data:sqlite 计数/封禁状态）。
#      此前不在备份内而文档声称覆盖——整机故障后撞库计数清零。
if [ -d "$ROOT/services/nginx/fail2ban-data" ]; then
  f="$BACKUP_DIR/workwork-fail2ban-$STAMP.tar.gz"
  tar -czf "$f" -C "$ROOT" services/nginx/fail2ban-data
  parts+=("$f")
else
  echo "  跳过: services/nginx/fail2ban-data 不存在（nginx 未部署？）"
  missing="$missing fail2ban-data"
fi

if [ "${#parts[@]}" -eq 0 ]; then
  echo "!! 没有生成任何备份文件，中止" >&2
  exit 1
fi

# 4) 清单（大小 + sha256），权限收紧到 0600
# sha256 工具两代系统名不同：Linux=sha256sum，macOS=shasum -a 256
if command -v sha256sum >/dev/null 2>&1; then
  sha() { sha256sum "$@"; }
else
  sha() { shasum -a 256 "$@"; }
fi
manifest="$BACKUP_DIR/workwork-manifest-$STAMP.txt"
{
  echo "workwork backup $STAMP"
  echo "root: $ROOT"
  echo "missing:[$missing]"
  du -h "${parts[@]}"
  sha "${parts[@]}"
} >"$manifest"
chmod 0600 "${parts[@]}" "$manifest"

# 5) 保留期清理（非法值显式告警,不静默跳过）
case "$BACKUP_RETENTION_DAYS" in
  ''|*[!0-9]*)
    echo "⚠ BACKUP_RETENTION_DAYS='$BACKUP_RETENTION_DAYS' 非数字,按 0(不清理)处理" >&2
    BACKUP_RETENTION_DAYS=0
    ;;
esac
if [ "$BACKUP_RETENTION_DAYS" -gt 0 ]; then
  find "$BACKUP_DIR" -name 'workwork-*-*.tar.gz' -mtime +"$BACKUP_RETENTION_DAYS" -delete
  find "$BACKUP_DIR" -name 'workwork-manifest-*.txt' -mtime +"$BACKUP_RETENTION_DAYS" -delete
fi

# 6) 异机复制（把备份挪出单机是备份有意义的前提）
if [ -n "$BACKUP_RSYNC_TARGET" ]; then
  echo "=== rsync 到 $BACKUP_RSYNC_TARGET ==="
  rsync -a --chmod=F600 "$BACKUP_DIR"/workwork-*-"$STAMP".* "$BACKUP_RSYNC_TARGET/"
fi

echo "=== 备份完成 ==="
du -h "${parts[@]}"
echo "清单: $manifest"
