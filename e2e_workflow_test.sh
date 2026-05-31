#!/bin/bash
# E2E Workflow Test Script for Gas Town
# Tests the full orchestration: Mayor -> Architect -> Planner -> Polecat -> QA

set -e
set -x

GT_DIR="${GT_ROOT:-$HOME/gt}"
RIG="ping_rig"

echo "=== E2E Workflow Test ==="

# 1. Start services
echo "[1] Starting GT services..."
gt install "$GT_DIR" || true
cd "$GT_DIR"

if [ ! -d "$GT_DIR/$RIG" ] || ! grep -q "\"$RIG\"" "$GT_DIR/mayor/rigs.json" 2>/dev/null; then
    echo "[$RIG is missing or not registered in rigs.json! Creating a dummy rig to test against...]"
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
    gt rig add "$RIG" "file://$DUMMY_DIR"
    
    mkdir -p "$GT_DIR/$RIG/mayor/rig/.gastown"
    echo '{"qa_verify_command": "python3 -m pytest -v pingapp/test_main.py", "spec_summary": "FastAPI ping app."}' > "$GT_DIR/$RIG/mayor/rig/.gastown/workflow-profile.json"
    
    cat > "$GT_DIR/$RIG/mayor/rig/SPEC.md" << 'EOF'
# Ping Service
Create a Python FastAPI script in `pingapp/main.py` that provides a simple `/ping` endpoint returning `{"ping": "pong"}`.
Also include pytest tests in `pingapp/test_main.py` that verify the endpoint works.
Also include a `requirements.txt`.
EOF
    (
        cd "$GT_DIR/$RIG/mayor/rig"
        git add .gastown/workflow-profile.json SPEC.md
        git commit -m "Add spec and workflow profile" || true
    )
fi

gt down 2>/dev/null || true
gt up

sleep 5

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

echo "[2] Mailing mayor with free-form project request (Stage 0 kickoff)..."
gt mail send mayor/ -s "Project: $RIG Ping Webserver" --stdin <<KICKOFFMAIL
Rig: $RIG
Spec: $GT_DIR/$RIG/mayor/rig/SPEC.md

Please build the project described by the SPEC at the path above
(default work item is FizzBuzz). Design -> plan -> implement -> review
via your normal pipeline. Output a real code artifact in the rig
working tree.

Operator: e2e_workflow_test.sh
KICKOFFMAIL
gt nudge mayor "New project request, check your inbox (Stage 0 kickoff)"

# 4. Monitor flow. Planner is town-level (no rig prefix). Mayor will
#    create the project bead during Stage 0 — we can't bind PROJECT_BEAD
#    up front anymore, so we let `bd list` show us anything new it makes.
echo "[4] Monitoring flow..."
for i in {1..20}; do
    sleep 15
    echo "=== Check $i (T+$((i*15))s) ==="
    echo "--- Mayor hook ---"
    gt hook show mayor 2>/dev/null | head -4 || echo "empty"
    echo "--- Architect hook ---"
    gt hook show $RIG/architect 2>/dev/null | head -4 || echo "empty"
    echo "--- Planner hook ---"
    gt hook show planner 2>/dev/null | head -4 || echo "empty"
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