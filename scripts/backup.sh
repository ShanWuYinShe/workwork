#!/usr/bin/env bash
# workwork 数据备份：把灾备恢复所需的一切打成带时间戳的 tar 包。
#
# 覆盖内容（缺哪个跳哪个，各自独立告警）：
#   .wework/    运行时数据全集：htpasswd+users.json(登录能力)、generated/(admin
#               生成的 nginx/xray/媒体配置)、music-library(曲库)、网易云/YouTube
#               cookies(凭证)、fail2ban 状态、internal token
#   cert/       TLS 证书与私钥（存在时）
#   .env        环境变量（存在时）
#   named volume media_center_data（catalog.db，SQLite）单独导出
#
# 备份是敏感数据整体打包：归档权限 0600，异机传输用 BACKUP_RSYNC_TARGET。
# 恢复步骤与演练清单见 docs/operations.md。
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

mkdir -p "$BACKUP_DIR"
# 备份目录含全部凭据与配置，权限收紧；已存在时 chmod 不报错。
chmod 700 "$BACKUP_DIR" 2>/dev/null || true

echo "=== workwork 备份 $STAMP ==="
echo "仓库根: $ROOT"
echo "备份到: $BACKUP_DIR"

missing=""
need() { # need <label> <path>
  if [ -e "$2" ]; then
    return 0
  fi
  echo "  跳过: $1 不存在（$2）"
  missing="$missing $1"
  return 1
}

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

# 3) named volume media_center_data（catalog.db）。volume 实际名字带 compose
#    项目前缀，按后缀匹配而不是写死；docker 不可用（本地开发）时跳过。
if command -v docker >/dev/null 2>&1; then
  vol="$(docker volume ls --format '{{.Name}}' | grep -i 'media_center_data$' | head -1 || true)"
  if [ -n "$vol" ]; then
    f="$BACKUP_DIR/workwork-media_center_data-$STAMP.tar.gz"
    docker run --rm -v "$vol:/data:ro" -v "$BACKUP_DIR:/backup" alpine:3.24.1 \
      tar czf "/backup/$(basename "$f")" -C /data .
    parts+=("$f")
  else
    echo "  跳过: 未找到 media_center_data volume（服务未部署？）"
    missing="$missing media_center_data"
  fi
else
  echo "  跳过: docker 不可用，未导出 media_center_data"
  missing="$missing media_center_data"
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

# 5) 保留期清理
if [ "$BACKUP_RETENTION_DAYS" -gt 0 ] 2>/dev/null; then
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
