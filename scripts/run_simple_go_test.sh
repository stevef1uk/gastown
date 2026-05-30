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

# Canonical absolute path + file URL (Mac /tmp → /private/tmp; git needs file:///…)
resolve_repo_dir() {
  rm -rf "$REPO_DIR"
  mkdir -p "$REPO_DIR"
  (cd "$REPO_DIR" && pwd -P)
}

echo "=== 1. Creating clean local repository for $LANG_CHOICE ==="
REPO_DIR="$(resolve_repo_dir)"
cd "$REPO_DIR"
git init -b main

cp "$SPEC_FILE" SPEC.md
git add SPEC.md
git commit -m "Initial ping app spec ($LANG_CHOICE)"

RIG_URL="file://${REPO_DIR}"
echo "  Spec repo: $REPO_DIR"
echo "  Rig URL:   $RIG_URL"

echo "=== 2. Resetting town and rig using reset-town-and-rig.sh ==="
cd "$GASTOWN_DIR"
export GT_ROOT
export RIG_NAME="$RIG_NAME"
export RIG_URL
export DOCTOR_RESTART_SESSIONS=1
export START_RIG_FLOW=1

bash "$GASTOWN_DIR/scripts/reset-town-and-rig.sh"

echo "=== 3. Verify ==="
cd "$GT_ROOT"
if [[ ! -d "$GT_ROOT/$RIG_NAME" ]]; then
  echo "ERROR: rig directory missing: $GT_ROOT/$RIG_NAME" >&2
  echo "  reset likely stopped before 'gt rig add' (check Docker for 'gt up', or re-run with:" >&2
  echo "    cd $GT_ROOT && gt up && gt rig add $RIG_NAME '$RIG_URL'" >&2
  exit 1
fi
if ! gt rig list 2>/dev/null | grep -q "$RIG_NAME"; then
  echo "WARN: $GT_ROOT/$RIG_NAME exists but 'gt rig list' does not show $RIG_NAME" >&2
fi

echo "=== Done! ==="
echo "  Town: $GT_ROOT"
echo "  Rig:  $RIG_NAME"
echo "  From town root: cd $GT_ROOT && gt rig list && gt mayor workflow status"
