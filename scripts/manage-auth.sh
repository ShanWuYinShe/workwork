#!/bin/bash
set -e

HTPASSWD_FILE="/Users/liyou/Work/service/services/nginx/.htpasswd"

case "$1" in
  add-user)
    if [ -z "$2" ] || [ -z "$3" ]; then
      echo "Usage: $0 add-user <username> <password>"
      exit 1
    fi
    mkdir -p "$(dirname "$HTPASSWD_FILE")"
    if [ -f "$HTPASSWD_FILE" ]; then
      htpasswd -b "$HTPASSWD_FILE" "$2" "$3"
    else
      htpasswd -bc "$HTPASSWD_FILE" "$2" "$3"
    fi
    echo "User '$2' added/updated successfully."
    ;;
  remove-user)
    if [ -z "$2" ]; then
      echo "Usage: $0 remove-user <username>"
      exit 1
    fi
    if [ ! -f "$HTPASSWD_FILE" ]; then
      echo "Error: $HTPASSWD_FILE does not exist."
      exit 1
    fi
    htpasswd -D "$HTPASSWD_FILE" "$2"
    echo "User '$2' removed successfully."
    ;;
  list-users)
    if [ ! -f "$HTPASSWD_FILE" ]; then
      echo "No users found (htpasswd file does not exist)."
      exit 0
    fi
    cut -d: -f1 "$HTPASSWD_FILE"
    ;;
  help|--help|-h)
    echo "Usage: $0 <command> [arguments]"
    echo ""
    echo "Commands:"
    echo "  add-user <username> <password>   Add or update a user"
    echo "  remove-user <username>            Remove a user"
    echo "  list-users                        List all users"
    echo "  help                              Show this help message"
    ;;
  *)
    echo "Unknown command: $1"
    echo "Run '$0 help' for usage."
    exit 1
    ;;
esac
