#!/bin/bash
set -euo pipefail

# Test Playwright Integration - Create new rig and verify Playwright E2E works
# Assumes gt is already installed and running
# Platform-independent (Mac/Linux)

set -x

GASTOWN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GT_ROOT="${GT_ROOT:-$HOME/gt}"
REPO_DIR="/tmp/playwright-new-rig-test"
RIG_NAME="pwtest"

# Platform detection
UNAME_S="$(uname -s)"
case "${UNAME_S}" in
    Darwin*)    PLATFORM="mac";;
    Linux*)     PLATFORM="linux";;
    *)          PLATFORM="unknown";;
esac

cleanup() {
    echo "=== Cleanup ==="
    # DISABLED: this cleanup wiped the pwtest rig on timeout. The rig and its
    # workspace must be preserved for post-mortem; remove manually if needed.
    # cd "${HOME}/gt" 2>/dev/null || true
    # if gt rig list --json 2>/dev/null | grep -q '"name": "pwtest"'; then
    #     gt rig remove pwtest --force 2>/dev/null || true
    # fi
    # if [ -d "${HOME}/gt/pwtest" ]; then
    #     echo "Removing rig directory: ${HOME}/gt/pwtest"
    #     rm -rf "${HOME}/gt/pwtest"
    # fi
    rm -rf "${REPO_DIR}"
}

# Clean up persisted workflow instances from previous runs so the test starts fresh.
instances_json="${HOME}/gt/orchestrator/instances.json"
if [ -f "$instances_json" ]; then
    rm -f "$instances_json"
    echo "Removed stale ${instances_json}"
fi

trap cleanup EXIT

echo "=== 1. Create clean test repo ==="
rm -rf "${REPO_DIR}"
mkdir -p "${REPO_DIR}"
cd "${REPO_DIR}"
git init -b main
# Simple SPEC - trivial Go web server + Playwright E2E
cat > SPEC.md <<'SPECEOF'
# Ping App - Trivial Go Web Server

## Goal
A tiny Go HTTP server with one endpoint. Tests run via Playwright in Docker.

## Architecture
- Go 1.22, single main.go
- Static HTML/JS served from /
- **Web server runs on HOST; Playwright E2E tests via Docker compose, reaching the host through host.docker.internal**

## HTTP API
- GET /ping → 200 JSON {"message": "pong"}

## Web UI
- Single page at / with "Hello" and a button
- Button click changes text

## Delivery Phases
1. **go-module** - Initialize go.mod
2. **core** - main.go with /ping handler
3. **web** - index.html + app.js
4. **integration-test** - Playwright E2E via Docker compose (Playwright container reaches host via host.docker.internal)

## Layout
**layout_root: pingapp**

```
pingapp/
├── go.mod
├── cmd/server/main.go
├── web/index.html
├── web/app.js
├── playwright.config.ts
├── e2e/ping.spec.ts
├── package.json
└── docker-compose.yml
```

## Testing
- Unit: `go test ./...`
- E2E: `docker compose up --exit-code-from playwright`
SPECEOF

git add SPEC.md
git commit -m 'Initial spec'

RIG_URL="file://${REPO_DIR}"
echo "  Spec repo: ${REPO_DIR}"
echo "  Rig URL:   ${RIG_URL}"

echo "=== 2. Bring up town services (Dolt + orchestrator) ==="
cd "${HOME}/gt"
gt up --orchestrator-only

echo "=== 3. Add rig to Gas Town ==="
# Pre-flight: remove a stale pwtest rig AND any leftover pwtest workflows.
if gt rig list --json 2>/dev/null | grep -q '"name": "pwtest"'; then
    echo "Removing stale pwtest rig..."
    gt rig remove pwtest --force
fi
for wf in $(gt mayor workflow list 2>/dev/null | grep -o "wf-[0-9]*" | sort -u); do
    if gt mayor workflow status "$wf" 2>/dev/null | grep -q "rig=pwtest"; then
        echo "Deleting leftover pwtest workflow $wf..."
        gt mayor workflow pause "$wf" --shutdown >/dev/null 2>&1 || true
        gt mayor workflow delete "$wf" >/dev/null 2>&1 || true
    fi
done
if [ -d "${HOME}/gt/pwtest" ]; then
    rm -rf "${HOME}/gt/pwtest"
fi
gt rig add pwtest file:///tmp/playwright-new-rig-test

echo "=== 4. Run workflow ==="
# Rig add auto-starts the workflow when auto_start is enabled, so "already
# active" is expected and fine — the workflow is running either way.
if ! gt mayor workflow start rig-flow --rig pwtest 2>&1 | tee /tmp/pwtest-start.log; then
    echo "Workflow start failed" >&2
    cat /tmp/pwtest-start.log >&2
    exit 1
fi
if grep -q "already active" /tmp/pwtest-start.log; then
    echo "Workflow already active (auto-started by rig add) — continuing"
fi

echo "=== 4. Wait for workflow completion ==="
MAX_WAIT=1800
ELAPSED=0
while [ ${ELAPSED} -lt ${MAX_WAIT} ]; do
    STATUS=$(gt mayor workflow status 2>&1 | grep " rig=pwtest\b" | grep -o 'state=[a-z]*' | cut -d= -f2 | head -1 || echo "unknown")
    echo "Workflow status: ${STATUS} (elapsed: ${ELAPSED}s)"
    if [ "${STATUS}" = "completed" ]; then
        echo "Workflow completed successfully!"
        break
    fi
    if [ "${STATUS}" = "failed" ] || [ "${STATUS}" = "error" ]; then
        echo "Workflow failed!"
        exit 1
    fi
    sleep 30
    ELAPSED=$((ELAPSED + 30))
done

if [ ${ELAPSED} -ge ${MAX_WAIT} ]; then
    echo "Timeout waiting for workflow completion"
    exit 1
fi

echo "=== 5. Verify Playwright tests ran and passed ==="
cd "${HOME}/gt/pwtest/mayor/rig/pingapp"

# Ensure the shared Playwright image is present (async build may still be running)
echo "Waiting for Playwright Docker image..."
for i in $(seq 1 60); do
    if docker image inspect playwright-go-test:latest >/dev/null 2>&1; then
        echo "Playwright image present"
        break
    fi
    if [ "${i}" = "60" ]; then
        echo "ERROR: playwright-go-test:latest image never appeared (check 'docker images' / build.log)"
        exit 1
    fi
    sleep 5
done

docker compose -f docker-compose.yml up --exit-code-from playwright 2>&1 | tail -20

echo "=== SUCCESS: Playwright integration test passed! ==="
