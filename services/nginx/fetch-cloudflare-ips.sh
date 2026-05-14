#!/bin/sh
# Fetch Cloudflare IP ranges and generate nginx include file.
# Falls back to built-in ranges if fetch fails.
set -e
OUT=/etc/nginx/cloudflare-ips.conf
TMP=$(mktemp /tmp/cf-ips.XXXXXX)
trap 'rm -f "$TMP"' EXIT

{
  echo "# Cloudflare IP ranges — auto-generated at $(date -Iseconds)"
  echo "# Cloudflare IPv4"
  curl -fsSL --connect-timeout 10 https://www.cloudflare.com/ips-v4 2>/dev/null | while read ip; do
    [ -n "$ip" ] && echo "set_real_ip_from $ip;"
  done
  echo "# Cloudflare IPv6"
  curl -fsSL --connect-timeout 10 https://www.cloudflare.com/ips-v6 2>/dev/null | while read ip; do
    [ -n "$ip" ] && echo "set_real_ip_from $ip;"
  done
  echo "real_ip_header CF-Connecting-IP;"
} > "$TMP" && mv "$TMP" "$OUT"
