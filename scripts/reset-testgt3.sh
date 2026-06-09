#!/bin/bash
# Reset testgt3 rig to just SPEC.md — ready for a fresh rig-flow run.
set -euo pipefail

RIG_DIR="${GT_ROOT:-$HOME/gt}/testgt3/mayor/rig"

if [ ! -d "$RIG_DIR" ]; then
    echo "ERROR: $RIG_DIR not found"
    exit 1
fi

echo "Resetting $RIG_DIR to initial state (SPEC.md only)..."

cd "$RIG_DIR"

# 1. Remove everything except SPEC.md and .git
find . -mindepth 1 -maxdepth 1 \
    ! -name '.git' \
    ! -name 'SPEC.md' \
    -exec rm -rf {} +

# 2. Reset git to a single commit with just SPEC.md
git add -A
git commit --allow-empty -m "chore: reset for fresh rig-flow run" || true

# Remove old history — squash everything into one commit
BASE=$(git rev-list --max-parents=0 HEAD)
if [ -n "$BASE" ]; then
    git reset --soft "$BASE"
    git commit --amend -m "chore: initial SPEC.md for fresh rig-flow run" --no-edit
fi

# 3. Force push to remote
if git remote get-url origin >/dev/null 2>&1; then
    git push --force origin main
    echo "Pushed reset to origin/main"
fi

echo "Done. Rig is reset to SPEC.md only."
echo "Run: gt rig spec-index testgt3 --profiled to regenerate the workflow profile, then gt down && gt up --orchestrator-only"
