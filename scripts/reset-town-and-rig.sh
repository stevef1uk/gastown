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
#   START_RIG_FLOW=1         — after reset, wait for orchestrator MCP, then:
#                                gt mayor workflow start rig-flow --rig "$RIG"
#   GT_UP_FLAGS=--orchestrator-only  — passed to each gt up (default when START_RIG_FLOW=1)
#   ORCHESTRATOR_WAIT_SECS=90 — max wait for NATS + orchestrator MCP before workflow start
#   PLANNING_RESYNC_WAIT_SECS=180 — after rig-flow start, wait for planning state then
#                                profile-only sync-planning (see finalize_rig_planning_state)
#   SKIP_ORCHESTRATOR_SYNC=1 — do not refresh $GT_ROOT/orchestrator/ from gastown
#   RESET_ORCHESTRATOR_INSTANCES=0 — keep orchestrator/instances.json (default: clear it)
#   SKIP_PLANNING_VERIFY=1   — do not fail if legacy implement beads remain (not recommended)
#
# After rig add this script runs `gt rig sync-planning --force` when workflow-profile.json
# exists (from SPEC.md spec-index) and verifies no legacy "Implement <layout>/..." beads remain.
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
RIG="${RIG_NAME:-testgt4}"
RIG_URL="${RIG_URL:-https://github.com/stevef1uk/testgt4}"
ORCHESTRATOR_WAIT_SECS="${ORCHESTRATOR_WAIT_SECS:-90}"
PLANNING_RESYNC_WAIT_SECS="${PLANNING_RESYNC_WAIT_SECS:-180}"
# rig-flow is orchestrator-driven; --orchestrator-only skips legacy town hq architect/qa/polecat.
if [[ "${START_RIG_FLOW:-0}" == "1" && -z "${GT_UP_FLAGS:-}" ]]; then
  GT_UP_FLAGS="--orchestrator-only"
fi
GT_UP_FLAGS="${GT_UP_FLAGS:-}"

gt_up() {
  # shellcheck disable=SC2086
  (cd "$GT_ROOT" && gt up $GT_UP_FLAGS "$@")
}

# gt mayor workflow start uses NATS → orchestrator MCP; it does not wait for agent sessions.
wait_for_orchestrator_mcp() {
  local deadline=$((SECONDS + ORCHESTRATOR_WAIT_SECS))
  local n=0
  echo "[orchestrator] waiting for NATS + orchestrator MCP (up to ${ORCHESTRATOR_WAIT_SECS}s)..."
  while (( SECONDS < deadline )); do
    n=$((n + 1))
    if (cd "$GT_ROOT" && gt orchestrator status 2>&1) | grep -Fq 'MCP ping OK'; then
      echo "[orchestrator] MCP ready"
      return 0
    fi
    if (( n == 1 || n % 5 == 0 )); then
      gt_up >/dev/null 2>&1 || true
    fi
    sleep 2
  done
  echo "FATAL: orchestrator MCP not ready — is Docker/NATS up? Try:" >&2
  echo "  cd $GT_ROOT && gt up $GT_UP_FLAGS && gt orchestrator status" >&2
  return 1
}

if [[ ! -f "$CLEAN_SCRIPT" ]]; then
  echo "FATAL: clean script not found: $CLEAN_SCRIPT" >&2
  exit 1
fi
if [[ ! -f "$GT_ROOT/settings/config.json" ]]; then
  echo "=== gt install (no town at $GT_ROOT yet) ==="
  if ! command -v gt &>/dev/null; then
    echo "FATAL: gt not on PATH — run 'make install' in gastown first" >&2
    exit 1
  fi
  gt install "$GT_ROOT" --git --dolt-port ${DOLT_PORT:-3307}
fi
if [[ ! -f "$GT_ROOT/settings/config.json" ]]; then
  echo "FATAL: not a Gas Town root (no settings/config.json): $GT_ROOT" >&2
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

# Remove polecat/agent junk from a prior bad run (npm/jest placeholders, wrong-path venv).
# SPEC and rig git history come from the remote on gt rig add; this clears rig-root cruft.
clean_rig_pipeline_artifacts() {
  local r="$1"
  local rig_dir="$GT_ROOT/$r/mayor/rig"
  [[ -d "$rig_dir" ]] || return 0
  echo "[clean] rig pipeline artifacts under $r/mayor/rig/..."
  if [[ -d "$rig_dir/backend" ]]; then
    rm -rf "$rig_dir/backend"
    echo "  removed backend/ (polecat recreates during implementation)"
  fi
  for d in node_modules env .venv venv tests polecat; do
    if [[ -d "$rig_dir/$d" ]]; then
      rm -rf "$rig_dir/$d"
      echo "  removed $d/"
    fi
  done
  # Prior implementation runs (flat layout, wrong module root, stale index).
  for d in linkshelf frontend backend; do
    if [[ -d "$rig_dir/$d" ]]; then
      rm -rf "$rig_dir/$d"
      echo "  removed stale $d/"
    fi
  done
  for f in fizzbuzz.py main.py test_fizzbuzz.py dummy.py plan_complete.js \
    package.json package-lock.json test-execution-command tests_skipped.txt \
    spec_file_path.txt run-tests run-tests.sh run-tests.sh_backup \
    go.mod go.sum codeindex.json implementation-progress.json plan.md architecture.md; do
    if [[ -f "$rig_dir/$f" ]]; then
      rm -f "$rig_dir/$f"
      echo "  removed stale $f (recreated by rig-flow / spec clone)"
    fi
  done
}

# Rig identity bead must exist and must not be status:docked — otherwise the daemon
# SIGTERM-kills te-*-polecat (isRigOperational fail-safe). gt doctor --fix used to
# create the bead with status:docked; we undock and verify here after every doctor pass.
ensure_rig_identity_operational() {
  local r="$1"
  echo "[rig] ensure identity bead operational for $r..."
  if ! (cd "$GT_ROOT" && gt rig undock "$r" 2>/dev/null); then
    true
  fi
  local rig_work="$GT_ROOT/$r/mayor/rig"
  if [[ ! -e "$rig_work/.beads" ]] && [[ ! -d "$rig_work/.beads" ]]; then
    rig_work="$GT_ROOT/$r"
  fi
  local prefix rig_bead
  prefix="$(python3 - "$GT_ROOT" "$r" <<'PY'
import json, sys
town, rig = sys.argv[1], sys.argv[2]
data = json.load(open(f"{town}/mayor/rigs.json"))
print(data["rigs"][rig]["beads"]["prefix"])
PY
)"
  rig_bead="${prefix}-rig-${r}"
  if ! (cd "$rig_work" && bd show "$rig_bead" >/dev/null 2>&1); then
    echo "FATAL: rig identity bead $rig_bead missing — run: cd $GT_ROOT && gt doctor --fix" >&2
    return 1
  fi
  if (cd "$rig_work" && bd label list "$rig_bead" 2>/dev/null | grep -q 'status:docked'); then
    echo "FATAL: $rig_bead still has status:docked after gt rig undock" >&2
    (cd "$rig_work" && bd label list "$rig_bead") >&2 || true
    return 1
  fi
  echo "[rig] OK — $rig_bead exists and is not docked"
}

# After rig-flow reaches planning, re-sync from workflow-profile.json (not SPEC layout enrich).
# Planning pre_run uses EnrichWorkflowValidationFromArchitecture, which can replace profile
# required_files with flat paths from SPEC ## Layout; this recenters beads before the planner runs.
wait_and_resync_at_planning() {
  local r="$1"
  local inst="$GT_ROOT/orchestrator/instances.json"
  [[ -f "$inst" ]] || return 0
  local deadline=$((SECONDS + PLANNING_RESYNC_WAIT_SECS))
  echo "[planning] waiting up to ${PLANNING_RESYNC_WAIT_SECS}s for rig-flow planning on $r..."
  while (( SECONDS < deadline )); do
    local st
    st="$(python3 - "$inst" "$r" <<'PY'
import json, sys
path, rig = sys.argv[1], sys.argv[2]
data = json.load(open(path))
for inst in data.get("instances", []):
    vars = inst.get("variables") or {}
    if vars.get("rig") != rig:
        continue
    if inst.get("template_id") != "rig-flow":
        continue
    if inst.get("status") in ("completed", "failed", "cancelled"):
        continue
    print(inst.get("current_state") or "")
    break
PY
)"
    case "$st" in
      planning|plan_review|project_setup|implementation)
        echo "[planning] rig-flow state=$st — profile sync after SPEC enrich"
        finalize_rig_planning_state "$r"
        return 0
        ;;
    esac
    sleep 5
  done
  echo "[planning] no planning state within ${PLANNING_RESYNC_WAIT_SECS}s (design may still be running)"
  echo "  When planning starts, run: gt rig sync-planning $r --force"
}

# Canonicalize implement beads + plan.md when workflow-profile exists; verify clean bead set.
finalize_rig_planning_state() {
  local r="$1"
  local rig_dir="$GT_ROOT/$r/mayor/rig"
  local profile="$rig_dir/.gastown/workflow-profile.json"
  if [[ ! -f "$profile" ]]; then
    echo "[planning] no workflow-profile.json yet — beads/plan sync skipped (spec-index runs on rig add when SPEC.md exists)"
    return 0
  fi
  echo "=== gt rig sync-planning $r --force ==="
  if ! (cd "$GT_ROOT" && gt rig sync-planning "$r" --force); then
    echo "FATAL: gt rig sync-planning failed — rebuild gt from gastown and retry" >&2
    exit 1
  fi
  if [[ "${SKIP_PLANNING_VERIFY:-0}" == "1" ]]; then
    echo "[planning] verify skipped (SKIP_PLANNING_VERIFY=1)"
    return 0
  fi
  local beads_dir="$GT_ROOT/$r/.beads"
  # Flat layout titles only (canonical beads are Implement linkshelf/internal/... or cmd/... or web/...).
  local legacy
  legacy="$(BEADS_DIR="$beads_dir" bd list --status=open --flat --limit=0 2>/dev/null \
    | grep -E ' - Implement linkshelf/(main|handlers|store|schema|store_test)\.go per' || true)"
  if [[ -n "$legacy" ]]; then
    echo "FATAL: flat-layout implement beads remain after sync-planning:" >&2
    echo "$legacy" >&2
    echo "  Run: gt rig sync-planning $r --force" >&2
    exit 1
  fi
  if [[ -f "$rig_dir/plan.md" ]]; then
    local flat
    flat="$(grep -En '^### [^:]+: linkshelf/(main|handlers|store|schema|store_test)\.go' "$rig_dir/plan.md" 2>/dev/null || true)"
    if [[ -n "$flat" ]]; then
      echo "FATAL: plan.md still has flat layout paths (expected linkshelf/internal/..., cmd/server/...):" >&2
      echo "$flat" >&2
      exit 1
    fi
  fi
  echo "[planning] verify OK — no legacy Implement-prefix open beads; plan.md paths look canonical"
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

echo "=== gt up ${GT_UP_FLAGS:+$GT_UP_FLAGS} ==="
if ! gt_up; then
  echo "FATAL: gt up failed — is Docker running? (NATS). Fix and run:" >&2
  echo "  cd $GT_ROOT && gt up && gt rig add $RIG '$RIG_URL'" >&2
  exit 1
fi
normalize_town_agent_settings
sync_orchestrator_assets

drain_hq_mail

echo "=== gt rig add $RIG ==="
if ! (cd "$GT_ROOT" && gt rig add "$RIG" "$RIG_URL"); then
  echo "FATAL: gt rig add failed for $RIG ($RIG_URL)" >&2
  echo "  Ensure spec repo exists: ls -la ${RIG_URL#file://}" >&2
  exit 1
fi

# spec-index extracts workflow-profile.json from SPEC.md. It requires SPEC.md to
# exist — req-flow rigs only have REQUIREMENTS.md (the analyst writes SPEC.md later
# in the pipeline, at which point spec-index runs automatically). gt rig add above
# already ran spec-index when SPEC.md was present, so skip here otherwise.
if [[ -f "$GT_ROOT/$RIG/mayor/rig/SPEC.md" ]]; then
  echo "=== gt rig spec-index $RIG --force ==="
  if ! (cd "$GT_ROOT" && gt rig spec-index "$RIG" --force); then
    echo "FATAL: gt rig spec-index failed for $RIG" >&2
    exit 1
  fi
else
  echo "=== gt rig spec-index $RIG (skipped: no SPEC.md — req-flow writes it later) ==="
fi


clean_rig_pipeline_artifacts "$RIG"
finalize_rig_planning_state "$RIG"
drain_hq_mail
drain_rig_mail "$RIG"

echo "=== gt up (pick up new rig agents) ${GT_UP_FLAGS:+$GT_UP_FLAGS} ==="
gt_up

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
ensure_rig_identity_operational "$RIG"

echo "=== gt doctor (read-only summary) ==="
(cd "$GT_ROOT" && gt doctor) || true

if [[ "${START_RIG_FLOW:-0}" == "1" ]]; then
  wait_for_orchestrator_mcp
  echo "=== gt mayor workflow start rig-flow ==="
  if ! (cd "$GT_ROOT" && gt mayor workflow start rig-flow --rig "$RIG"); then
    echo "FATAL: gt mayor workflow start failed (orchestrator MCP or duplicate workflow)" >&2
    exit 1
  fi
  echo "[orchestrator] workflow started — ensuring rig pipeline sessions..."
  ensure_rig_identity_operational "$RIG"
  finalize_rig_planning_state "$RIG"
  wait_and_resync_at_planning "$RIG"
  gt_up || true
  ensure_rig_identity_operational "$RIG"
  if [[ -f "$GT_ROOT/orchestrator/instances.json" ]]; then
    echo "[orchestrator] instances.json:"
    grep -E '"id"|current_state|status' "$GT_ROOT/orchestrator/instances.json" || true
  else
    echo "FATAL: workflow start succeeded but $GT_ROOT/orchestrator/instances.json missing" >&2
    exit 1
  fi
  echo "[orchestrator] verify agents: cd $GT_ROOT && gt status -v"
  echo "[orchestrator] note: planner may recreate legacy Implement-* beads during planning;"
  echo "  after planning: gt rig sync-planning $RIG --force"
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
