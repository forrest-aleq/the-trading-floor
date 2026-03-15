#!/bin/zsh

set -euo pipefail

LABEL="com.hnic.trading-floor"
UID_VALUE="$(id -u)"
RUNTIME_HOME="$HOME/Library/Application Support/${LABEL}"
PROGRAM_PATH="$RUNTIME_HOME/bin/floor"
LOG_OUT="$RUNTIME_HOME/var/log/floor.out"
LOG_ERR="$RUNTIME_HOME/var/log/floor.err"

echo "launchd label: $LABEL"
launchctl print "gui/${UID_VALUE}/${LABEL}" 2>/dev/null | sed -n '1,120p' || {
  echo "service not loaded"
  exit 1
}

echo
echo "processes:"
pgrep -af "$PROGRAM_PATH" || echo "no live floor process found"

for file in "$LOG_OUT" "$LOG_ERR"; do
  echo
  echo "tail $file"
  if [[ -f "$file" ]]; then
    tail -n 20 "$file"
  else
    echo "missing"
  fi
done
