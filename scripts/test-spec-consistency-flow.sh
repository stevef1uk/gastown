#!/usr/bin/env bash
set -euo pipefail

# ==============================================================================
# test-spec-consistency-flow.sh
#
# Sets up a safe, standalone test rig with an intentionally inconsistent SPEC.md
# to verify the new rig-flow intake stages:
#   kickoff (Mayor) -> spec_analysis (Analyst) -> spec_review (QA) -> design (Architect)
#
# Safety guarantees:
#   - Does NOT touch or disrupt active workflows (like wf-3 in testgt4).
#   - Does NOT delete or wipe the Gas Town root ($GT_ROOT).
# ==============================================================================

GT_ROOT="${GT_ROOT:-/home/stevef/gt}"
RIG_NAME="spec_consistency_test"
START_WORKFLOW="false"
SPEC_MODE="inconsistent"

for arg in "$@"; do
    case "$arg" in
        --start)
            START_WORKFLOW="true"
            ;;
        --clean|--correct|--valid)
            SPEC_MODE="correct"
            ;;
        --help|-h)
            echo "Usage: $0 [rig_name] [--clean|--correct] [--start]"
            echo ""
            echo "Options:"
            echo "  --clean, --correct  Write a fully consistent, valid SPEC.md to test straight-through execution"
            echo "  --start             Automatically start/reset the workflow after setup"
            echo "  rig_name            Target rig (default: spec_consistency_test)"
            exit 0
            ;;
        *)
            if [[ "$arg" != --* ]]; then
                RIG_NAME="$arg"
            fi
            ;;
    esac
done

if [[ "${RIG_NAME}" == "testgt4" ]]; then
    echo "ERROR: Refusing to use 'testgt4' as test rig. An active workflow is running there." >&2
    exit 1
fi

RIG_DIR="${GT_ROOT}/${RIG_NAME}/mayor/rig"

echo "=== [1/4] Preparing test rig: ${RIG_NAME} ==="
echo "Rig directory: ${RIG_DIR}"
mkdir -p "${RIG_DIR}"

if [[ "${SPEC_MODE}" == "correct" ]]; then
    echo "=== [2/4] Writing clean, self-consistent SPEC.md (straight-through mode) ==="
    cat > "${RIG_DIR}/SPEC.md" << 'EOF'
# Sample Trading Bot Specification

## 1. Overview
A lightweight sample service providing market price quotes and trade execution.

## 2. Backend Service
The backend service is written in Python with FastAPI.
- Server runs on port 8080.
- Launch command:
```bash
uvicorn app.main:app --host 0.0.0.0 --port 8080
```
- Health endpoint: `GET /health` returns `{"status": "ok"}` on port 8080.

## 3. Frontend Client
The frontend client connects to the backend API.
- Environment configuration:
```env
NEXT_PUBLIC_API_URL=http://localhost:8080
```
- Client queries `http://localhost:8080/health` to confirm server status.
Frontend delivery is out of scope for Phase 1; Section 3 documents the API integration contract only.

## 4. Delivery Phases
### Phase 1: Foundation
- Scope: Initial backend health service and unit tests.
- Required files:
  - `app/main.py`
  - `tests/test_api.py`
- QA verify command:
```bash
python3 -m pytest tests/test_api.py
```
EOF
else
    echo "=== [2/4] Writing intentionally inconsistent SPEC.md (audit & pause mode) ==="
    # This SPEC contains 2 deliberate internal contradictions:
    # 1. Port contradiction: §2 backend runs on port 8080, but §3 frontend expects 3000.
    # 2. Test path contradiction: §4 lists tests/test_api.py, but QA verify runs tests/test_main.py.
    cat > "${RIG_DIR}/SPEC.md" << 'EOF'
# Sample Trading Bot Specification

## 1. Overview
A lightweight sample service providing market price quotes and trade execution.

## 2. Backend Service
The backend service is written in Python with FastAPI.
- Server runs on port 8080.
- Launch command:
```bash
uvicorn app.main:app --host 0.0.0.0 --port 8080
```
- Health endpoint: `GET /health` returns `{"status": "ok"}` on port 8080.

## 3. Frontend Client
The frontend client connects to the backend API.
- Environment configuration:
```env
NEXT_PUBLIC_API_URL=http://localhost:3000
```
- Client queries `http://localhost:3000/health` to confirm server status.

## 4. Delivery Phases
### Phase 1: Foundation
- Scope: Initial backend health service and unit tests.
- Required files:
  - `app/main.py`
  - `tests/test_api.py`
- QA verify command:
```bash
python3 -m pytest tests/test_main.py
```
EOF
fi

# Initialize git repository in mayor/rig if not already present
if [ ! -d "${RIG_DIR}/.git" ]; then
    (cd "${RIG_DIR}" && git init -q && git config user.name "Mayor" && git config user.email "mayor@gastown.local" && git add SPEC.md && git commit -q -m "Initial inconsistent SPEC.md")
else
    (cd "${RIG_DIR}" && git add SPEC.md && git commit -q -m "Initial inconsistent SPEC.md" || true)
fi

echo "=== [3/4] Registering rig in Gas Town ==="
if ! grep -q "\"${RIG_NAME}\"" "${GT_ROOT}/settings/rigs.json" 2>/dev/null; then
    echo "Registering rig with: gt rig add ${RIG_NAME} --adopt --force"
    GT_ROOT="${GT_ROOT}" gt rig add "${RIG_NAME}" --adopt --force 2>/dev/null || true
fi

echo "=== [4/4] Setup complete! ==="
echo ""
echo "Logging and audit locations:"
echo "  1. Live Orchestrator FSM transitions:"
echo "     tail -f ${GT_ROOT}/logs/orchestrator.log | grep -E '${RIG_NAME}|spec_analysis|spec_review'"
echo ""
echo "  2. Dedicated on-disk SPEC Consistency Audit Log (created during audit):"
echo "     cat ${RIG_DIR}/SPEC_AUDIT.md"
echo ""
echo "  3. Analyst inspection and findings log:"
echo "     tail -f ${GT_ROOT}/logs/sessions/*-${RIG_NAME}-analyst.log"
echo ""
echo "  4. QA review and patch application log:"
echo "     tail -f ${GT_ROOT}/logs/sessions/*-${RIG_NAME}-qa.log"
echo ""
echo "  5. Verified SPEC.md patch diff (changes applied by QA):"
echo "     git -C ${RIG_DIR} diff SPEC.md"
echo ""
echo "To start the workflow:"
echo "  GT_ROOT=${GT_ROOT} gt mayor workflow start rig-flow --rig ${RIG_NAME}"
echo ""
echo "To inspect progress:"
echo "  GT_ROOT=${GT_ROOT} gt mayor workflow status"

if [[ "${START_WORKFLOW}" == "true" ]]; then
    echo ""
    echo "=== [Auto-Start] Launching or resetting rig-flow on ${RIG_NAME} ==="
    EXISTING_WF=$(GT_ROOT="${GT_ROOT}" gt mayor workflow list 2>/dev/null | grep "rig=${RIG_NAME}" | awk '{print $1}' | head -n 1 || true)
    if [[ -n "${EXISTING_WF}" ]]; then
        echo "Found existing workflow ${EXISTING_WF} for rig ${RIG_NAME}. Resetting to kickoff..."
        GT_ROOT="${GT_ROOT}" gt mayor workflow reset "${EXISTING_WF}" --to kickoff
    else
        echo "Starting new rig-flow workflow for ${RIG_NAME}..."
        GT_ROOT="${GT_ROOT}" gt mayor workflow start rig-flow --rig "${RIG_NAME}"
    fi
    echo "Workflow launched! Tracking status..."
    GT_ROOT="${GT_ROOT}" gt mayor workflow status
fi
