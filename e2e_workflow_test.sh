#!/bin/bash
# E2E Workflow Test Script for Gas Town
# Tests the full orchestration: Mayor -> Architect -> Planner -> Polecat -> QA

set -e
set -x

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GT_DIR="${GT_ROOT:-$HOME/gt}"
RIG="ping_rig"
FREERIDE_ROOT="${FREERIDE_ROOT:-}"

# Portable check: is port $1 listening with "dolt" in the process name?
is_dolt_listening() {
    local port="${1:-3307}"
    case "$(uname -s)" in
        Darwin)
            lsof -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | grep -qi dolt
            ;;
        Linux)
            ss -tlnp "sport = :$port" 2>/dev/null | grep -qi dolt
            ;;
        *)
            return 1
            ;;
    esac
}

freeride_bootstrap_dir() {
    if [[ -n "$FREERIDE_ROOT" && -d "$FREERIDE_ROOT/scripts" ]]; then
        echo "$FREERIDE_ROOT/scripts"
        return 0
    fi
    return 1
}

run_freeride_bootstrap() {
    local script="$1"
    shift
    local dir
    if ! dir="$(freeride_bootstrap_dir)"; then
        return 0
    fi
    if [[ ! -x "$dir/$script" ]]; then
        chmod +x "$dir/$script" 2>/dev/null || true
    fi
    echo "[bootstrap] $dir/$script $*"
    bash "$dir/$script" "$@"
}

echo "=== E2E Workflow Test ==="

if freeride_bootstrap_dir >/dev/null; then
    run_freeride_bootstrap check-do-it-all-deps.sh
fi

# 1. Start services
echo "[1] Starting GT services..."
gt install "$GT_DIR" || true
cd "$GT_DIR"

# Prepare git repo for rig registration (no Dolt needed yet)
rig_needs_creation=false
if [ ! -d "$GT_DIR/$RIG/mayor" ]; then
    rig_needs_creation=true
    echo "[$RIG is missing or not registered in rigs.json! Will create after stack is up...]"
    rm -rf "$GT_DIR/$RIG"
    DUMMY_DIR="/tmp/gt-dummy-repo-$$"
    rm -rf "$DUMMY_DIR"
    mkdir -p "$DUMMY_DIR"
    (
        cd "$DUMMY_DIR"
        git init
        echo "# Dummy Project" > README.md
        git add README.md
        git config user.email "test@example.com"
        git config user.name "Test Bot"
        git commit -m "Initial commit"
    )
fi

run_freeride_bootstrap ensure-gt-orchestrator-singleton.sh || true
gt down 2>/dev/null || true
run_freeride_bootstrap ensure-gt-orchestrator-singleton.sh || true
gt up || true

# Now Dolt is running (gt up handles empty database initialization).
# Register rig if needed.
if [ "$rig_needs_creation" = true ]; then
    echo "[$RIG needs creation — registering with Dolt live...]"
    dolt_port="${GT_DOLT_PORT:-3307}"
    for i in {1..10}; do
        if is_dolt_listening "$dolt_port"; then
            echo "[Dolt ready on port $dolt_port after ${i}s]"
            break
        fi
        sleep 1
    done
    gt rig add "$RIG" "file://$DUMMY_DIR"
    
    mkdir -p "$GT_DIR/$RIG/mayor/rig/.gastown"
    echo '{"qa_verify_command": "python3 -m pytest -v pingapp/test_main.py", "spec_summary": "FastAPI ping app."}' > "$GT_DIR/$RIG/mayor/rig/.gastown/workflow-profile.json"
    
    cp "$SCRIPT_DIR/internal/orchestrator/example_specs/SPEC_python.md" "$GT_DIR/$RIG/mayor/rig/SPEC.md"

    (
        cd "$GT_DIR/$RIG/mayor/rig"
        git add .gastown/workflow-profile.json SPEC.md
        git commit -m "Add spec and workflow profile" || true
    )
fi

if freeride_bootstrap_dir >/dev/null && [[ "${DO_IT_ALL:-}" == "1" || -n "$FREERIDE_ROOT" ]]; then
    # Do not run ensure-gt-orchestrator-singleton here — it used to kill the sole
    # orchestrator that gt up just started, leaving 0 processes until timeout.
    run_freeride_bootstrap wait-for-gt-stack.sh --with-orchestrator
    echo "[bootstrap] restarting rig $RIG after stack ready..."
    gt rig restart "$RIG" || true
    # spec-index calls the LLM; run only after the town stack is up (avoid pre-up hangs).
    if command -v gt >/dev/null 2>&1; then
        echo "[1a] Indexing workflow profile from SPEC/architecture..."
        gt rig spec-index "$RIG" --force || true
    fi
else
    sleep 5
fi

# 1b. Seed HQ issue_prefix if missing (Fix #101).
# `gt down` wipes the HQ beads dolt dir, and `gt up` recreates an
# empty `hq.config` without the `issue_prefix=hq` row. Without it,
# the very next `gt mail send mayor/` fails with
# "database not initialized: issue_prefix config is missing".
# This is a belt-and-braces invocation of the healer that
# clean-gastown.sh drops next to the wipe. If the healer doesn't
# exist (someone wired up the town without clean-gastown.sh), we
# fall back to an inline INSERT IGNORE.
if [[ -x "$GT_DIR/.gt-post-reset-init.sh" ]]; then
    bash "$GT_DIR/.gt-post-reset-init.sh" || true
elif command -v dolt >/dev/null 2>&1; then
    DOLT_CLI_PASSWORD="" dolt --host 127.0.0.1 --port 3307 \
        --user root --no-tls sql -q \
        "USE hq; INSERT IGNORE INTO config (\`key\`, value) VALUES ('issue_prefix','hq');" \
        2>/dev/null || true
fi

# 2. Kick off the project by mailing the Mayor — the way a real operator
#    would. (Fix #106: Mayor template now has a Stage 0 / kickoff branch
#    that creates the project bead itself, then dispatches Stage 1.)
#
# Previous versions of this script pre-created the project bead via
# `bd create` and then mailed the architect directly. That was a
# workaround for the Mayor template having no "Stage 0" / project-
# kickoff branch — it assumed a bead was always already attached. The
# workaround bypassed the whole point of having a Mayor: the operator
# should be able to describe what they want, hand it to the Mayor, and
# walk away.
#
# With Fix #106 in the mayor.md.tmpl, the operator does ONLY this:
#   - Send Mayor a free-form mail with subject "Project: <title>" and
#     a body containing `Rig:` and `Spec:` lines (no `Project bead:`).
#   - Nudge Mayor to wake.
#
# The Mayor's routing table now recognises this as Stage 0 (kickoff),
# creates the project bead with `bd create`, and slings/mails the
# architect with that bead. Stage 1 onward proceeds as before.

echo "[2] Kicking off workflow explicitly..."
gt mayor workflow start rig-flow --rig="$RIG" || true
echo "✓ Workflow started."

# 4. Monitor flow. Planner is town-level (no rig prefix). Mayor will
#    create the project bead during Stage 0 — we can't bind PROJECT_BEAD
#    up front anymore, so we let `bd list` show us anything new it makes.
echo "[4] Monitoring flow..."
for i in {1..20}; do
    sleep 15
    echo "=== Check $i (T+$((i*15))s) ==="
    echo "--- Mayor hook ---"
    (cd $GT_DIR 2>/dev/null && gt hook show mayor 2>/dev/null) || true
    echo "--- Architect hook ---"
    (cd $GT_DIR/$RIG 2>/dev/null && gt hook show architect 2>/dev/null) || true
    echo "--- Planner hook ---"
    (cd $GT_DIR 2>/dev/null && gt hook show planner 2>/dev/null) || true
    echo "--- Recent HQ beads ---"
    (cd $GT_DIR && bd list --status=open --limit=8 2>/dev/null | head -10) || true
    echo "--- Recent rig beads ---"
    (cd $GT_DIR/$RIG 2>/dev/null && bd list --status=open --limit=8 2>/dev/null | head -10) || true
    echo "--- architecture.md? ---"
    ls -la $GT_DIR/$RIG/architect/architecture.md 2>/dev/null | head -1 || echo "not yet"
done

# 5. Check final state
echo "[5] Final state..."
gt status
echo "--- architecture.md ---"
ls -la $GT_DIR/$RIG/architect/architecture.md 2>/dev/null || echo "No architecture.md"
echo "--- rig working tree (code artifacts?) ---"
ls -la $GT_DIR/$RIG/refinery/rig 2>/dev/null | head -20 || echo "no refinery worktree"

echo "=== Test Complete ==="