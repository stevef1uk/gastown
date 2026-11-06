#!/bin/bash
set -euo pipefail

# Test Playwright Integration - Create new rig and verify Playwright E2E works
# Assumes gt is already installed and running
# Platform-independent (Mac/Linux)

set -x

GASTOWN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GT_ROOT="${GT_ROOT:-$HOME/gt}"
REPO_DIR="/tmp/playwright-new-rig-test"

# Resolve the active proxy before modifying the rig or starting agents. Overrides:
# LLM_ENDPOINT=https://host/v1/chat/completions, or FREERIDE_PORT=9999.
LLM_ENDPOINT="$(python3 "${GASTOWN_DIR}/scripts/sync_freeride_endpoint.py" --town-root "${GT_ROOT}")"
export LLM_ENDPOINT

# Ensure git repo exists for gc
mkdir -p "${REPO_DIR}"
git -C "${REPO_DIR}" init -b main 2>/dev/null || true
git -C "${REPO_DIR}" commit --allow-empty -m "Initial repo state" 2>/dev/null || true
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

# Keep the workflow registry and its next_seq intact. The CLI cleanup below
# removes only this test rig's workflows through the running orchestrator.
# Deleting instances.json here can reuse workflow IDs (and their budget keys),
# and cannot clear an orchestrator's already-loaded in-memory workflows.

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
A tiny Go HTTP server with one endpoint. Tests run via Playwright in a Docker compose wrapper — the Go server itself runs on the host and is NOT containerized.

## Architecture
- Go 1.22, single main.go
- Static HTML/JS served from /
- **Go server runs on the HOST directly (go run pingapp/cmd/server). No Docker is needed for the Go application.**
- Playwright E2E tests run in Docker compose, reaching the host through host.docker.internal

## HTTP API
- GET /ping → 200 JSON {"message": "pong"}

## Web UI
- Single page at / with "Hello" and a button
- Button click changes text

## Delivery Phases
1. **go-module** - Initialize go.mod
2. **core** - main.go with /ping handler
3. **web** - index.html + app.js
4. **integration-test** - Playwright E2E via Docker compose (Playwright container reaches host via host.docker.internal; Go server stays on the host)

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
- E2E: `docker compose up --exit-code-from playwright` (docker-compose.yml runs ONLY the Playwright test harness container; the Go server is not containerized)
- The Go server runs on the host on port 8080. Do NOT define a Dockerfile or base image for the Go application — Docker is only the Playwright test harness.
SPECEOF

git add SPEC.md
git commit -m 'Initial spec'

RIG_URL="file://${REPO_DIR}"
echo "  Spec repo: ${REPO_DIR}"
echo "  Rig URL:   ${RIG_URL}"

echo "=== 2. Bring up town services (Dolt + orchestrator) ==="
cd "${GT_ROOT}"
gt up --orchestrator-only
# gt up leaves existing sessions running with their old environment. Both town
# workflow agents must reload the endpoint; rig agents are recreated below.
gt mayor restart
gt setup restart

echo "=== 3. Add rig to Gas Town ==="
# Select by structured rig identity, including paused runs. Capture the producer
# fully: grep -q pipelines under pipefail can skip cleanup on a broken pipe.
old_workflows="$(gt mayor workflow list --json | python3 -c '
import json,sys
for workflow in json.load(sys.stdin)["workflows"]:
    if (workflow.get("variables") or {}).get("rig") == "pwtest":
        print(workflow["id"])
')"
for wf in ${old_workflows}; do
    echo "Deleting leftover pwtest workflow ${wf}..."
    old_status="$(gt mayor workflow status "${wf}" --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["workflows"][0]["status"])')"
    if [ "${old_status}" != "completed" ] && [ "${old_status}" != "failed" ]; then
        gt mayor workflow pause "${wf}" --shutdown=false
    fi
    gt mayor workflow delete "${wf}"
done
# Stop/remove the old rig only after its workflows have stopped dispatching.
rig_snapshot="$(gt rig list --json)"
if python3 -c 'import json,sys; rigs=json.load(sys.stdin); sys.exit(0 if any(r.get("name")=="pwtest" for r in (rigs or [])) else 1)' <<< "${rig_snapshot}"; then
    echo "Removing stale pwtest rig..."
    gt rig remove pwtest --force
fi
if [ -d "${GT_ROOT}/pwtest" ]; then
    rm -rf "${GT_ROOT}/pwtest"
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

# Follow exactly the newly running instance, never the first historical row.
WORKFLOW_ID="$(gt mayor workflow list --json | python3 -c '
import json,sys
workflows=[w for w in json.load(sys.stdin)["workflows"]
           if (w.get("variables") or {}).get("rig")=="pwtest"]
running=[w for w in workflows if w.get("status") in ("running", "")]
candidates=running or workflows
if len(candidates)!=1:
    raise SystemExit("Expected exactly one new pwtest workflow, found %d candidates" % len(candidates))
print(candidates[0]["id"])
')"
echo "Following workflow ${WORKFLOW_ID}"

echo "=== 4. Wait for workflow completion ==="
MAX_WAIT=1800
ELAPSED=0
while [ ${ELAPSED} -lt ${MAX_WAIT} ]; do
    SNAPSHOT="$(gt mayor workflow status "${WORKFLOW_ID}" --json)"
    STATUS="$(python3 -c '
import json,sys
workflow=json.load(sys.stdin)["workflows"][0]
print(workflow["status"] if workflow["status"] in ("paused","failed") else workflow["current_state"])
' <<< "${SNAPSHOT}")"
    echo "Workflow ${WORKFLOW_ID} status: ${STATUS} (elapsed: ${ELAPSED}s)"
    if [ "${STATUS}" = "paused" ]; then
        python3 -c 'import json,sys; w=json.load(sys.stdin)["workflows"][0]; print("Paused because: " + (w.get("pause_reason") or "No reason recorded"))' <<< "${SNAPSHOT}"
        exit 1
    fi
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
cd "${GT_ROOT}/pwtest/mayor/rig/pingapp"

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

# Compose CLI differs by host: plugin ("docker compose") on Docker Desktop/macOS,
# standalone ("docker-compose") on many Linux installs. Detect once, use everywhere.
if docker compose version >/dev/null 2>&1; then
    DC="docker compose"
else
    DC="docker-compose"
fi
echo "Using compose CLI: ${DC}"

# Preflight: refuse to run when disk is nearly full — a full E2E cycle pulls a
# ~1.5GB Playwright image and writes node_modules; running at 100% corrupts
# builds mid-flight (GH: disk-full left a 186GB stale compose volume behind).
avail_kb=$(df -k --output=avail / | tail -1 | tr -d ' ')
if [ "${avail_kb:-0}" -lt 10485760 ]; then
    echo "ERROR: less than 10GB free on / ($(( avail_kb / 1024 / 1024 ))GB). Free space first:"
    echo "  docker system prune -af --volumes   # removes stopped + unused volumes"
    echo "  docker builder prune -af"
    exit 1
fi

# Teardown guard: always remove the compose project (containers + anonymous
# volumes) so repeated runs can never accumulate GBs in named volumes.
cleanup_compose() {
    ${DC} -f docker-compose.yml down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup_compose EXIT

# Create docker-compose.yml for Playwright E2E (not in SPEC layout).
# Uses the shared safe runner image `playwright-go-test:latest` (built once per
# machine from the embedded Dockerfile.playwright template) — NOT a raw
# mcr.microsoft.com/playwright tag, which gastown's compose validation flags.
# Bind mounts only — no named volumes, nothing persists between runs.
cat > docker-compose.yml <<'COMPOSEEOF'
services:
  playwright:
    image: playwright-go-test:latest
    user: "${DOCKER_UID:-1000}:${DOCKER_GID:-1000}"
    working_dir: /work
    volumes:
      - ./pingapp:/work
    environment:
      - BASE_URL=http://host.docker.internal:8080
    command: sh -c "npm install --ignore-scripts && npx playwright test"
    network_mode: "host"
    extra_hosts:
      - "host.docker.internal:host-gateway"
COMPOSEEOF

${DC} -f docker-compose.yml up --exit-code-from playwright 2>&1 | tail -20
RC=${PIPESTATUS[0]}
cleanup_compose

if [ "$RC" -ne 0 ]; then
    echo "=== FAILURE: Playwright integration test exited $RC ==="
    exit "$RC"
fi

echo "=== SUCCESS: Playwright integration test passed! ==="
