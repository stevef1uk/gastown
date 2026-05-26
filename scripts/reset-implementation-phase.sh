#!/usr/bin/env bash
#
# reset-implementation-phase.sh — targeted implementation hard reset for one rig
#
# Mirrors orchestrator hook reset_implementation_phase (wall-clock state timeout):
#   - stop dev servers on tracked ports
#   - delete on-disk files for open and in_progress implement beads only
#   - reset in_progress implement beads → open
#   - remove qa/implementation-progress.json
#
# Closed implement beads and their files are left alone.
#
# Usage:
#   ./scripts/reset-implementation-phase.sh
#   GT_ROOT=~/gt RIG=testgt3 ./scripts/reset-implementation-phase.sh
#   GT_ROOT=~/gt RIG=testgt3 ./scripts/reset-implementation-phase.sh --dry-run
#
set -euo pipefail

GT_ROOT="${GT_ROOT:-$HOME/gt}"
RIG="${RIG:-${RIG_NAME:-testgt3}}"
DRY_RUN=false
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=true ;;
    -h|--help)
      sed -n '2,22p' "$0"
      exit 0
      ;;
  esac
done

RIG_DIR="$GT_ROOT/$RIG/mayor/rig"
BEADS_DIR="${BEADS_DIR:-$GT_ROOT/$RIG/.beads}"
PROFILE="$RIG_DIR/.gastown/workflow-profile.json"

if [[ ! -d "$RIG_DIR" ]]; then
  echo "FATAL: missing rig dir $RIG_DIR" >&2
  exit 1
fi
if [[ ! -f "$PROFILE" ]]; then
  echo "FATAL: missing workflow profile $PROFILE" >&2
  exit 1
fi

run() {
  if $DRY_RUN; then
    echo "[dry-run] $*"
  else
    "$@"
  fi
}

echo "[reset] rig=$RIG town=$GT_ROOT"

# Dev servers (best-effort)
if [[ -x "$(dirname "$0")/stop-rig-dev-servers.sh" ]]; then
  echo "[reset] stopping dev servers..."
  run "$(dirname "$0")/stop-rig-dev-servers.sh" 8080 || true
fi

echo "[reset] removing files for open/in_progress implement beads..."
export BEADS_DIR
cd "$RIG_DIR"
python3 - "$PROFILE" "$RIG_DIR" "$DRY_RUN" <<'PY'
import json, os, re, subprocess, sys

profile_path, rig_dir, dry = sys.argv[1], sys.argv[2], sys.argv[3].lower() == "true"
raw = json.load(open(profile_path))
val = raw.get("validation") or raw
layout = (val.get("layout_root") or "").strip("/")
bead_prefix = (val.get("bead_title_contains") or "Implement").strip()
env = {**os.environ, "BEADS_DIR": os.environ.get("BEADS_DIR", "")}
KEEP = {"go.mod", "go.sum", "requirements.txt", "pyproject.toml"}

def extract_path(title):
    m = re.search(r"Implement\s+(\S+)\s+per\s+architecture", title, re.I)
    return m.group(1) if m else ""

def implement_beads(status):
    p = subprocess.run(
        ["bd", "list", "--limit=0", f"--status={status}", "--json"],
        capture_output=True, text=True, env=env,
    )
    raw = (p.stdout or "").strip()
    if not raw:
        return []
    data = json.loads(raw)
    items = data if isinstance(data, list) else data.get("issues") or data.get("items") or []
    out = []
    for it in items:
        title = it.get("title") or ""
        if bead_prefix.lower() in title.lower() and "per arch" in title.lower():
            path = extract_path(title)
            if path:
                out.append(path)
    return out

paths = []
seen = set()
for status in ("open", "in_progress"):
    for path in implement_beads(status):
        if path in seen:
            continue
        seen.add(path)
        if os.path.basename(path).lower() in KEEP:
            continue
        paths.append(path)

removed = 0
for rel in paths:
    abs_path = os.path.join(rig_dir, rel)
    if not os.path.isfile(abs_path):
        continue
    if dry:
        print(f"  would remove {rel}")
    else:
        os.remove(abs_path)
        print(f"  removed {rel}")
    removed += 1
print(f"  total: {removed} active bead file(s)")
PY

echo "[reset] removing malformed layout artifacts (prose/backtick filenames)..."
python3 - "$PROFILE" "$RIG_DIR" "$DRY_RUN" <<'PY'
import json, os, sys

profile_path, rig_dir, dry = sys.argv[1], sys.argv[2], sys.argv[3].lower() == "true"
raw = json.load(open(profile_path))
val = raw.get("validation") or raw
layout = (val.get("layout_root") or "").strip("/")
if not layout:
    sys.exit(0)
layout_dir = os.path.join(rig_dir, layout)
KEEP = {"go.mod", "go.sum", "requirements.txt", "pyproject.toml"}

def malformed(rel_base):
    if rel_base.lower() in KEEP:
        return False
    if "`" in rel_base or rel_base.startswith("**"):
        return True
    low = rel_base.lower()
    if "command to create" in low or "per architecture" in low:
        return True
    if "." not in os.path.basename(rel_base) and rel_base not in ("Dockerfile", "Makefile", "LICENSE", "README", "Containerfile"):
        return True
    return False

removed = 0
for root, _dirs, files in os.walk(layout_dir):
    for name in files:
        if name.lower() in KEEP:
            continue
        path = os.path.join(root, name)
        rel = os.path.relpath(path, rig_dir).replace("\\", "/")
        if not malformed(name) and not malformed(rel):
            continue
        if dry:
            print(f"  would remove malformed {rel}")
        else:
            os.remove(path)
            print(f"  removed malformed {rel}")
        removed += 1
print(f"  total: {removed} malformed file(s)")
PY

echo "[reset] resetting in_progress implement beads to open..."
python3 - "$DRY_RUN" <<'PY'
import json, os, subprocess, sys

dry = sys.argv[1].lower() == "true"
beads_dir = os.environ.get("BEADS_DIR", "")
env = {**os.environ, "BEADS_DIR": beads_dir}

def implement_ids(status):
    p = subprocess.run(
        ["bd", "list", "--limit=0", f"--status={status}", "--json"],
        capture_output=True, text=True, env=env,
    )
    raw = (p.stdout or "").strip()
    if not raw:
        return []
    data = json.loads(raw)
    items = data if isinstance(data, list) else data.get("issues") or data.get("items") or []
    out = []
    for it in items:
        title = (it.get("title") or "").lower()
        if "implement" in title and "per arch" in title:
            out.append(it.get("id", ""))
    return [x for x in out if x]

for bead_id in implement_ids("in_progress"):
    print(f"  in_progress → open: {bead_id}")
    if dry:
        print(f"[dry-run] bd update {bead_id} --status=open")
    else:
        subprocess.run(["bd", "update", bead_id, "--status=open"], check=True, env=env)
PY

PROGRESS="$GT_ROOT/$RIG/qa/implementation-progress.json"
if [[ -f "$PROGRESS" ]]; then
  echo "[reset] clearing $PROGRESS"
  run rm -f "$PROGRESS"
fi

echo "[reset] done. Polecat can restart implementation from the first open implement bead."
