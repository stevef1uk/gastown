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
#   START_RIG_FLOW=1         — after reset, run:
#                                gt mayor workflow start rig-flow --rig "$RIG"
#   SKIP_ORCHESTRATOR_SYNC=1 — do not refresh $GT_ROOT/orchestrator/ from gastown
#   RESET_ORCHESTRATOR_INSTANCES=0 — keep orchestrator/instances.json (default: clear it)
#
# Orchestrator workflow state (wf-*, design/planning, …) is persisted in
# $GT_ROOT/orchestrator/instances.json (removed when RESET_ORCHESTRATOR_INSTANCES=1).
# Templates/prompts under $GT_ROOT/orchestrator/ are refreshed from gastown
# (source: internal/orchestrator/town/). For a single-rig reset without nuclear clean,
# use scripts/reset-rig-orchestrator.sh.
#
# After a nuclear reset, `gt doctor --fix` sometimes needs Dolt to settle
# before rig DB issue_prefix rows exist; this script runs two fix passes.
#
# Requires: gt on PATH (typically ~/.local/bin), bash, clean-gastown.sh deps (python3, etc.)
#
set -euo pipefail

if [[ -z "${BASH_VERSION:-}" ]]; then
  echo "FATAL: run with bash, not sh: bash $0 $*" >&2
  exit 1
fi

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

# Remove polecat implementation cruft from a prior bad architect/planner run.
# SPEC and rig git history come from the remote on gt rig add; this only
# clears obvious stale artifacts under mayor/rig/.
clean_rig_pipeline_artifacts() {
  local r="$1"
  local rig_dir="$GT_ROOT/$r/mayor/rig"
  [[ -d "$rig_dir" ]] || return 0
  echo "[clean] rig pipeline artifacts under $r/mayor/rig/..."
  if [[ -d "$rig_dir/backend" ]]; then
    rm -rf "$rig_dir/backend"
    echo "  removed backend/ (polecat recreates during implementation)"
  fi
  for f in fizzbuzz.py main.py test_fizzbuzz.py dummy.py plan_complete.js; do
    if [[ -f "$rig_dir/$f" ]]; then
      rm -f "$rig_dir/$f"
      echo "  removed stray $f"
    fi
  done
}

# Install orchestrator FSM templates + prompts from gastown into the town.
sync_orchestrator_assets() {
  if [[ "${SKIP_ORCHESTRATOR_SYNC:-0}" == "1" ]]; then
    echo "[orchestrator] sync skipped (SKIP_ORCHESTRATOR_SYNC=1)"
    return 0
  fi
  echo "=== sync orchestrator assets (gastown → $GT_ROOT/orchestrator) ==="
  if (cd "$GT_ROOT" && gt orchestrator sync --update-changed 2>/dev/null); then
    return 0
  fi
  local orch_src="$GASTOWN/internal/orchestrator/town"
  if [[ ! -d "$orch_src" ]]; then
    echo "[orchestrator] warn: no embedded town assets in $orch_src" >&2
    echo "[orchestrator] run 'make install' in gastown after building gt with orchestrator sync" >&2
    return 0
  fi
  local added=0 updated=0
  local file_list
  file_list="$(mktemp)"
  find "$orch_src" -type f | sort >"$file_list"
  while IFS= read -r srcfile; do
    [[ -n "$srcfile" ]] || continue
    rel="${srcfile#"$orch_src/"}"
    dst="$GT_ROOT/orchestrator/$rel"
    mkdir -p "$(dirname "$dst")"
    if [[ ! -f "$dst" ]]; then
      cp "$srcfile" "$dst"
      added=$((added + 1))
    elif ! cmp -s "$srcfile" "$dst"; then
      cp "$srcfile" "$dst"
      updated=$((updated + 1))
    fi
  done <"$file_list"
  rm -f "$file_list"
  echo "[orchestrator] synced from source tree ($added added, $updated updated)"
}

normalize_town_agent_settings() {
  local cfg="$GT_ROOT/settings/config.json"
  [[ -f "$cfg" ]] || return 0
  echo "[config] normalize town agent settings..."
  python3 - "$cfg" <<'PY'
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
data = json.loads(path.read_text())

agents = data.get("agents", {}) or {}
role_agents = data.get("role_agents", {}) or {}
default_agent = data.get("default_agent", "")

builtins = {"claude", "codex", "gemini", "cursor", "auggie", "amp", "opencode", "copilot"}
valid = set(builtins) | set(agents.keys())

preferred_default = "gt-agent-local" if "gt-agent-local" in valid else "claude"
if (not isinstance(default_agent, str)) or ("/" in default_agent) or (default_agent not in valid):
    data["default_agent"] = preferred_default

for role in ("planner", "mechanic", "mayor"):
    current = role_agents.get(role)
    if (current is None) or (not isinstance(current, str)) or (current not in valid) or (current == "gt-agent-powerful"):
        role_agents[role] = preferred_default

if "gt-agent-nvidia" in valid:
    current = role_agents.get("polecat")
    if (current is None) or (not isinstance(current, str)) or (current not in valid):
        role_agents["polecat"] = "gt-agent-nvidia"

data["role_agents"] = role_agents

path.write_text(json.dumps(data, indent=2) + "\n")
PY
}

reset_orchestrator_instances() {
  if [[ "${RESET_ORCHESTRATOR_INSTANCES:-1}" != "1" ]]; then
    echo "[orchestrator] keeping instances.json (RESET_ORCHESTRATOR_INSTANCES=0)"
    return 0
  fi
  local inst="$GT_ROOT/orchestrator/instances.json"
  if [[ -f "$inst" ]]; then
    rm -f "$inst"
    echo "[orchestrator] removed $inst (stale workflow state)"
  fi
}

echo "=== gt down ==="
(cd "$GT_ROOT" && gt down) || true
reset_orchestrator_instances

echo "=== clean-gastown (nuclear, --force) ==="
bash "$CLEAN_SCRIPT" --force "$GT_ROOT"

echo "=== gt up ==="
(cd "$GT_ROOT" && gt up)
normalize_town_agent_settings
sync_orchestrator_assets

drain_hq_mail

echo "=== gt rig add $RIG ==="
(cd "$GT_ROOT" && gt rig add "$RIG" "$RIG_URL")

clean_rig_pipeline_artifacts "$RIG"
drain_hq_mail
drain_rig_mail "$RIG"

echo "=== gt up (pick up new rig agents) ==="
(cd "$GT_ROOT" && gt up)

sync_orchestrator_assets
drain_hq_mail
drain_rig_mail "$RIG"

echo "=== gt doctor --fix (settle + two passes) ==="
sleep 3
doc_flags="--fix"
if [[ "${DOCTOR_RESTART_SESSIONS:-0}" == "1" ]]; then
  doc_flags="$doc_flags --restart-sessions"
fi
# shellcheck disable=SC2086
(cd "$GT_ROOT" && gt doctor $doc_flags) || true
sleep 3
# shellcheck disable=SC2086
(cd "$GT_ROOT" && gt doctor $doc_flags) || true

echo "=== gt doctor (read-only summary) ==="
(cd "$GT_ROOT" && gt doctor) || true

if [[ "${START_RIG_FLOW:-0}" == "1" ]]; then
  echo "=== gt mayor workflow start rig-flow ==="
  if (cd "$GT_ROOT" && gt mayor workflow start rig-flow --rig "$RIG"); then
    echo "[orchestrator] workflow started — tail logs/orchestrator.log and */typescript"
  else
    echo "[orchestrator] workflow start failed (is orchestrator running? gt orchestrator status)" >&2
  fi
fi

echo "=== done ==="
echo "Town: $GT_ROOT"
echo "Rig:  $RIG  ($RIG_URL)"
echo "Orchestrator templates: $GT_ROOT/orchestrator/ (source: $GASTOWN/internal/orchestrator/town/)"
if [[ "${START_RIG_FLOW:-0}" == "1" ]]; then
  echo "Next: tail -f $GT_ROOT/logs/orchestrator.log $GT_ROOT/planner/typescript"
else
  echo "Next (orchestrator path):"
  echo "  START_RIG_FLOW=1 $0"
  echo "  # or: cd $GT_ROOT && gt mayor workflow start rig-flow --rig $RIG"
fi
