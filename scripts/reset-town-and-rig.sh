#!/usr/bin/env bash
#
# reset-town-and-rig.sh — gt down, nuclear clean-gastown, drain mail, gt up, gt rig add
#
# Usage:
#   ./scripts/reset-town-and-rig.sh
#   GT_ROOT=~/gt GASTOWN=~/dev/freeride/gastown RIG=testgt2 RIG_URL=https://github.com/you/repo ./scripts/reset-town-and-rig.sh
#
# Optional:
#   DOCTOR_RESTART_SESSIONS=1  — pass --restart-sessions to gt doctor --fix
#                                (helps stale patrol tmux after reset)
#
# After a nuclear reset, `gt doctor --fix` sometimes needs Dolt to settle
# before rig DB issue_prefix rows exist; this script runs two fix passes.
# If you still see rig-config-sync / database-prefix warnings, run
# `cd "$GT_ROOT" && gt doctor --fix` once more by hand when Dolt is healthy.
# (Doctor no longer runs `bd init --force` when a .beads directory already
# exists — that used to wipe agent and rig identity beads.)
# patrol-not-stuck is not auto-fixed — review wisps or restart sessions
# (DOCTOR_RESTART_SESSIONS=1).
#
# Requires: gt on PATH (typically ~/.local/bin), bash, clean-gastown.sh deps (python3, etc.)
#
set -euo pipefail

GT_ROOT="${GT_ROOT:-$HOME/gt}"
# Directory containing this repo (parent of scripts/)
GASTOWN="${GASTOWN:-$(cd "$(dirname "$0")/.." && pwd)}"
CLEAN_SCRIPT="${GASTOWN}/scripts/clean-gastown.sh"
RIG="${RIG_NAME:-testgt2}"
RIG_URL="${RIG_URL:-https://github.com/stevef1uk/testgt2}"

if [[ ! -f "$CLEAN_SCRIPT" ]]; then
  echo "FATAL: clean script not found: $CLEAN_SCRIPT" >&2
  exit 1
fi
if [[ ! -f "$GT_ROOT/config.json" ]]; then
  echo "FATAL: not a Gas Town root (no config.json): $GT_ROOT" >&2
  exit 1
fi

export GT_ROOT

drain_hq_mail() {
  cd "$GT_ROOT"
  echo "[drain] HQ inboxes (mayor, planner, deacon, mechanic)..."
  for addr in mayor/ planner/ deacon/ mechanic/; do
    gt mail clear "$addr" 2>/dev/null || true
  done
}

drain_rig_mail() {
  local r="$1"
  cd "$GT_ROOT"
  if [[ ! -d "$GT_ROOT/$r" ]]; then
    return 0
  fi
  echo "[drain] Rig '$r' agent inboxes..."
  for role in architect qa refinery witness; do
    gt mail clear "$r/$role" 2>/dev/null || true
  done
  if [[ -d "$GT_ROOT/$r/polecats" ]]; then
    for pc in "$GT_ROOT/$r/polecats"/*/; do
      [[ -d "$pc" ]] || continue
      gt mail clear "$r/polecats/$(basename "$pc")" 2>/dev/null || true
    done
  fi
  if [[ -d "$GT_ROOT/$r/crew" ]]; then
    for cr in "$GT_ROOT/$r/crew"/*/; do
      [[ -d "$cr" ]] || continue
      gt mail clear "$r/crew/$(basename "$cr")" 2>/dev/null || true
    done
  fi
}

echo "=== gt down ==="
(cd "$GT_ROOT" && gt down) || true

echo "=== clean-gastown (nuclear, --force) ==="
bash "$CLEAN_SCRIPT" --force "$GT_ROOT"

echo "=== gt up ==="
(cd "$GT_ROOT" && gt up)

# Fresh HQ DB may still get noise from seeds/plugins; drain before rig add.
drain_hq_mail

echo "=== gt rig add $RIG ==="
(cd "$GT_ROOT" && gt rig add "$RIG" "$RIG_URL")

drain_hq_mail
drain_rig_mail "$RIG"

echo "=== gt up (pick up new rig agents) ==="
(cd "$GT_ROOT" && gt up)

drain_hq_mail
drain_rig_mail "$RIG"

# Give Dolt/bd a moment after agents start so doctor fixes can touch rig DBs.
echo "=== gt doctor --fix (settle + two passes) ==="
sleep 3
doc_flags=(--fix)
if [[ "${DOCTOR_RESTART_SESSIONS:-0}" == "1" ]]; then
  doc_flags+=(--restart-sessions)
fi
(cd "$GT_ROOT" && gt doctor "${doc_flags[@]}") || true
sleep 3
(cd "$GT_ROOT" && gt doctor "${doc_flags[@]}") || true

echo "=== gt doctor (read-only summary) ==="
(cd "$GT_ROOT" && gt doctor) || true

echo "=== done ==="
echo "Town: $GT_ROOT"
echo "Rig:  $RIG  ($RIG_URL)"
echo "Next: single kickoff mail to mayor/, then gt nudge mayor \"...\""
