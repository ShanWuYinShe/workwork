#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

if [ ! -f .env ]; then
    echo "Error: .env not found. Copy .env.example and fill in values:"
    echo "  cp .env.example .env"
    exit 1
fi

source .env

REQUIRED_VARS=(IPV6_PREFIX NGINX_IPV6_SUFFIX XRAY_IPV6_SUFFIX XRAY_CLIENT_IDS XRAY_PRIVATE_KEY XRAY_SHORT_IDS XRAY_REALITY_DEST XRAY_REALITY_SERVER_NAME CLIPROXY_API_KEYS AUTH_USER AUTH_PASS)
for var in "${REQUIRED_VARS[@]}"; do
    if [ -z "${!var:-}" ]; then
        echo "Error: $var is not set in .env"
        exit 1
    fi
done

echo "Generating configs from templates..."

# --- Xray ---
echo "  services/xray/config.json"
IFS=',' read -ra UUIDS <<< "$XRAY_CLIENT_IDS"
CLIENTS_JSON="["
for i in "${!UUIDS[@]}"; do
    [ "$i" -gt 0 ] && CLIENTS_JSON+=","
    CLIENTS_JSON+="{\"id\":\"${UUIDS[$i]}\",\"flow\":\"\"}"
done
CLIENTS_JSON+="]"

REALITY_CLIENTS_JSON="["
for i in "${!UUIDS[@]}"; do
    [ "$i" -gt 0 ] && REALITY_CLIENTS_JSON+=","
    REALITY_CLIENTS_JSON+="{\"id\":\"${UUIDS[$i]}\",\"flow\":\"xtls-rprx-vision\"}"
done
REALITY_CLIENTS_JSON+="]"

sed \
    -e "s|__XRAY_CLIENTS_JSON__|$CLIENTS_JSON|g" \
    -e "s|__XRAY_REALITY_CLIENTS_JSON__|$REALITY_CLIENTS_JSON|g" \
    -e "s|__IPV6_PREFIX____XRAY_IPV6_SUFFIX__|${IPV6_PREFIX}${XRAY_IPV6_SUFFIX}|g" \
    -e "s|__XRAY_REALITY_DEST__|$XRAY_REALITY_DEST|g" \
    -e "s|__XRAY_REALITY_SERVER_NAME__|$XRAY_REALITY_SERVER_NAME|g" \
    -e "s|__XRAY_PRIVATE_KEY__|$XRAY_PRIVATE_KEY|g" \
    -e "s|__XRAY_SHORT_IDS__|$XRAY_SHORT_IDS|g" \
    services/xray/config.json.tmpl > services/xray/config.json

# --- Nginx ---
echo "  services/nginx/nginx.conf"
sed \
    -e "s|__IPV6_PREFIX____NGINX_IPV6_SUFFIX__|${IPV6_PREFIX}${NGINX_IPV6_SUFFIX}|g" \
    services/nginx/nginx.conf.tmpl > services/nginx/nginx.conf

echo "  services/nginx/.htpasswd"
docker run --rm alpine sh -c "apk add --no-cache apache2-utils >/dev/null 2>&1 && htpasswd -nbB '$AUTH_USER' '$AUTH_PASS'" > services/nginx/.htpasswd

# --- CliproxyAPI ---
echo "  services/cliproxyapi/config.yaml"
IFS=',' read -ra API_KEYS <<< "$CLIPROXY_API_KEYS"
API_KEYS_YAML=""
for key in "${API_KEYS[@]}"; do
    API_KEYS_YAML+="  - \"$key\""$'\n'
done
API_KEYS_YAML="${API_KEYS_YAML%$'\n'}"

sed \
    -e "s|__CLIPROXY_API_KEYS_YAML__|$API_KEYS_YAML|g" \
    services/cliproxyapi/config.yaml.tmpl > services/cliproxyapi/config.yaml

echo ""
echo "Config generation complete. Run 'docker compose up -d' to start services."
