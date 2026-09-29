#!/usr/bin/env bash
# 备份恢复验证:对一次备份做「能否支撑恢复」的机械校验——哈希、关键状态
# 文件、fail2ban sqlite 库完整性、证书配对。backup.sh 每次备份后自动调用
# (失败 → backup.failure webhook),也可独立运行:
#   scripts/restore-verify.sh [备份目录] [时间戳STAMP]
#
# 它覆盖的是演练的机械部分;「真机从备份启动」的完整演练仍按
# docs/operations.md 手动执行(机械校验通过 ≠ 应用一定能在旧数据上起)。
set -euo pipefail

DIR="${1:-/var/backups/workwork}"
if [ -z "${2:-}" ]; then
  STAMP="$(ls -t "$DIR"/workwork-manifest-*.txt 2>/dev/null | head -1 | sed -e 's/.*manifest-//' -e 's/\.txt$//')"
else
  STAMP="$2"
fi
[ -n "$STAMP" ] || { echo "!! 未找到任何备份清单($DIR)" >&2; exit 1; }
MANIFEST="$DIR/workwork-manifest-$STAMP.txt"
[ -f "$MANIFEST" ] || { echo "!! 清单不存在: $MANIFEST" >&2; exit 1; }

echo "=== 恢复验证 $STAMP ==="
FAILS=0
SKIPS=0
check() { # check <名称> <命令...> —— 命令须隐含"目标存在",缺失记 SKIP
  local name=$1; shift
  if "$@" >/dev/null 2>&1; then
    echo "  ✓ $name"
  else
    echo "  ✗ $name"
    FAILS=$((FAILS + 1))
  fi
}
skip() { echo "  - $1(跳过)"; SKIPS=$((SKIPS + 1)); }

# sha256 工具两代系统名不同：Linux=sha256sum，macOS=shasum -a 256
# （与 backup.sh 同款探测；本脚本经 make backup 链式调用，且需支持 macOS 本地复跑）
if command -v sha256sum >/dev/null 2>&1; then
  sha() { sha256sum "$@"; }
else
  sha() { shasum -a 256 "$@"; }
fi

# 1) 清单哈希逐文件校验(du 行不是 sha 行,按 64-hex 首列过滤)
cd "$DIR"
echo "· 哈希校验"
if awk '$1 ~ /^[0-9a-f]{64}$/ {print $1"  "$2}' "$MANIFEST" | sha -c -; then
  echo "  ✓ sha256 全部一致"
else
  echo "  ✗ sha256 不一致"
  exit 1
fi

# 2) 解包到临时目录
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
for f in wework cert env fail2ban; do
  src="$DIR/workwork-$f-$STAMP.tar.gz"
  [ -f "$src" ] || continue
  mkdir -p "$WORK/$f"
  tar -xzf "$src" -C "$WORK/$f"
done

echo "· 关键状态文件"
# .wework 归档打包时以 -C $ROOT .wework 打包,含顶层 .wework/ 目录。
WW="$WORK/wework/.wework"
check "登录凭证 .htpasswd 非空" test -s "$WW/nginx-auth/.htpasswd"
check "代理凭证 .htpasswd-proxy 存在" test -f "$WW/nginx-auth/.htpasswd-proxy"
if [ -f "$WW/users.json" ]; then
  check "users.json 合法且非空" python3 -c '
import json, sys
d = json.load(open(sys.argv[1]))
assert d.get("users"), "users 为空"
' "$WW/users.json"
else
  skip "users.json"
fi
check "生成的 nginx 配置存在" test -s "$WW/generated/nginx/blocked-ips.conf"
check "生成的 xray 配置为合法 JSON" python3 -c '
import json, sys
cfg = json.load(open(sys.argv[1]))
assert cfg.get("inbounds"), "inbounds 为空"
' "$WW/generated/xray/config.json"

echo "· 证书"
CERT="$WORK/cert/cert/server_cert.pem" KEY="$WORK/cert/cert/server_cert.key"
if [ -f "$CERT" ] && [ -f "$KEY" ]; then
  if [ "$(openssl x509 -in "$CERT" -noout -pubkey 2>/dev/null | openssl pkey -pubin -outform DER 2>/dev/null | sha | cut -d" " -f1)" = "$(openssl pkey -in "$KEY" -pubout -outform DER 2>/dev/null | sha | cut -d" " -f1)" ]; then
    echo "  ✓ 证书与私钥配对"
  else
    echo "  ✗ 证书与私钥不配对"
    FAILS=$((FAILS + 1))
  fi
else
  skip "证书文件"
fi

echo "· 环境变量"
if [ -f "$WORK/env/.env" ]; then
  check ".env 非空且含 AUTH_USER" bash -c "test -s '$WORK/env/.env' && grep -q '^AUTH_USER=' '$WORK/env/.env'"
else
  skip ".env"
fi

echo "· sqlite 库完整性"
if [ -f "$WORK/fail2ban/fail2ban.sqlite3" ]; then
  check "fail2ban 库 integrity_check" python3 -c '
import sqlite3, sys
c = sqlite3.connect(sys.argv[1])
assert c.execute("PRAGMA integrity_check").fetchone()[0] == "ok"
' "$WORK/fail2ban/fail2ban.sqlite3"
else
  skip "fail2ban 库"
fi

echo "=== 结果: 失败 $FAILS,跳过 $SKIPS ==="
[ "$FAILS" -eq 0 ]
