#!/usr/bin/env bash
#
# delete-workflows.sh — remove workflow instances from orchestrator/instances.json
#
# Prefer stopping the orchestrator first (gt orchestrator stop) so in-memory state
# does not overwrite your edits.
#
# Usage:
#   ./scripts/delete-workflows.sh wf-2
#   ./scripts/delete-workflows.sh wf-1 wf-3 --dry-run
#   ./scripts/delete-workflows.sh --rig testgt1
#   ./scripts/delete-workflows.sh --completed
#   ./scripts/delete-workflows.sh --all -f
#
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$SCRIPT_DIR/workflow_instances.py" delete "$@"
