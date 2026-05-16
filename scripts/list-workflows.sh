#!/usr/bin/env bash
#
# list-workflows.sh — table view of orchestrator workflow instances
#
# Reads ~/gt/orchestrator/instances.json (offline; does not require orchestrator MCP).
#
# Usage:
#   ./scripts/list-workflows.sh
#   ./scripts/list-workflows.sh --rig testgt1
#   GT_ROOT=~/gt ./scripts/list-workflows.sh --status active
#   ./scripts/list-workflows.sh --json
#
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$SCRIPT_DIR/workflow_instances.py" list "$@"
