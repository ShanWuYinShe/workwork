#!/bin/sh
set -e

# Fetch latest Cloudflare IP ranges on every container start
fetch-cloudflare-ips.sh 2>/dev/null || echo "WARNING: failed to fetch Cloudflare IP ranges, using cached" >&2

exec "$@"
