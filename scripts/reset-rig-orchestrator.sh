#!/usr/bin/env bash
#
# reset-rig-orchestrator.sh — reset ONE rig for clean rig-flow debug (no nuclear town clean)
#
# Usage:
#   ./scripts/reset-rig-orchestrator.sh
#   ./scripts/reset-rig-orchestrator.sh --force
#   GT_ROOT=~/gt RIG=finally ./scripts/reset-rig-orchestrator.sh
#   START_RIG_FLOW=1 ./scripts/reset-rig-orchestrator.sh --force
#
# Environment (defaults shown):
#   GT_ROOT=~/gt              Gas Town town root
#   GASTOWN=<repo>            gastown repo (parent of scripts/)
#   RIG=testgt2               rig to reset (RIG_NAME also accepted)
#   GT_DOWN=1                 run gt down before reset
#   GT_UP=1                   run gt up after reset
#   RESET_ORCHESTRATOR_INSTANCES=1  clear workflow state in instances.json
#                                   (lighter: gt mayor workflow reset wf-N --to design)
#   RESET_IMPL_BEADS=1        delete rig beads whose title contains "Implement backend"
#   KEEP_ROLE_BEADS=1         skip te-<rig>-architect/qa/refinery/witness and patrol beads
#   CLEAR_ALL_RIG_BEADS=0     if 1, delete all rig beads (including role/patrol)
#   CLEAR_TOWN_IMPL_BEADS=0   if 1, also delete hq-* "Implement backend" in $GT_ROOT/.beads
#   RESET_GIT_WORKTREE=1      git clean/checkout backend/ under mayor/rig
#   SKIP_ORCHESTRATOR_SYNC=0  set 1 to skip sync_orchestrator_assets
#   START_RIG_FLOW=0          if 1, start one rig-flow workflow after reset
#
# After reset, workflow-profile.json is rewritten via gt rig normalize-profile
# (Dockerfile/compose belong in the *last* delivery phase). Do not manually
# set-phase to an old infra phase expecting docker beads there.
#
# Examples:
#   # Rewind FSM only (keep wf-* id) after deleting architecture.md / plan.md:
#   gt mayor workflow reset wf-1 --to design
#
#   # Interactive reset for testgt2, then inspect:
#   ./scripts/reset-rig-orchestrator.sh
#
#   # Non-interactive + fresh workflow:
#   START_RIG_FLOW=1 ./scripts/reset-rig-orchestrator.sh --force
#
#   # Keep orchestrator instances for other rigs, only clear this rig's wf-*:
#   RESET_ORCHESTRATOR_INSTANCES=1 RIG=myrig ./scripts/reset-rig-orchestrator.sh --force
#
#   # Also purge HQ implementation beads:
#   CLEAR_TOWN_IMPL_BEADS=1 ./scripts/reset-rig-orchestrator.sh --force
#
# Requires: gt, python3, bd (beads), bash. Dolt must be reachable for bd delete.
#
set -euo pipefail

FORCE=false
for arg in "$@"; do
  case "$arg" in
    -f|--force) FORCE=true ;;
    -h|--help)
      sed -n '2,/^set -euo pipefail$/p' "$0" | head -n -1
      exit 0
      ;;
  esac
done

GT_ROOT="${GT_ROOT:-$HOME/gt}"
GASTOWN="${GASTOWN:-$(cd "$(dirname "$0")/.." && pwd)}"
RIG="${RIG:-${RIG_NAME:-finally}}"

export GT_ROOT GASTOWN RIG

if [[ ! -f "$GT_ROOT/settings/config.json" ]]; then
  echo "FATAL: not a Gas Town root (no settings/config.json): $GT_ROOT" >&2
  exit 1
fi
if [[ ! -d "$GT_ROOT/$RIG" ]]; then
  echo "FATAL: rig directory missing: $GT_ROOT/$RIG" >&2
  exit 1
fi

if [[ "$FORCE" != "true" ]]; then
  echo "This will reset rig-flow state for rig '$RIG' under $GT_ROOT (not a full town nuclear clean)."
  echo "  - orchestrator instances for this rig"
  echo "  - implementation beads / pipeline artifacts (SPEC.md kept)"
  echo "  - agent mail for architect/qa/polecat/refinery/witness"
  read -r -p "Continue? [y/N] " ans
  case "${ans,,}" in
    y|yes) ;;
    *) echo "Aborted."; exit 0 ;;
  esac
fi

drain_rig_mail() {
  local r="$1"
  cd "$GT_ROOT"
  if [[ ! -d "$GT_ROOT/$r" ]]; then
    return 0
  fi
  echo "[drain] Rig '$r' agent inboxes..."
  for role in architect qa polecat refinery witness; do
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

clean_rig_pipeline_artifacts() {
  local r="$1"
  local rig_dir="$GT_ROOT/$r/mayor/rig"
  [[ -d "$rig_dir" ]] || return 0
  echo "[clean] rig pipeline artifacts under $r/mayor/rig/ (keeping SPEC.md)..."
  if [[ -d "$rig_dir/backend" ]]; then
    rm -rf "$rig_dir/backend"
    echo "  removed backend/"
  fi
  for f in architecture.md plan.md; do
    if [[ -f "$rig_dir/$f" ]]; then
      rm -f "$rig_dir/$f"
      echo "  removed $f"
    fi
  done
  for f in fizzbuzz.py main.py test_fizzbuzz.py dummy.py plan_complete.js; do
    if [[ -f "$rig_dir/$f" ]]; then
      rm -f "$rig_dir/$f"
      echo "  removed stray $f"
    fi
  done
}

# Reset tracked/untracked backend/ in the mayor/rig git worktree.
# If backend/ is tracked on the default branch, `git checkout -- backend/` restores
# it after `git clean -fd backend/` — run both so polecat starts from a clean tree.
reset_rig_git_worktree() {
  local r="$1"
  local rig_dir="$GT_ROOT/$r/mayor/rig"
  [[ -d "$rig_dir/.git" ]] || { echo "[git] skip: no git repo at $rig_dir"; return 0; }
  echo "[git] reset backend/ worktree in $rig_dir ..."
  (
    cd "$rig_dir"
    if [[ -d backend ]] || git ls-files --error-unmatch backend 2>/dev/null; then
      git clean -fd backend/ 2>/dev/null || true
      if git ls-files --error-unmatch backend >/dev/null 2>&1; then
        git checkout -- backend/ 2>/dev/null || true
        echo "  git clean -fd backend/ && git checkout -- backend/ (tracked files may reappear until removed from remote/default branch)"
      else
        echo "  git clean -fd backend/"
      fi
    else
      echo "  no backend/ to clean"
    fi
  )
}

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
    return 0
  fi
  local added=0 updated=0
  while IFS= read -r srcfile; do
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
  done < <(find "$orch_src" -type f | sort)
  echo "[orchestrator] synced from source tree ($added added, $updated updated)"
}

# Prefer rig-scoped removal; keep workflows for other rigs when variables.rig differs.
reset_orchestrator_instances() {
  if [[ "${RESET_ORCHESTRATOR_INSTANCES:-1}" != "1" ]]; then
    echo "[orchestrator] keeping instances.json (RESET_ORCHESTRATOR_INSTANCES=0)"
    return 0
  fi
  local inst="$GT_ROOT/orchestrator/instances.json"
  if [[ ! -f "$inst" ]]; then
    echo "[orchestrator] no instances.json"
    return 0
  fi
  python3 - "$inst" "$RIG" <<'PY'
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
rig = sys.argv[2]
data = json.loads(path.read_text())
instances = data.get("instances") or []
kept = []
removed = 0
for inst in instances:
    if not isinstance(inst, dict):
        kept.append(inst)
        continue
    vars_ = inst.get("variables") or {}
    if vars_.get("rig") == rig:
        removed += 1
        continue
    kept.append(inst)
if removed == 0 and not kept:
    path.unlink(missing_ok=True)
    print(f"[orchestrator] removed {path} (empty)")
elif removed == 0:
    print(f"[orchestrator] no instances for rig {rig!r} in {path}")
else:
    data["instances"] = kept
    if not kept:
        path.unlink(missing_ok=True)
        print(f"[orchestrator] removed {path} (no instances left after rig-scoped purge)")
    else:
        path.write_text(json.dumps(data, indent=2) + "\n")
        print(f"[orchestrator] removed {removed} instance(s) for rig {rig!r}; kept {len(kept)} other(s)")
PY
}

DOLT_HOST="${DOLT_HOST:-127.0.0.1}"
DOLT_PORT="${DOLT_PORT:-3307}"

ensure_dolt() {
  if command -v dolt >/dev/null 2>&1; then
    if DOLT_CLI_PASSWORD="" dolt --host "$DOLT_HOST" --port "$DOLT_PORT" \
        --user root --no-tls sql -q "SELECT 1;" &>/dev/null; then
      echo "[dolt] server reachable at ${DOLT_HOST}:${DOLT_PORT}"
      return 0
    fi
  fi
  if command -v gt &>/dev/null && [[ -f "$GT_ROOT/settings/config.json" ]]; then
    echo "[dolt] starting Gas Town shared server (gt dolt start) ..."
    (cd "$GT_ROOT" && gt dolt start) && return 0
  fi
  echo "[dolt] warn: no Dolt on ${DOLT_HOST}:${DOLT_PORT}; run: cd $GT_ROOT && gt dolt start" >&2
  return 1
}

start_beads_dolt() {
  local beads_dir="$1"
  local rig_ctx="${2:-}"
  echo "[beads] BEADS_DIR=$beads_dir"
  if command -v gt &>/dev/null && [[ -f "$GT_ROOT/settings/config.json" ]]; then
    ensure_dolt || true
    return 0
  fi
  if [[ -n "$rig_ctx" ]]; then
    (cd "$GT_ROOT/$rig_ctx/mayor/rig" 2>/dev/null && export BEADS_DIR="$beads_dir" && bd dolt start 2>/dev/null) \
      || (export BEADS_DIR="$beads_dir" && bd dolt start 2>/dev/null) \
      || true
  else
    (export BEADS_DIR="$beads_dir" && bd dolt start 2>/dev/null) || true
  fi
}

delete_impl_beads_in_dir() {
  local beads_dir="$1"
  local label="$2"
  local rig_for_ctx="${3:-}"
  local hq_only_flag="${4:-}"

  [[ -d "$beads_dir" ]] || return 0

  local keep_roles="${KEEP_ROLE_BEADS:-1}"
  local clear_all="${CLEAR_ALL_RIG_BEADS:-0}"
  local rig="$RIG"

  start_beads_dolt "$beads_dir" "$rig_for_ctx"

  echo "[beads] deleting implementation beads in $label ($beads_dir) ..."
  (
    export BEADS_DIR="$beads_dir"
    if [[ -n "$rig_for_ctx" ]]; then
      cd "$GT_ROOT/$rig_for_ctx/mayor/rig"
    else
      cd "$GT_ROOT"
    fi
    python3 - "$rig" "$keep_roles" "$clear_all" "$hq_only_flag" <<'PY'
import json
import os
import re
import subprocess
import sys

rig = sys.argv[1]
keep_roles = sys.argv[2] == "1"
clear_all = sys.argv[3] == "1"
hq_only = sys.argv[4] == "hq"
beads_dir = os.environ.get("BEADS_DIR", "")

role_suffixes = ("architect", "qa", "refinery", "witness")
role_id_re = re.compile(
    rf"^te-{re.escape(rig)}-({'|'.join(role_suffixes)})$", re.I
)

def run_bd(*args):
    return subprocess.run(
        ["bd", *args],
        capture_output=True,
        text=True,
        env={**os.environ, "BEADS_DIR": beads_dir},
    )

proc = run_bd("list", "--json", "--flat", "--limit=0")
if proc.returncode != 0:
    print(f"  warn: bd list --json failed: {proc.stderr.strip()}", file=sys.stderr)
    sys.exit(0)

try:
    items = json.loads(proc.stdout or "[]")
except json.JSONDecodeError:
    print("  warn: could not parse bd list --json", file=sys.stderr)
    sys.exit(0)

if not isinstance(items, list):
    items = items.get("beads") or items.get("issues") or []

deleted = 0
skipped = 0
for item in items:
    if not isinstance(item, dict):
        continue
    bead_id = item.get("id") or item.get("ID") or ""
    title = (item.get("title") or item.get("Title") or "").strip()
    bead_type = (item.get("type") or item.get("Type") or "").lower()
    title_l = title.lower()

    is_role = bool(bead_id and role_id_re.match(bead_id))
    is_patrol = "patrol" in title_l or bead_type in ("molecule", "mol", "wisp")
    is_impl = "implement backend" in title_l
    if hq_only and not (bead_id.startswith("hq-") and is_impl):
        continue

    if clear_all:
        should_delete = True
    elif is_impl:
        should_delete = True
        if keep_roles and (is_role or is_patrol):
            should_delete = False
    else:
        should_delete = False

    if not should_delete:
        if is_impl or (clear_all and bead_id):
            skipped += 1
        continue
    if not bead_id:
        continue

    del_proc = run_bd("delete", bead_id, "--force")
    if del_proc.returncode == 0:
        deleted += 1
        print(f"  deleted {bead_id}: {title[:60]}")
    else:
        print(f"  warn: bd delete {bead_id} failed: {del_proc.stderr.strip()}", file=sys.stderr)

print(f"  done: {deleted} deleted, {skipped} skipped")
PY
  )
}

print_verification() {
  echo ""
  echo "=== verification ==="
  (cd "$GT_ROOT" && gt mayor workflow status 2>/dev/null) || echo "(gt mayor workflow status unavailable)"
  echo ""
  local rig_dir="$GT_ROOT/$RIG/mayor/rig"
  local beads_dir="$GT_ROOT/$RIG/.beads"
  if [[ -d "$beads_dir" ]]; then
    echo "--- bd list (rig $RIG) ---"
    start_beads_dolt "$beads_dir" "$RIG"
    (export BEADS_DIR="$beads_dir" && cd "$rig_dir" 2>/dev/null && bd list 2>/dev/null | head -30) \
      || (export BEADS_DIR="$beads_dir" && bd list 2>/dev/null | head -30) \
      || echo "(bd list failed)"
  fi
  echo ""
  echo "--- mayor/rig backend/ ---"
  if [[ -d "$rig_dir/backend" ]]; then
    ls -la "$rig_dir/backend" 2>/dev/null || true
  else
    echo "(no backend/ directory)"
  fi
  echo ""
  echo "--- orchestrator/instances.json ---"
  local inst="$GT_ROOT/orchestrator/instances.json"
  if [[ -f "$inst" ]]; then
    python3 - "$inst" "$RIG" <<'PY'
import json, sys
from pathlib import Path
path = Path(sys.argv[1])
rig = sys.argv[2]
data = json.loads(path.read_text())
for inst in data.get("instances") or []:
    if not isinstance(inst, dict):
        continue
    iid = inst.get("id", "?")
    state = inst.get("current_state", "?")
    status = inst.get("status", "?")
    irig = (inst.get("variables") or {}).get("rig", "")
    mark = " <-- this rig" if irig == rig else ""
    print(f"  {iid}  state={state}  status={status}  rig={irig}{mark}")
PY
  else
    echo "(missing — fresh orchestrator state)"
  fi
}

# --- main ---

if [[ "${GT_DOWN:-1}" == "1" ]]; then
  echo "=== gt down ==="
  (cd "$GT_ROOT" && gt down) || true
fi

reset_orchestrator_instances

if [[ "${GT_UP:-1}" == "1" ]]; then
  echo "=== gt up ==="
  (cd "$GT_ROOT" && gt up)
fi

sync_orchestrator_assets

if [[ "${RESET_IMPL_BEADS:-1}" == "1" ]]; then
  if [[ "${CLEAR_ALL_RIG_BEADS:-0}" == "1" ]]; then
    echo "[beads] CLEAR_ALL_RIG_BEADS=1 — deleting all beads in rig .beads"
    KEEP_ROLE_BEADS=0
  fi
  delete_impl_beads_in_dir "$GT_ROOT/$RIG/.beads" "rig $RIG" "$RIG"
fi

if [[ "${CLEAR_TOWN_IMPL_BEADS:-0}" == "1" && -d "$GT_ROOT/.beads" ]]; then
  delete_impl_beads_in_dir "$GT_ROOT/.beads" "town HQ (hq-* Implement backend)" "" "hq"
fi

clean_rig_pipeline_artifacts "$RIG"

if [[ "${RESET_GIT_WORKTREE:-1}" == "1" ]]; then
  reset_rig_git_worktree "$RIG"
fi

drain_rig_mail "$RIG"

if [[ -d "$GT_ROOT/$RIG/.beads" ]]; then
  chmod 700 "$GT_ROOT/$RIG/.beads"
  echo "[beads] chmod 700 $GT_ROOT/$RIG/.beads"
fi

if [[ "${GT_UP:-1}" == "1" ]]; then
  echo "=== gt up (refresh agents after cleanup) ==="
  (cd "$GT_ROOT" && gt up) || true
fi

sync_orchestrator_assets
drain_rig_mail "$RIG"

normalize_workflow_profile() {
  echo "=== gt rig normalize-profile (docker → final delivery phase) ==="
  if (cd "$GT_ROOT" && gt rig normalize-profile "$RIG"); then
    (cd "$GT_ROOT" && gt rig set-phase "$RIG" --list) || true
  else
    echo "[profile] warn: normalize-profile failed — run: cd $GT_ROOT && gt rig normalize-profile $RIG" >&2
  fi
}
normalize_workflow_profile

if [[ "${START_RIG_FLOW:-0}" == "1" ]]; then
  echo "=== gt mayor workflow start rig-flow ==="
  if (cd "$GT_ROOT" && gt mayor workflow start rig-flow --rig "$RIG"); then
    echo "[orchestrator] workflow started"
  else
    echo "[orchestrator] workflow start failed" >&2
  fi
fi

print_verification

echo ""
echo "=== done ==="
echo "Town: $GT_ROOT"
echo "Rig:  $RIG"
if [[ "${START_RIG_FLOW:-0}" != "1" ]]; then
  echo "Start workflow: cd $GT_ROOT && gt mayor workflow start rig-flow --rig $RIG"
  echo "Or: START_RIG_FLOW=1 $0 --force"
fi
