#!/bin/bash
# 生成 xray 配置文件，替换占位符为实际的 UUID 和密钥
# 用法: ./gen-xray-config.sh <实例编号>
# 例如: ./gen-xray-config.sh 1

set -e

if [ -z "$1" ]; then
  echo "Usage: $0 <instance_number>"
  echo "Example: $0 1"
  exit 1
fi

N=$1
CONFIG="configs/xray-reality-${N}.json"

if [ ! -f "$CONFIG" ]; then
  echo "Error: ${CONFIG} not found."
  exit 1
fi

UUID=$(docker run --rm teddysun/xray xray uuid)
KEYS=$(docker run --rm teddysun/xray xray x25519)
PRIVATE_KEY=$(echo "$KEYS" | grep "Private" | awk '{print $3}')
PUBLIC_KEY=$(echo "$KEYS" | grep "Public" | awk '{print $3}')

sed -i.bak \
  -e "s|REPLACE_WITH_YOUR_UUID_${N}|${UUID}|g" \
  -e "s|REPLACE_WITH_YOUR_PRIVATE_KEY_${N}|${PRIVATE_KEY}|g" \
  "$CONFIG"

rm -f "${CONFIG}.bak"

echo "=== Instance ${N} ==="
echo "UUID:        ${UUID}"
echo "Private Key: ${PRIVATE_KEY}"
echo "Public Key:  ${PUBLIC_KEY}"
echo ""
echo "Config saved to ${CONFIG}"
echo "Remember to share the Public Key with clients."