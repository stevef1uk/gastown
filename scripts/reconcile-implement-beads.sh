#!/usr/bin/env bash
#
# reconcile-implement-beads.sh — deterministic disk vs beads audit for one rig
#
# - Lists missing/empty/stub required_files under layout_root
# - Lists closed implement beads whose files still need work
# - Reopens those beads (bd update --status=open) unless --dry-run
#
# Usage:
#   ./scripts/reconcile-implement-beads.sh
#   GT_ROOT=~/gt RIG=testgt3 ./scripts/reconcile-implement-beads.sh --dry-run
#
set -euo pipefail

GT_ROOT="${GT_ROOT:-$HOME/gt}"
RIG="${RIG:-${RIG_NAME:-testgt3}}"
DRY=()
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY=(--dry-run) ;;
    -h|--help)
      sed -n '2,18p' "$0"
      exit 0
      ;;
  esac
done

BEADS_DIR="${BEADS_DIR:-$GT_ROOT/$RIG/.beads}"
export BEADS_DIR

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
exec go run ./internal/cmd/reconcile-implement-beads/ -town "$GT_ROOT" -rig "$RIG" "${DRY[@]}"
