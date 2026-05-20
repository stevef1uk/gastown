#!/usr/bin/env bash
#
# sync-planning-beads.sh — deterministic rig-flow planning scaffold (beads + plan.md)
#
# Wraps: gt rig sync-planning <rig>
#
# Creates one open implement bead per required_files path in workflow-profile.json
# and writes plan.md with real bead IDs (same as orchestrator pre_run sync_planning_artifacts).
#
# Usage:
#   GT_ROOT=~/gt ./scripts/sync-planning-beads.sh testgt3
#   ./scripts/sync-planning-beads.sh testgt3 --force
#
set -euo pipefail

GT_ROOT="${GT_ROOT:-$HOME/gt}"
RIG="${1:?usage: $0 <rig> [--force]}"
shift || true
FORCE=()
if [[ "${1:-}" == "--force" ]]; then
  FORCE=(--force)
  shift
fi
if [[ $# -gt 0 ]]; then
  echo "Unknown args: $*" >&2
  exit 1
fi

export GT_ROOT
exec gt rig sync-planning "$RIG" "${FORCE[@]}"
