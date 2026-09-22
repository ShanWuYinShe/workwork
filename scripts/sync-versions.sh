#!/usr/bin/env bash
# 跨仓基础镜像版本统一工具：workwork/versions.env 是唯一主本，
# media_center / music_player（与本仓并列的检出目录）的 versions.env、
# 各 Dockerfile 的 ARG 默认值、CI 的 go-version/bun-version、compose 的
# :- 兜底值，全部以主本为准。
#
# 用法:
#   sync-versions.sh check      # 全矩阵一致性校验,漂移即退出 1(make check 已挂)
#   sync-versions.sh sync       # 把主本值刷写到所有管理点位(改完需按仓库
#                               # 各自的 worktree 流程提交;本脚本不改 git)
#   sync-versions.sh sync --dry # 预览将要改写的位置,不落盘
#
# 站点仓定位:与 workwork 仓库根并列的 media_center / music_player 目录
# (本地 ~/work 与服务器 ~ 布局一致);可用环境变量 SITES 覆盖。
# 某站点目录不存在时(CI 等场景)跳过并告警,不判失败。
#
# 管理键 = 主本 versions.env 里的全部键。站点专属键(如 PYTHON_VERSION、
# XRAY_VERSION)只在该仓内部生效,不参与跨仓同步。
set -euo pipefail

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MASTER_ROOT="$(git -C "$SELF_DIR" rev-parse --show-toplevel)"
MASTER_FILE="$MASTER_ROOT/versions.env"
MODE="${1:-check}"
DRY=""
[ "${2:-}" = "--dry" ] && DRY="1"

[ -f "$MASTER_FILE" ] || { echo "主本不存在: $MASTER_FILE" >&2; exit 1; }

# 站点仓:仓库根的并列目录(存在才纳入)
SITES="${SITES:-}"
if [ -z "$SITES" ]; then
  for name in media_center music_player; do
    d="$(dirname "$MASTER_ROOT")/$name"
    [ -d "$d/.git" ] && SITES="$SITES $d"
  done
fi

master_keys() { grep -vE '^[[:space:]]*#|^[[:space:]]*$' "$MASTER_FILE" | cut -d= -f1; }
master_val() { grep -E "^$1=" "$MASTER_FILE" | head -1 | cut -d= -f2-; }

# repo_files <repo根>: 输出该仓参与版本管理的文件,格式 "<类型>\t<相对路径>"
repo_files() {
  git -C "$1" ls-files -- 'versions.env' 'deploy/versions.env' \
      '*Dockerfile*' '.github/workflows/*.yml' 'docker-compose.yml' 2>/dev/null |
  while IFS= read -r f; do
    case "$f" in
      */versions.env|versions.env) echo "env	$f" ;;
      *Dockerfile*)                echo "dockerfile	$f" ;;
      */workflows/*)               echo "ci	$f" ;;
      *compose*.yml)               echo "compose	$f" ;;
    esac
  done
}

# spot_value <repo根> <类型> <相对路径> <键>: 读该点位当前生效值(无则空)
spot_value() {
  local root="$1" typ="$2" f="$3" key="$4"
  local p="$root/$f"
  [ -f "$p" ] || return 0
  case "$typ" in
    env)        { grep -E "^$key=" "$p" || true; } | head -1 | cut -d= -f2- ;;
    dockerfile) { grep -E "^ARG $key=" "$p" || true; } | head -1 | sed "s/^ARG $key=//" ;;
    ci)
      case "$key" in
        GO_VERSION)  { grep -E "go-version:" "$p" || true; } | head -1 | sed "s/.*go-version:[[:space:]]*['\"]\{0,1\}\([^'\"]*\)['\"]\{0,1\}.*/\1/" ;;
        BUN_VERSION) { grep -E "bun-version:" "$p" || true; } | head -1 | sed "s/.*bun-version:[[:space:]]*['\"]\{0,1\}\([^'\"]*\)['\"]\{0,1\}.*/\1/" ;;
      esac ;;
    compose) { grep -oE "\\\$\{$key:-[^}]*\}" "$p" || true; } | head -1 | sed "s/.*:-//;s/}//" ;;
  esac
}

# rewrite <repo根> <类型> <相对路径> <键> <新值>
rewrite() {
  local root="$1" typ="$2" f="$3" key="$4" val="$5"
  local p="$root/$f"
  [ -f "$p" ] || return 0
  case "$typ" in
    env)        sed -i.bak "s|^$key=.*|$key=$val|" "$p" ;;
    dockerfile) sed -i.bak "s|^ARG $key=.*|ARG $key=$val|" "$p" ;;
    ci)
      case "$key" in
        GO_VERSION)  sed -i.bak "s|^\(.*go-version:[[:space:]]*\)['\"][^'\"]*['\"]|\1'$val'|" "$p" ;;
        BUN_VERSION) sed -i.bak "s|^\(.*bun-version:[[:space:]]*\)['\"][^'\"]*['\"]|\1'$val'|" "$p" ;;
      esac ;;
    compose) sed -i.bak "s|\\\$\{$key:-[^}]*\}|\${$key:-$val}|g" "$p" ;;
  esac
  rm -f "$p.bak"
}

drift=0
changed=0

for root in "$MASTER_ROOT" $SITES; do
  label=$([ "$root" = "$MASTER_ROOT" ] && echo "主本" || basename "$root")
  echo "-- $label ($root)"
  kvt=$(mktemp)
  grep -vE '^[[:space:]]*#|^[[:space:]]*$' "$MASTER_FILE" > "$kvt"
  while IFS= read -r line; do
    key="${line%%=*}"; want="${line#*=}"
    while IFS=$'\t' read -r typ f; do
      [ -n "${typ:-}" ] || continue
      case "$typ" in
        ci) case "$key" in GO_VERSION|BUN_VERSION) ;; *) continue ;; esac ;;
      esac
      cur=$(spot_value "$root" "$typ" "$f" "$key")
      [ -n "$cur" ] || continue
      if [ "$cur" = "$want" ]; then
        printf "  ✓ %-42s %-14s %s\n" "$f" "$key" "$cur"
      else
        drift=$((drift+1))
        printf "  ✗ %-42s %-14s %s (主本 %s)\n" "$f" "$key" "$cur" "$want"
        if [ "$MODE" = "sync" ]; then
          if [ -n "$DRY" ]; then
            echo "    [dry] 将改写为 $want"
          else
            rewrite "$root" "$typ" "$f" "$key" "$want"
            echo "    已改写 → $want"
            changed=$((changed+1))
          fi
        fi
      fi
    done < <(repo_files "$root")
  done < "$kvt"
  rm -f "$kvt"
  # 站点专属键:主本没有的键不参与同步,仅展示
  if [ -f "$root/versions.env" ]; then
    grep -vE '^[[:space:]]*#|^[[:space:]]*$' "$root/versions.env" | cut -d= -f1 |
    while IFS= read -r k; do
      master_keys | grep -qx "$k" || printf "  · 站点专属键(不参与同步) %-10s %s\n" "$k" "$(grep -E "^$k=" "$root/versions.env" | cut -d= -f2-)"
    done
  fi
  echo
done

if [ "$MODE" = "sync" ]; then
  if [ "$drift" -eq 0 ]; then
    echo "sync: 无需改写,已全部一致。"
  else
    echo "sync: 改写 $changed 处,剩余未覆盖漂移 $drift 处(见上方 ✗)。"
    [ -n "$DRY" ] && echo "(dry-run,未落盘)"
    echo "后续: 各仓按 worktree 流程提交推送 → 服务器 pull → make build && make up <svc...>"
  fi
else
  if [ "$drift" -gt 0 ]; then
    echo "check: 发现 $drift 处版本漂移。" >&2
    exit 1
  fi
  echo "check: 全部一致 ✓"
fi
