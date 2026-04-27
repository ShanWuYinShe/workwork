#!/usr/bin/env bash
set -euo pipefail

API="https://api.cloudflare.com/client/v4"

usage() {
    cat <<EOF
Usage: $(basename "$0") <command> [args]

Commands:
  ban <ip> [duration]   Ban an IP (duration in minutes, default 60, max 1440)
  unban <ip>            Unban an IP
  list                  List all firewall rules
  help                  Show this help

Environment:
  CF_API_TOKEN          Cloudflare API token
  CF_ZONE_ID            Cloudflare Zone ID

EOF
}

require_var() {
    if [[ -z "${!1:-}" ]]; then
        echo "Error: $1 is not set" >&2
        exit 1
    fi
}

api_request() {
    local method="$1"
    local path="$2"
    local body="${3:-}"

    require_var CF_API_TOKEN
    require_var CF_ZONE_ID

    local curl_cmd=(curl -s -X "$method" \
        -H "Authorization: Bearer $CF_API_TOKEN" \
        -H "Content-Type: application/json")

    if [[ -n "$body" ]]; then
        curl_cmd+=(-d "$body")
    fi

    "${curl_cmd[@]}" "$API/zones/$CF_ZONE_ID/$path"
}

list_rules() {
    local response
    response=$(api_request GET "firewall/rules?per_page=100")
    echo "$response" | python3 -c "
import sys, json
data = json.load(sys.stdin)
if not data.get('success'):
    print('API Error:', data.get('errors'), file=sys.stderr)
    sys.exit(1)
rules = data.get('result', [])
if not rules:
    print('No firewall rules found')
else:
    for r in rules:
        print(f\"{r['id']} | {r['action']} | {r['mode']} | {r.get('configuration', {}).get('value', 'N/A')} | {r.get('description', '')}\")
"
}

ban_ip() {
    local ip="$1"
    local duration="${2:-60}"

    if ! [[ "$duration" =~ ^[0-9]+$ ]] || [[ "$duration" -lt 1 ]] || [[ "$duration" -gt 1440 ]]; then
        echo "Error: duration must be between 1 and 1440 minutes" >&2
        exit 1
    fi

    local expires_at
    expires_at=$(date -u -d "+${duration} minutes" +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date -v+${duration}M -u +"%Y-%m-%dT%H:%M:%SZ")

    local response
    response=$(api_request POST "firewall/rules" "[{
        \"configuration\": {\"target\": \"ip\", \"value\": \"$ip\"},
        \"mode\": \"block\",
        \"description\": \"Banned until $expires_at\",
        \"products\": [\"firewall\"]
    }]")

    echo "$response" | python3 -c "
import sys, json
data = json.load(sys.stdin)
if not data.get('success'):
    print('API Error:', data.get('errors'), file=sys.stderr)
    sys.exit(1)
print(f\"Banned $ip until $expires_at\")
"
}

unban_ip() {
    local ip="$1"
    local response
    response=$(api_request GET "firewall/rules?per_page=100")
    local rule_id
    rule_id=$(echo "$response" | python3 -c "
import sys, json
data = json.load(sys.stdin)
if not data.get('success'):
    print('API Error:', data.get('errors'), file=sys.stderr)
    sys.exit(1)
rules = data.get('result', [])
for r in rules:
    if r.get('configuration', {}).get('value') == '$ip' and r.get('mode') == 'block':
        print(r['id'])
        break
")
    if [[ -z "$rule_id" ]]; then
        echo "No blocking rule found for $ip" >&2
        exit 1
    fi

    local del_response
    del_response=$(api_request DELETE "firewall/rules/$rule_id")
    echo "$del_response" | python3 -c "
import sys, json
data = json.load(sys.stdin)
if not data.get('success'):
    print('API Error:', data.get('errors'), file=sys.stderr)
    sys.exit(1)
print('Unbanned $ip')
"
}

COMMAND="${1:-}"
case "$COMMAND" in
    ban)
        [[ $# -ge 2 ]] || { echo "Error: ban requires <ip> [duration]" >&2; exit 1; }
        ban_ip "$2" "${3:-60}"
        ;;
    unban)
        [[ $# -ge 2 ]] || { echo "Error: unban requires <ip>" >&2; exit 1; }
        unban_ip "$2"
        ;;
    list)
        list_rules
        ;;
    help|--help|-h|"")
        usage
        ;;
    *)
        echo "Unknown command: $COMMAND" >&2
        usage
        exit 1
        ;;
esac
