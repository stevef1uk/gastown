#!/usr/bin/env bash
#
# clean-gastown.sh — Nuclear reset for Gas Town workspaces
#
# Usage: ./clean-gastown.sh [options] [town-root]
#
# Options:
#   -f, --force    Skip confirmation prompts (DANGEROUS)
#   -h, --help     Show this help message
#
# If town-root is omitted, searches upward for config.json.
#

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# ─── Helpers ─────────────────────────────────────────────────────────

log_info()  { echo -e "${BLUE}[INFO]${NC}  $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_ok()    { echo -e "${GREEN}[OK]${NC}    $*"; }
log_fatal() { echo -e "${RED}[FATAL]${NC} $*" >&2; }

confirm() {
    local prompt="$1"
    echo -ne "${YELLOW}${prompt}${NC} [y/N] "
    read -r reply
    [[ "$reply" =~ ^[Yy]$ ]]
}

find_town_root() {
    local dir="${1:-$(pwd)}"
    dir="$(cd "$dir" && pwd)"

    while [[ "$dir" != "/" ]]; do
        if [[ -f "$dir/config.json" ]]; then
            echo "$dir"
            return 0
        fi
        dir="$(dirname "$dir")"
    done
    return 1
}

# ─── Parse arguments ─────────────────────────────────────────────────

FORCE=false
TOWN_ROOT=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        -f|--force)
            FORCE=true
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [options] [town-root]"
            echo ""
            echo "Nuclear reset for Gas Town workspaces. Deletes all data, rigs, and state."
            echo ""
            echo "Options:"
            echo "  -f, --force    Skip confirmation prompts (DANGEROUS)"
            echo "  -h, --help     Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0 ~/gt              # Interactive reset with previews"
            echo "  $0 --force ~/gt      # Non-interactive reset"
            exit 0
            ;;
        -*)
            log_fatal "Unknown option: $1"
            exit 1
            ;;
        *)
            TOWN_ROOT="$1"
            shift
            ;;
    esac
done

# ─── Main ────────────────────────────────────────────────────────────

if [[ -z "$TOWN_ROOT" ]]; then
    TOWN_ROOT="$(find_town_root 2>/dev/null || true)"
fi

if [[ -z "$TOWN_ROOT" || ! -f "$TOWN_ROOT/config.json" ]]; then
    log_fatal "Could not find a Gas Town workspace (no config.json found)."
    log_fatal "Usage: $0 [path/to/town]"
    exit 1
fi

TOWN_ROOT="$(cd "$TOWN_ROOT" && pwd)"
TOWN_NAME="$(basename "$TOWN_ROOT")"

echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║          GAS TOWN NUCLEAR RESET SCRIPT                       ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""
log_info "Target town: ${YELLOW}${TOWN_ROOT}${NC}"
echo ""

# ─── Phase 0: Discovery ──────────────────────────────────────────────

# Find running processes
AGENT_PIDS=($(pgrep -f "gt-agent" 2>/dev/null || true))
NATS_PIDS=($(pgrep -f "nats-wrapper" 2>/dev/null || true))
DAEMON_PID=""
if [[ -f "$TOWN_ROOT/daemon/daemon.pid" ]]; then
    DAEMON_PID="$(cat "$TOWN_ROOT/daemon/daemon.pid" 2>/dev/null || true)"
fi

# Find rig directories from rigs.json
RIG_NAMES=()
if [[ -f "$TOWN_ROOT/rigs.json" ]]; then
    # Simple JSON extraction - look for rig keys
    RIG_NAMES=($(cat "$TOWN_ROOT/rigs.json" | grep -oP '"\K[^"]+(?=":\s*\{)' 2>/dev/null || true))
fi

# Find agent state files
AGENT_STATES=($(find "$TOWN_ROOT" -maxdepth 3 -name "gt-agent-state.json" 2>/dev/null || true))

# Count Dolt issues
DOLT_ISSUES=0
DOLT_WISPS=0
if command -v dolt &>/dev/null && [[ -d "$TOWN_ROOT/.dolt-data/hq" ]]; then
    DOLT_ISSUES=$(cd "$TOWN_ROOT/.dolt-data/hq" && dolt sql -q "SELECT COUNT(*) FROM issues;" 2>/dev/null | tail -1 || echo "0")
    DOLT_WISPS=$(cd "$TOWN_ROOT/.dolt-data/hq" && dolt sql -q "SELECT COUNT(*) FROM wisps;" 2>/dev/null | tail -1 || echo "0")
fi

# ─── Phase 1: Preview ────────────────────────────────────────────────

echo "────────────────────────────────────────────────────────────────"
echo "  PREVIEW OF WHAT WILL BE DELETED"
echo "────────────────────────────────────────────────────────────────"
echo ""

echo "  Running processes to stop:"
if [[ ${#AGENT_PIDS[@]} -gt 0 ]]; then
    echo "    • gt-agent processes: ${#AGENT_PIDS[@]}"
fi
if [[ ${#NATS_PIDS[@]} -gt 0 ]]; then
    echo "    • nats-wrapper processes: ${#NATS_PIDS[@]}"
fi
if [[ -n "$DAEMON_PID" ]]; then
    echo "    • daemon (PID $DAEMON_PID)"
fi
if [[ ${#AGENT_PIDS[@]} -eq 0 && ${#NATS_PIDS[@]} -eq 0 && -z "$DAEMON_PID" ]]; then
    echo "    • (none found)"
fi

echo ""
echo "  Databases to reset:"
echo "    • .dolt-data/  (issues: ~${DOLT_ISSUES}, wisps: ~${DOLT_WISPS})"

echo ""
echo "  Rig directories to delete:"
if [[ ${#RIG_NAMES[@]} -gt 0 ]]; then
    for rig in "${RIG_NAMES[@]}"; do
        if [[ -d "$TOWN_ROOT/$rig" ]]; then
            echo "    • ${rig}/"
        fi
    done
else
    echo "    • (none found in rigs.json)"
fi

echo ""
echo "  Agent state files to delete:"
if [[ ${#AGENT_STATES[@]} -gt 0 ]]; then
    for f in "${AGENT_STATES[@]}"; do
        rel="${f#$TOWN_ROOT/}"
        echo "    • ${rel}"
    done
else
    echo "    • (none found)"
fi

echo ""
echo "  Other artifacts to delete:"
[[ -d "$TOWN_ROOT/.beads" ]]        && echo "    • .beads/         (beads local cache)"
[[ -d "$TOWN_ROOT/.runtime" ]]      && echo "    • .runtime/       (nudge queues, locks)"
[[ -d "$TOWN_ROOT/.gt-nats-pids" ]] && echo "    • .gt-nats-pids/  (session PID files)"
[[ -d "$TOWN_ROOT/logs" ]]          && echo "    • logs/           (session wrapper logs)"
[[ -f "$TOWN_ROOT/.events.jsonl" ]] && echo "    • .events.jsonl   (event log)"
[[ -f "$TOWN_ROOT/gt-agent-state.json" ]] && echo "    • gt-agent-state.json (root agent state)"

echo ""
echo "  Files to RESET (keep file, empty contents):"
[[ -f "$TOWN_ROOT/rigs.json" ]] && echo "    • rigs.json       (will become '{}')"

echo ""
echo "  Files PRESERVED (never touched):"
echo "    • config.json     (town configuration)"
echo "    • settings/       (town settings)"
echo "    • .git/           (git repository)"
echo "    • CLAUDE.md / AGENTS.md"
echo ""

echo "────────────────────────────────────────────────────────────────"
echo ""

if [[ "$FORCE" != true ]]; then
    if ! confirm "⚠️  Are you sure you want to DELETE all of the above?"; then
        log_info "Aborted by user. Nothing was deleted."
        exit 0
    fi

    # Double-confirm for destructive rig deletion
    if [[ ${#RIG_NAMES[@]} -gt 0 ]]; then
        echo ""
        if ! confirm "⚠️  REALLY sure? This will DELETE rig directories including any SPEC.md, crew workspaces, etc."; then
            log_info "Aborted by user. Nothing was deleted."
            exit 0
        fi
    fi
else
    log_warn "--force specified: skipping confirmation prompts"
fi

echo ""
log_info "Starting nuclear reset..."
echo ""

# ─── Phase 2: Stop Processes ─────────────────────────────────────────

log_info "Stopping processes..."

if [[ ${#AGENT_PIDS[@]} -gt 0 ]]; then
    for pid in "${AGENT_PIDS[@]}"; do
        kill "$pid" 2>/dev/null || true
    done
    sleep 1
    # Force kill any stragglers
    for pid in "${AGENT_PIDS[@]}"; do
        kill -9 "$pid" 2>/dev/null || true
    done
    log_ok "Stopped ${#AGENT_PIDS[@]} gt-agent process(es)"
fi

if [[ ${#NATS_PIDS[@]} -gt 0 ]]; then
    for pid in "${NATS_PIDS[@]}"; do
        kill -9 "$pid" 2>/dev/null || true
    done
    log_ok "Stopped ${#NATS_PIDS[@]} nats-wrapper process(es)"
fi

if [[ -n "$DAEMON_PID" ]]; then
    kill "$DAEMON_PID" 2>/dev/null || true
    sleep 1
    kill -9 "$DAEMON_PID" 2>/dev/null || true
    rm -f "$TOWN_ROOT/daemon/daemon.pid"
    log_ok "Stopped daemon (PID $DAEMON_PID)"
fi

# Stop dolt if running
DOLT_PID=""
if [[ -f "$TOWN_ROOT/.dolt-data/.dolt/dolt.pid" ]]; then
    DOLT_PID="$(cat "$TOWN_ROOT/.dolt-data/.dolt/dolt.pid" 2>/dev/null || true)"
fi
if [[ -n "$DOLT_PID" ]]; then
    kill "$DOLT_PID" 2>/dev/null || true
    sleep 1
    kill -9 "$DOLT_PID" 2>/dev/null || true
    log_ok "Stopped dolt server (PID $DOLT_PID)"
fi

# ─── Phase 3: Delete Rig Directories ─────────────────────────────────

log_info "Deleting rig directories..."
DELETED_RIGS=0
for rig in "${RIG_NAMES[@]}"; do
    rig_dir="$TOWN_ROOT/$rig"
    if [[ -d "$rig_dir" ]]; then
        rm -rf "$rig_dir"
        log_ok "Deleted rig directory: ${rig}/"
        ((DELETED_RIGS++)) || true
    fi
done
if [[ $DELETED_RIGS -eq 0 ]]; then
    log_warn "No rig directories found to delete"
fi

# ─── Phase 4: Delete Dolt Databases ──────────────────────────────────

log_info "Resetting Dolt databases..."
if [[ -d "$TOWN_ROOT/.dolt-data" ]]; then
    rm -rf "$TOWN_ROOT/.dolt-data"
    log_ok "Deleted .dolt-data/ (all databases)"
else
    log_warn "No .dolt-data/ found"
fi

# ─── Phase 5: Delete Beads Cache ─────────────────────────────────────

log_info "Clearing beads cache..."
if [[ -d "$TOWN_ROOT/.beads" ]]; then
    rm -rf "$TOWN_ROOT/.beads"
    log_ok "Deleted .beads/"
else
    log_warn "No .beads/ found"
fi

# ─── Phase 6: Delete Agent State Files ───────────────────────────────

log_info "Deleting agent state files..."
DELETED_STATES=0
for f in "${AGENT_STATES[@]}"; do
    if [[ -f "$f" ]]; then
        rm -f "$f"
        rel="${f#$TOWN_ROOT/}"
        log_ok "Deleted ${rel}"
        ((DELETED_STATES++)) || true
    fi
done
if [[ $DELETED_STATES -eq 0 ]]; then
    log_warn "No agent state files found"
fi

# ─── Phase 7: Delete Logs and Events ─────────────────────────────────

log_info "Deleting logs and events..."

if [[ -d "$TOWN_ROOT/logs" ]]; then
    rm -rf "$TOWN_ROOT/logs"
    log_ok "Deleted logs/"
fi

if [[ -f "$TOWN_ROOT/.events.jsonl" ]]; then
    rm -f "$TOWN_ROOT/.events.jsonl"
    log_ok "Deleted .events.jsonl"
fi

if [[ -f "$TOWN_ROOT/.events.jsonl.lock" ]]; then
    rm -f "$TOWN_ROOT/.events.jsonl.lock"
    log_ok "Deleted .events.jsonl.lock"
fi

# ─── Phase 8: Delete Runtime Artifacts ───────────────────────────────

log_info "Deleting runtime artifacts..."

if [[ -d "$TOWN_ROOT/.runtime" ]]; then
    rm -rf "$TOWN_ROOT/.runtime"
    log_ok "Deleted .runtime/"
fi

if [[ -d "$TOWN_ROOT/.gt-nats-pids" ]]; then
    rm -rf "$TOWN_ROOT/.gt-nats-pids"
    log_ok "Deleted .gt-nats-pids/"
fi

if [[ -f "$TOWN_ROOT/gt-agent-state.json" ]]; then
    rm -f "$TOWN_ROOT/gt-agent-state.json"
    log_ok "Deleted gt-agent-state.json"
fi

# Delete any session wrapper logs in town root
find "$TOWN_ROOT" -maxdepth 1 -name "*.wrapper.log" -delete 2>/dev/null || true

# ─── Phase 9: Reset rigs.json ────────────────────────────────────────

log_info "Resetting rigs.json..."
if [[ -f "$TOWN_ROOT/rigs.json" ]]; then
    echo '{"version": 1, "rigs": {}}' > "$TOWN_ROOT/rigs.json"
    log_ok "Reset rigs.json (emptied rigs list)"
fi

# ─── Phase 10: Delete daemon state ───────────────────────────────────

if [[ -d "$TOWN_ROOT/daemon" ]]; then
    rm -rf "$TOWN_ROOT/daemon"
    log_ok "Deleted daemon/"
fi

# ─── Phase 11: Clean role directories ────────────────────────────────

log_info "Cleaning role directories..."
for role_dir in mayor deacon; do
    if [[ -d "$TOWN_ROOT/$role_dir" ]]; then
        # Only delete runtime/state contents, preserve configs
        find "$TOWN_ROOT/$role_dir" -mindepth 1 -maxdepth 1 \
            ! -name "town.json" \
            ! -name "rigs.json" \
            ! -name "overseer.json" \
            ! -name "daemon.json" \
            -exec rm -rf {} + 2>/dev/null || true
        log_ok "Cleaned ${role_dir}/ (preserved configs)"
    fi
done

# ─── Summary ─────────────────────────────────────────────────────────

echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║                    RESET COMPLETE                            ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""
log_ok "Town '${TOWN_NAME}' has been nuclear-reset."
echo ""
echo "  Remaining structure:"
find "$TOWN_ROOT" -maxdepth 1 -type d | sort | sed "s|^$TOWN_ROOT/|    📁 |" | sed "s|^$TOWN_ROOT$|    📁 ${TOWN_NAME}/ (town root)|"
echo ""
log_warn "IMPORTANT: Services were stopped during cleanup."
echo "  Run the following commands to restart Gas Town:"
echo ""
echo "    cd ${TOWN_ROOT}"
echo "    ${YELLOW}gt up${NC}                    # Start NATS, Dolt, daemon, and all agents"
echo "    ${YELLOW}gt doctor${NC}                # Verify everything is healthy"
echo ""
echo "  Note: If agents were repeatedly crashing before cleanup, the daemon"
echo "  may have them in exponential backoff. 'gt up' will restart them cleanly."
echo ""
log_info "Next steps:"
echo "  1. Run '${YELLOW}gt rig add <name> <git-url>${NC}' to create new rigs"
echo "  2. Run '${YELLOW}gt mountain <epic>${NC}' to start building"
echo ""
