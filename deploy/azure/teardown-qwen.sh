#!/usr/bin/env bash
# teardown-qwen.sh — Tear down the Azure GPU infrastructure created by setup-qwen.sh.
#
# Usage:
#   ./teardown-qwen.sh [--rg trading-floor-ai] [--yes]

set -euo pipefail

RESOURCE_GROUP="${AZURE_RG:-trading-floor-ai}"
ASSUME_YES="false"

while [[ $# -gt 0 ]]; do
    case $1 in
        --rg) RESOURCE_GROUP="$2"; shift 2 ;;
        --yes) ASSUME_YES="true"; shift ;;
        *) echo "Unknown arg: $1"; exit 1 ;;
    esac
done

echo "=== Trading Floor: Azure teardown ==="
echo "Resource Group: $RESOURCE_GROUP"

if [[ "$ASSUME_YES" != "true" ]]; then
    read -r -p "Delete resource group '$RESOURCE_GROUP'? [y/N] " confirm
    if [[ "${confirm:-}" != "y" && "${confirm:-}" != "Y" ]]; then
        echo "Aborted."
        exit 0
    fi
fi

az group delete \
    --name "$RESOURCE_GROUP" \
    --yes \
    --no-wait

echo "Delete request submitted for resource group '$RESOURCE_GROUP'."
