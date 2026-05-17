#!/bin/sh
set -e

# Fetch latest Cloudflare IP ranges on every container start
fetch-cloudflare-ips.sh 2>/dev/null || echo "WARNING: failed to fetch Cloudflare IP ranges, using cached" >&2

# Set up logrotate cron: run daily at 03:00
echo "0 3 * * * /usr/sbin/logrotate /etc/logrotate.d/nginx" | crontab -

# Generate xhttp proxy htpasswd if XHTTP_PROXY_AUTH is configured
if [ -n "$XHTTP_PROXY_AUTH" ]; then
    PROXY_USER=$(echo "$XHTTP_PROXY_AUTH" | cut -d: -f1)
    PROXY_PASS=$(echo "$XHTTP_PROXY_AUTH" | cut -d: -f2)
    if [ -n "$PROXY_USER" ] && [ -n "$PROXY_PASS" ]; then
        printf '%s' "$PROXY_PASS" | htpasswd -i -c /etc/nginx/htpasswd/.htpasswd-proxy "$PROXY_USER" 2>/dev/null
    fi
fi

exec "$@"
