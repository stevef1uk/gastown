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

# 2. Send project to Mayor (use different subject to create new bead)
echo "[2] Sending project to Mayor..."
gt mail send mayor/ -s "Project: Hello World API for testgt2" --stdin <<'EOF'
Please implement this project in testgt2 rig.

## Workflow:
1. Sling to Architect for design (--formula shiny)
2. Wait for Architect handoff
3. Sling to Planner for task breakdown (--formula mol-idea-to-plan)
4. Wait for Planner handoff  
5. Sling to Polecat for implementation (--formula mol-polecat-work --create)
6. After polecat completes, sling to QA for review

Full spec is in testgt2/mayor/rig/SPEC.md
EOF

# 3. Nudge mayor to check mail
echo "[3] Nudging mayor to check mail..."
gt nudge mayor "Check your mail - new project assigned"

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