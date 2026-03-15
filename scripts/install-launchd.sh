#!/bin/zsh

set -euo pipefail

SCRIPT_DIR=${0:A:h}
REPO_ROOT=${SCRIPT_DIR:h}
LABEL="com.hnic.trading-floor"
PLIST_TEMPLATE="$REPO_ROOT/deploy/launchd/${LABEL}.plist.template"
LAUNCH_AGENTS_DIR="$HOME/Library/LaunchAgents"
PLIST_PATH="$LAUNCH_AGENTS_DIR/${LABEL}.plist"
RUNTIME_HOME="$HOME/Library/Application Support/${LABEL}"
PROGRAM_PATH="$RUNTIME_HOME/bin/floor"
WORKDIR="$RUNTIME_HOME"
LOG_OUT="$RUNTIME_HOME/var/log/floor.out"
LOG_ERR="$RUNTIME_HOME/var/log/floor.err"
AUDIT_LOG_PATH="$RUNTIME_HOME/var/audit/audit.jsonl"
RUNTIME_ENV="$RUNTIME_HOME/.env"
RUNTIME_MIGRATIONS_DIR="$RUNTIME_HOME/store/migrations"

mkdir -p "$LAUNCH_AGENTS_DIR" "$RUNTIME_HOME/bin" "$RUNTIME_HOME/var/log" "$RUNTIME_HOME/var/audit" "$RUNTIME_MIGRATIONS_DIR"

cd "$REPO_ROOT"
go build -o "$PROGRAM_PATH" ./cmd/floor
cp "$REPO_ROOT"/store/migrations/*.sql "$RUNTIME_MIGRATIONS_DIR"/

if [[ -f "$REPO_ROOT/.env" ]]; then
  cp "$REPO_ROOT/.env" "$RUNTIME_ENV"
elif [[ -f "$REPO_ROOT/.env.example" && ! -f "$RUNTIME_ENV" ]]; then
  cp "$REPO_ROOT/.env.example" "$RUNTIME_ENV"
fi

export PROGRAM_PATH WORKDIR LOG_OUT LOG_ERR AUDIT_LOG_PATH
perl -0pe 's#__PROGRAM_PATH__#$ENV{PROGRAM_PATH}#g; s#__WORKDIR__#$ENV{WORKDIR}#g; s#__LOG_OUT__#$ENV{LOG_OUT}#g; s#__LOG_ERR__#$ENV{LOG_ERR}#g; s#__AUDIT_LOG_PATH__#$ENV{AUDIT_LOG_PATH}#g' \
  "$PLIST_TEMPLATE" > "$PLIST_PATH"

chmod 755 "$PROGRAM_PATH"
chmod 644 "$PLIST_PATH"

launchctl bootout "gui/$(id -u)" "$PLIST_PATH" >/dev/null 2>&1 || true
launchctl bootstrap "gui/$(id -u)" "$PLIST_PATH"
launchctl kickstart -k "gui/$(id -u)/$LABEL"

echo "installed $LABEL"
echo "plist: $PLIST_PATH"
echo "runtime home: $RUNTIME_HOME"
echo "stdout: $LOG_OUT"
echo "stderr: $LOG_ERR"
