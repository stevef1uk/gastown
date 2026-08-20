#!/bin/bash
set -euo pipefail

# Test script for req-flow: creates a rig with a self-contained simple
# REQUIREMENTS.md and starts the req-flow pipeline.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GASTOWN_DIR="$(dirname "$SCRIPT_DIR")"

GT_ROOT="${GT_ROOT:-$HOME/gt}"
REPO_DIR="/tmp/req_flow_test_repo"
RIG_NAME="req_flow_rig"

resolve_repo_dir() {
  rm -rf "$REPO_DIR"
  mkdir -p "$REPO_DIR"
  (cd "$REPO_DIR" && pwd -P)
}

echo "=== 1. Creating clean local repository with REQUIREMENTS.md ==="
REPO_DIR="$(resolve_repo_dir)"
cd "$REPO_DIR"
git init -b main

cat > REQUIREMENTS.md << 'EOF'
# Hello World API

Build a simple HTTP server with one endpoint:

- `GET /hello` returns `{"message": "Hello, World!"}`

The server must listen on port 8080. Write it in Go using the standard library only (no frameworks).

Provide a unit test for the handler.
EOF

git add REQUIREMENTS.md
git commit -m "Simple requirements for req-flow test"

RIG_URL="file://${REPO_DIR}"
echo "  Repo: $REPO_DIR"
echo "  Rig URL: $RIG_URL"

echo "=== 2. Resetting town and rig ==="
cd "$GASTOWN_DIR"
export GT_ROOT
export RIG_NAME="$RIG_NAME"
export RIG_URL
export DOCTOR_RESTART_SESSIONS=1
# Tell reset script to use --orchestrator-only, but NOT to auto-start rig-flow
export GT_UP_FLAGS="--orchestrator-only"

bash "$GASTOWN_DIR/scripts/reset-town-and-rig.sh"

echo "=== 3. Starting req-flow workflow ==="
cd "$GT_ROOT"
echo "  Waiting for orchestrator MCP..."
sleep 5

if gt orchestrator status 2>&1 | grep -Fq 'MCP ping OK'; then
    echo "  Orchestrator ready"
else
    echo "  Orchestrator not ready yet — trying gt up..."
    gt up --orchestrator-only 2>/dev/null || true
    sleep 10
fi

gt mayor workflow start req-flow --rig "$RIG_NAME"
echo "  Workflow started!"

# Wait for workflow-profile.json to be created by spec-index after spec_review
echo "=== Waiting for workflow-profile.json ==="
deadline=$((SECONDS + 120))
while (( SECONDS < deadline )); do
  if [[ -f "$GT_ROOT/$RIG_NAME/mayor/rig/.gastown/workflow-profile.json" ]]; then
    echo "  workflow-profile.json created!"
    break
  fi
  if ! (cd "$GT_ROOT" && gt orchestrator status 2>&1) | grep -Fq 'MCP ping OK'; then
    echo "  Orchestrator not ready, waiting..."
    gt up --orchestrator-only 2>/dev/null || true
    sleep 5
  fi
  sleep 2
done
if [[ ! -f "$GT_ROOT/$RIG_NAME/mayor/rig/.gastown/workflow-profile.json" ]]; then
  echo "WARN: workflow-profile.json not created within 120s — run 'gt rig spec-index $RIG_NAME --force' manually"
fi

echo "=== 4. Monitoring ==="
echo ""
echo "Watch the pipeline with:"
echo "  cd $GT_ROOT"
echo "  gt mayor workflow status"
echo "  tail -f logs/orchestrator.log"
echo ""
echo "Watch individual roles:"
echo "  tail -f $RIG_NAME/analyst/typescript    # analysis"
echo "  tail -f $RIG_NAME/qa/typescript          # spec_review + qa_review"
echo "  tail -f $RIG_NAME/architect/typescript   # design"
echo "  tail -f planner/typescript               # planning"
echo "  tail -f $RIG_NAME/setup/typescript       # project_setup"
echo "  tail -f $RIG_NAME/polecat/typescript     # implementation"
echo ""
echo "Agent console (recommended):"
echo "  gt-agent-console"
echo ""
echo "=== Done! ==="
echo "  Town: $GT_ROOT"
echo "  Rig:  $RIG_NAME"
echo "  Workflow: req-flow"
