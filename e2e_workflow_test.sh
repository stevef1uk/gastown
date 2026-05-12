#!/bin/bash
# E2E Workflow Test Script for Gas Town
# Tests the full orchestration: Mayor -> Architect -> Planner -> Polecat -> QA

set -e

GT_DIR="/home/stevef/gt"
RIG="testgt2"

echo "=== E2E Workflow Test ==="

# 1. Start services
echo "[1] Starting GT services..."
cd $GT_DIR
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

# 2. Create the project bead and sling the architect (Fix #102).
#
# The previous version of this script mailed mayor a free-form project
# description with no associated bead. That sent the orchestration off
# the rails:
#   - Mayor's mol-mayor template assumes a project bead is already
#     attached to the routing signal it sees. No template branch
#     covers "a free-form mail arrived describing a project, please
#     create a bead and start Stage 1 yourself".
#   - The architect was still woken (probably via mayor nudging on
#     subject only) and autonomously read SPEC.md, but its hook was
#     empty, so when it ran the canonical
#       gt hook | grep -oE 'hq-wisp-[a-z0-9]+|hq-[a-z0-9]+' | head -1
#     to grab the project bead for its handoff mail, it got an empty
#     string. Its "Architecture Ready" reply then carried
#       Project bead:
#     with nothing after the colon. Mayor read it, couldn't extract a
#     bead, and (per the routing table's "DO NOTHING" guidance) failed
#     to route the next stage. The pipeline stalled at the Architect →
#     Planner hop, with planner emitting `IDLE`/`BLOCKED: no work`
#     mails and mayor mirroring empty `BLOCKED: no work` mails back.
#
# We now create the project bead up-front in HQ and `gt sling shiny
# --on <bead> testgt2/architect` directly. That gives the architect:
#   1. A real hook (so its `gt hook | grep` succeeds and its handoff
#      mail to mayor carries the real bead ID).
#   2. A real formula context (shiny == design molecule), so the
#      architect doesn't have to guess what stage it's in.
# Mayor then sees an `Architecture Ready` mail with a real `Project
# bead:` line and can sling planner with `--on <same-bead>` per its
# template.

echo "[2] Creating project bead in HQ..."
# Bead lives in HQ (hq-* prefix) so both mayor and rig agents can
# reference it by ID across the whole town. The architect & planner
# both look up the bead ID via routing (HQ → rig prefix routes).
PROJECT_BEAD=$(cd $GT_DIR && bd create \
    --title "Hello World API for testgt2" \
    --description "Implement the testgt2 project per its SPEC.md (default work item: FizzBuzz). Architecture should be designed by the architect, broken down by the planner, implemented by a polecat, and reviewed by qa. The canonical SPEC lives at /home/stevef/gt/$RIG/mayor/rig/SPEC.md and the architecture output should land at /home/stevef/gt/$RIG/architect/architecture.md." \
    --type=task --priority=1 \
    --json 2>/dev/null | python3 -c 'import json,sys; d=json.loads(sys.stdin.read()); print(d.get("id") or d.get("ID") or "")')

if [[ -z "$PROJECT_BEAD" ]]; then
    echo "FATAL: bd create did not return a bead id" >&2
    exit 1
fi
echo "    project bead: $PROJECT_BEAD"

# 3. Mail architect with project bead context (Fix #102, simpler path).
#
# The "right" thing to do is `gt sling shiny --on $PROJECT_BEAD
# $RIG/architect`, which would hook the bead onto the architect's
# work queue and let `gt hook` retrieval inside the architect work
# correctly. But two Gas Town gaps block that today:
#   - `gt sling shiny --on hq-*` fails with "bonding formula to
#     bead: exit status 1" — the shiny formula's bond clause
#     mismatches HQ-prefixed beads. Documented in fixes_status.txt
#     as a Go-level follow-up.
#   - `gt hook <bead> <target>` then fails to fork its `bd list`
#     subprocess with "no such file or directory" despite bd
#     existing at /home/stevef/.local/bin/bd — also a Go-level
#     bug to investigate.
#
# Until those land, we fall back to sending the architect a mail
# carrying `Project bead: $PROJECT_BEAD` explicitly. The architect
# template tells it to grep its mail body for `Project bead:` (the
# same line it puts in its OWN handoff mail to mayor) so this works
# without a hooked bead. The mayor mail also carries the bead so
# mayor's "find project bead in architect's reply" step succeeds
# when the architect echoes the ID back.

echo "[3] Mailing architect with project context..."
gt mail send "$RIG/architect" -s "Project: testgt2 FizzBuzz" --stdin <<ARCHMAIL
Project bead: $PROJECT_BEAD
Rig: $RIG
Spec: /home/stevef/gt/$RIG/mayor/rig/SPEC.md

You have been assigned this project. Read the SPEC at the path above,
design the architecture, and write it to
\`/home/stevef/gt/$RIG/architect/architecture.md\` (and mirror to
\`/home/stevef/gt/$RIG/architecture.md\`).

When done, mail mayor:
  gt mail send mayor/ -s "Architecture Ready" -m \\
    "Project bead: $PROJECT_BEAD"\$'\n'"Design complete. ..."

It is **critical** that the \`Project bead: $PROJECT_BEAD\` line is
in your reply — mayor uses it to route the next stage (planner).
ARCHMAIL
gt nudge "$RIG/architect" "Project assigned, check your inbox"

# Also notify mayor so it's primed to handle the eventual
# `Architecture Ready` reply from the architect.
gt mail send mayor/ -s "Project: testgt2 FizzBuzz (bead: $PROJECT_BEAD)" --stdin <<MAYORMAIL
A new project has been started.

Project bead: $PROJECT_BEAD
Rig: $RIG
SPEC: /home/stevef/gt/$RIG/mayor/rig/SPEC.md

Stage 1 (Design) has been dispatched to $RIG/architect via mail —
do NOT re-dispatch. When you see "Architecture Ready" from
$RIG/architect, extract the \`Project bead:\` line from its mail
body and run:

    gt sling mol-idea-to-plan --on $PROJECT_BEAD planner

per your routing table. Standby.
MAYORMAIL
gt nudge mayor "Monitor: architecture in progress for $PROJECT_BEAD"

# 4. Monitor flow
echo "[4] Monitoring flow..."
for i in {1..20}; do
    sleep 15
    echo "=== Check $i ==="
    echo "--- Mayor hook ---"
    gt hook show mayor 2>/dev/null || echo "empty"
    echo "--- Architect hook ---"
    gt hook show $RIG/architect 2>/dev/null || echo "empty"
    echo "--- Planner hook ---"
    gt hook show $RIG/planner 2>/dev/null || echo "empty"
done

# 5. Check final state
echo "[5] Final state..."
gt status
ls -la $GT_DIR/$RIG/architect/architecture.md 2>/dev/null || echo "No architecture.md"

echo "=== Test Complete ==="