#!/usr/bin/env bash
#
# clear-implementation-beads.sh — delete rig- or town-level implementation beads (bd delete)
#
# Use after planner retries left duplicate "Implementation …" tasks, or to reset
# rig-flow implementation work without nuking the whole town.
#
# Usage:
#   ./scripts/clear-implementation-beads.sh --rig testgt1
#   ./scripts/clear-implementation-beads.sh --rig testgt1 --dry-run
#   ./scripts/clear-implementation-beads.sh --rig testgt1 --match "Implementation defender/"
#   ./scripts/clear-implementation-beads.sh --rig testgt1 --all-open    # every open bead in rig DB
#   ./scripts/clear-implementation-beads.sh --rig testgt1 --all         # every bead in rig DB (except roles/patrol)
#   ./scripts/clear-implementation-beads.sh --town --match "Implement backend"
#
# Environment:
#   GT_ROOT=~/gt          Gas Town town root
#   KEEP_ROLE_BEADS=1     skip te-<rig>-architect/qa/refinery/witness (default 1)
#
# Requires: bash, python3, bd, Dolt reachable (gt dolt start / gt up for Gas Town).
#
set -euo pipefail

GT_ROOT="${GT_ROOT:-$HOME/gt}"
RIG=""
SCOPE_TOWN=0
MATCH=""
ALL_BEADS=0
OPEN_ONLY=0
INCLUDE_CLOSED=0
DRY_RUN=0
FORCE=0
KEEP_ROLE_BEADS="${KEEP_ROLE_BEADS:-1}"

usage() {
  sed -n '2,22p' "$0" | head -n -1
  exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --rig) RIG="${2:?--rig requires a name}"; shift 2 ;;
    --town) SCOPE_TOWN=1; shift ;;
    --match) MATCH="${2:?--match requires a substring}"; shift 2 ;;
    --all) ALL_BEADS=1; shift ;;
    --all-open) ALL_BEADS=1; OPEN_ONLY=1; shift ;;
    --include-closed) INCLUDE_CLOSED=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    -f|--force) FORCE=1; shift ;;
    -h|--help) usage 0 ;;
    *) echo "Unknown option: $1" >&2; usage 1 ;;
  esac
done

if [[ -z "$RIG" && "$SCOPE_TOWN" -eq 0 ]]; then
  echo "FATAL: pass --rig <name> and/or --town" >&2
  usage 1
fi

if [[ ! -f "$GT_ROOT/settings/config.json" ]]; then
  echo "FATAL: not a Gas Town root (no settings/config.json): $GT_ROOT" >&2
  exit 1
fi

if [[ -n "$RIG" && ! -d "$GT_ROOT/$RIG" ]]; then
  echo "FATAL: rig directory missing: $GT_ROOT/$RIG" >&2
  exit 1
fi

read_profile_match() {
  local rig="$1"
  local profile="$GT_ROOT/$rig/mayor/rig/.gastown/workflow-profile.json"
  [[ -f "$profile" ]] || return 1
  python3 - "$profile" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1]))
    v = (d.get("validation") or {}).get("bead_title_contains") or ""
    v = v.strip()
    if v:
        print(v)
except Exception:
    pass
PY
}

if [[ -z "$MATCH" && "$ALL_BEADS" -eq 0 ]]; then
  if [[ -n "$RIG" ]] && profile_match="$(read_profile_match "$RIG" 2>/dev/null || true)" && [[ -n "$profile_match" ]]; then
    MATCH="$profile_match"
    echo "[match] using workflow-profile bead_title_contains: $MATCH"
  else
    MATCH="Implementation"
    echo "[match] default title substring: $MATCH"
  fi
fi

DOLT_HOST="${DOLT_HOST:-127.0.0.1}"
DOLT_PORT="${DOLT_PORT:-3307}"

dolt_port_reachable() {
  command -v dolt >/dev/null 2>&1 || return 1
  DOLT_CLI_PASSWORD="" dolt --host "$DOLT_HOST" --port "$DOLT_PORT" \
    --user root --no-tls sql -q "SELECT 1;" &>/dev/null
}

beads_db_ready() {
  local beads_dir="$1"
  local rig_ctx="${2:-}"
  if [[ -n "$rig_ctx" ]]; then
    (cd "$GT_ROOT/$rig_ctx/mayor/rig" 2>/dev/null && export BEADS_DIR="$beads_dir" && bd list --flat --limit=1 &>/dev/null) \
      || (export BEADS_DIR="$beads_dir" && bd list --flat --limit=1 &>/dev/null)
  else
    (cd "$GT_ROOT" 2>/dev/null && export BEADS_DIR="$beads_dir" && bd list --flat --limit=1 &>/dev/null) \
      || (export BEADS_DIR="$beads_dir" && bd list --flat --limit=1 &>/dev/null)
  fi
}

# Gas Town uses one shared Dolt on :3307 (gt dolt). Do not call bd dolt start per rig — it
# collides on the port and can block town HQ beads.
ensure_dolt() {
  if dolt_port_reachable; then
    echo "[dolt] server reachable at ${DOLT_HOST}:${DOLT_PORT}"
    return 0
  fi
  if command -v gt &>/dev/null && [[ -f "$GT_ROOT/settings/config.json" ]]; then
    echo "[dolt] starting Gas Town shared server (gt dolt start) ..."
    if (cd "$GT_ROOT" && gt dolt start); then
      if dolt_port_reachable; then
        return 0
      fi
    fi
  fi
  echo "[dolt] warn: no Dolt server on ${DOLT_HOST}:${DOLT_PORT}." >&2
  echo "[dolt]       Run: cd $GT_ROOT && gt dolt start  (or gt up)" >&2
  echo "[dolt]       If a rig-local bd dolt holds the port: BEADS_DIR=<rig>/.beads bd dolt stop" >&2
  return 1
}

start_beads_dolt() {
  local beads_dir="$1"
  local rig_ctx="${2:-}"
  echo "[beads] BEADS_DIR=$beads_dir"

  if beads_db_ready "$beads_dir" "$rig_ctx"; then
    return 0
  fi

  # Non–Gas Town: per-project bd dolt (only when shared gt server is not in use).
  if command -v gt &>/dev/null && [[ -f "$GT_ROOT/settings/config.json" ]]; then
    ensure_dolt || true
    if beads_db_ready "$beads_dir" "$rig_ctx"; then
      return 0
    fi
    echo "[beads] warn: bd cannot list beads in $beads_dir (is this DB on the town server?)" >&2
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

delete_beads_in_dir() {
  local beads_dir="$1"
  local label="$2"
  local rig_for_ctx="${3:-}"
  local hq_only="${4:-0}"

  [[ -d "$beads_dir" ]] || {
    echo "[beads] skip $label — no directory $beads_dir"
    return 0
  }

  start_beads_dolt "$beads_dir" "$rig_for_ctx"

  echo "[beads] scanning $label ..."
  (
    export BEADS_DIR="$beads_dir"
    export GT_ROOT
    if [[ -n "$rig_for_ctx" ]]; then
      cd "$GT_ROOT/$rig_for_ctx/mayor/rig"
    else
      cd "$GT_ROOT"
    fi
    python3 - \
      "$RIG" \
      "$KEEP_ROLE_BEADS" \
      "$ALL_BEADS" \
      "$OPEN_ONLY" \
      "$INCLUDE_CLOSED" \
      "$DRY_RUN" \
      "$MATCH" \
      "$hq_only" <<'PY'
import json
import os
import re
import subprocess
import sys

rig = sys.argv[1]
keep_roles = sys.argv[2] == "1"
all_beads = sys.argv[3] == "1"
open_only = sys.argv[4] == "1"
include_closed = sys.argv[5] == "1"
dry_run = sys.argv[6] == "1"
match_sub = sys.argv[7]
hq_only = sys.argv[8] == "1"
beads_dir = os.environ.get("BEADS_DIR", "")

role_suffixes = ("architect", "qa", "refinery", "witness")
role_id_re = re.compile(
    rf"^te-{re.escape(rig)}-({'|'.join(role_suffixes)})$", re.I
) if rig else None

def run_bd(*args):
    return subprocess.run(
        ["bd", *args],
        capture_output=True,
        text=True,
        env={**os.environ, "BEADS_DIR": beads_dir},
    )

# bd v0.59+ needs --flat with --json; without it some DBs error (e.g. missing started_at).
TEXT_LINE_RE = re.compile(
    r"^[○●✓❄]?\s*(?P<id>\S+)\s+(?:[○●✓❄]\s+)?(?:P\d+\s+)?(?P<title>.+)$"
)

def parse_text_list(stdout):
    items = []
    for line in (stdout or "").splitlines():
        line = line.strip()
        if not line or line.startswith("Showing ") or line.lower().startswith("no issues"):
            continue
        m = TEXT_LINE_RE.match(line)
        if not m:
            continue
        items.append({
            "id": m.group("id"),
            "title": m.group("title").strip(),
            "status": "open" if open_only else "",
        })
    return items

def fetch_items():
    base = ["list", "--flat", "--limit=0"]
    if open_only and not include_closed:
        base = ["list", "--status=open", "--flat", "--limit=0"]
    elif include_closed or all_beads:
        base = ["list", "--all", "--flat", "--limit=0"]

    # JSON path (preferred)
    proc = run_bd(*base, "--json")
    if proc.returncode == 0 and (proc.stdout or "").strip():
        try:
            parsed = json.loads(proc.stdout)
            if isinstance(parsed, dict) and parsed.get("error"):
                print(f"  warn: bd list --json: {parsed['error']}", file=sys.stderr)
            elif isinstance(parsed, list):
                return parsed
            elif isinstance(parsed, dict):
                for key in ("beads", "issues", "items"):
                    if key in parsed and isinstance(parsed[key], list):
                        return parsed[key]
        except json.JSONDecodeError:
            pass

    # Text fallback
    proc = run_bd(*base)
    if proc.returncode != 0:
        err = (proc.stderr or proc.stdout or "").strip()
        print(f"  FATAL: bd list failed: {err}", file=sys.stderr)
        sys.exit(1)
    items = parse_text_list(proc.stdout)
    if items:
        print(f"  note: used text bd list (JSON unavailable)", file=sys.stderr)
    return items

items = fetch_items()

match_l = match_sub.lower()
to_delete = []
skipped = 0

for item in items:
    if not isinstance(item, dict):
        continue
    bead_id = (item.get("id") or item.get("ID") or "").strip()
    title = (item.get("title") or item.get("Title") or "").strip()
    status = (item.get("status") or item.get("Status") or "").strip().lower()
    bead_type = (item.get("type") or item.get("Type") or "").lower()
    title_l = title.lower()

    if not bead_id:
        continue

    is_role = bool(role_id_re and role_id_re.match(bead_id))
    is_patrol = "patrol" in title_l or bead_type in ("molecule", "mol", "wisp")
    is_hq = bead_id.startswith("hq-")

    if hq_only and not is_hq:
        continue
    if not hq_only and is_hq and rig:
        # rig scope: skip town hq-* beads in rig DB listing (unusual but safe)
        pass

    if all_beads:
        should_delete = True
        if keep_roles and (is_role or is_patrol):
            should_delete = False
    else:
        should_delete = match_l in title_l
        if keep_roles and (is_role or is_patrol) and should_delete:
            should_delete = False

    if not should_delete:
        skipped += 1
        continue

    if open_only and status in ("closed", "done") and not include_closed:
        skipped += 1
        continue

    to_delete.append((bead_id, title, status))

print(f"  matched {len(to_delete)} bead(s) to delete ({skipped} skipped)")

if dry_run:
    for bead_id, title, status in to_delete[:50]:
        print(f"  [dry-run] would delete {bead_id} ({status}): {title[:70]}")
    if len(to_delete) > 50:
        print(f"  [dry-run] ... and {len(to_delete) - 50} more")
    sys.exit(0)

deleted = 0
failed = 0
batch = 50
for i in range(0, len(to_delete), batch):
    chunk = [b[0] for b in to_delete[i : i + batch]]
    del_proc = run_bd("delete", *chunk, "--force")
    if del_proc.returncode == 0:
        deleted += len(chunk)
        for bead_id, title, status in to_delete[i : i + batch]:
            print(f"  deleted {bead_id} ({status}): {title[:60]}")
    else:
        # fall back to one-by-one
        for bead_id, title, status in to_delete[i : i + batch]:
            one = run_bd("delete", bead_id, "--force")
            if one.returncode == 0:
                deleted += 1
                print(f"  deleted {bead_id} ({status}): {title[:60]}")
            else:
                failed += 1
                print(f"  warn: {bead_id}: {one.stderr.strip()}", file=sys.stderr)

print(f"  done: {deleted} deleted, {failed} failed, {skipped} skipped")
PY
  )
}

echo "=== clear-implementation-beads ==="
echo "  GT_ROOT=$GT_ROOT"

if [[ "$SCOPE_TOWN" -eq 1 ]]; then
  echo "  scope: town ($GT_ROOT/.beads)"
fi
if [[ -n "$RIG" ]]; then
  echo "  scope: rig $RIG ($GT_ROOT/$RIG/.beads)"
fi

if [[ "$DRY_RUN" -eq 1 ]]; then
  echo "  mode: dry-run"
fi

if [[ "$FORCE" -eq 0 && "$DRY_RUN" -eq 0 ]]; then
  echo ""
  echo "This permanently deletes beads (bd delete --force). Role/patrol beads are kept by default."
  read -r -p "Continue? [y/N] " ans
  case "${ans,,}" in
    y|yes) ;;
    *) echo "Aborted."; exit 0 ;;
  esac
fi

if command -v gt &>/dev/null && [[ -f "$GT_ROOT/settings/config.json" ]]; then
  ensure_dolt || true
fi

if [[ "$SCOPE_TOWN" -eq 1 ]]; then
  delete_beads_in_dir "$GT_ROOT/.beads" "town HQ" "" "1"
fi

if [[ -n "$RIG" ]]; then
  delete_beads_in_dir "$GT_ROOT/$RIG/.beads" "rig $RIG" "$RIG" "0"
fi

echo ""
echo "=== done ==="
