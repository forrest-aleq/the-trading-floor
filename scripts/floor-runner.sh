#!/bin/zsh

set -euo pipefail

SCRIPT_DIR=${0:A:h}
REPO_ROOT=${SCRIPT_DIR:h}

export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"

cd "$REPO_ROOT"

mkdir -p "$REPO_ROOT/var/log" "$REPO_ROOT/var/audit"

export AUDIT_LOG_PATH="${AUDIT_LOG_PATH:-$REPO_ROOT/var/audit/audit.jsonl}"
export IBKR_CLIENT_ID_TRIES="${IBKR_CLIENT_ID_TRIES:-20}"

exec "$REPO_ROOT/bin/floor"
