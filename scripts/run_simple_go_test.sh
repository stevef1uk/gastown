#!/bin/bash
set -euo pipefail

# Parse parameter (Go or Python), default to Go
LANG_CHOICE="${1:-Go}"
LANG_CHOICE=$(echo "$LANG_CHOICE" | tr '[:upper:]' '[:lower:]')

# Find gastown root from script location (works on any machine)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GASTOWN_DIR="$(dirname "$SCRIPT_DIR")"
SPEC_DIR="$GASTOWN_DIR/internal/orchestrator/example_specs"

if [[ "$LANG_CHOICE" == "python" ]]; then
    SPEC_FILE="$SPEC_DIR/SPEC_python.md"
else
    SPEC_FILE="$SPEC_DIR/SPEC_go.md"
    LANG_CHOICE="go"
fi

if [[ ! -f "$SPEC_FILE" ]]; then
    echo "Error: Could not find spec file at $SPEC_FILE"
    exit 1
fi

GT_ROOT="${GT_ROOT:-$HOME/gt}"
REPO_DIR="/tmp/ping_repo"
RIG_NAME="ping_rig"

echo "=== 1. Creating clean local repository for $LANG_CHOICE ==="
rm -rf "$REPO_DIR"
mkdir -p "$REPO_DIR"
cd "$REPO_DIR"
git init -b main

cp "$SPEC_FILE" SPEC.md
git add SPEC.md
git commit -m "Initial ping app spec ($LANG_CHOICE)"

echo "=== 2. Resetting town and rig using reset-town-and-rig.sh ==="
cd "$GASTOWN_DIR"
export RIG_NAME="$RIG_NAME"
export RIG_URL="file://$REPO_DIR"
export DOCTOR_RESTART_SESSIONS=1
export START_RIG_FLOW=1

./scripts/reset-town-and-rig.sh

echo "=== Done! ==="
