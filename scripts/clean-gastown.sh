#!/usr/bin/env bash
#
# clean-gastown.sh — Reset script for Gas Town workspaces
#
# Two modes:
#   1. Full town reset (default): nukes EVERYTHING — all rigs, all Dolt
#      databases (including hq), daemon, agent state. Town config is
#      preserved. Use only when starting from scratch.
#
#   2. Single rig reset (--rig=NAME): surgically removes one rig while
#      preserving the rest of the town (hq database, daemon, other rigs,
#      mayor/planner/deacon state). This is the right tool for "the
#      rig's agents got stuck in a weird state, give me a clean
#      <rig> while keeping the rest of the town".
#
# Usage: ./clean-gastown.sh [options] [town-root]
#
# Options:
#   --rig=NAME     Reset only the named rig (surgical mode).
#                  Preserves hq database, daemon, other rigs.
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
SINGLE_RIG=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        -f|--force)
            FORCE=true
            shift
            ;;
        --rig=*)
            SINGLE_RIG="${1#--rig=}"
            shift
            ;;
        --rig)
            shift
            SINGLE_RIG="${1:-}"
            shift || true
            ;;
        -h|--help)
            echo "Usage: $0 [options] [town-root]"
            echo ""
            echo "Reset Gas Town workspaces. Two modes:"
            echo "  - Full town reset (default): deletes everything."
            echo "  - Single rig reset (--rig=NAME): preserves hq + other rigs."
            echo ""
            echo "Options:"
            echo "  --rig=NAME     Reset only the named rig (surgical)."
            echo "  -f, --force    Skip confirmation prompts (DANGEROUS)"
            echo "  -h, --help     Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0 ~/gt                          # Full nuclear reset (all rigs)"
            echo "  $0 --force ~/gt                  # Same, non-interactive"
            echo "  $0 --rig=testgt2 ~/gt            # Reset only the testgt2 rig"
            echo "  $0 --rig=testgt2 --force ~/gt    # Same, non-interactive"
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

# Validate --rig name early (must be non-empty, no path components)
if [[ -n "$SINGLE_RIG" ]]; then
    if [[ "$SINGLE_RIG" == *"/"* || "$SINGLE_RIG" == *".."* ]]; then
        log_fatal "Invalid --rig value: $SINGLE_RIG (must be a plain name)"
        exit 1
    fi
fi

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

# ─── Single-rig mode (surgical) ──────────────────────────────────────
#
# When --rig=NAME is set, branch off into a targeted cleanup that
# touches only that one rig's directory, removes its registry entry,
# and drops its Dolt database — leaving the hq database, daemon,
# mayor/planner/deacon state, and any other rigs untouched.
#
# This branch terminates the script via `exit` so the full-town
# reset code below never runs in single-rig mode.

if [[ -n "$SINGLE_RIG" ]]; then
    RIG_DIR="$TOWN_ROOT/$SINGLE_RIG"

    echo ""
    echo "╔══════════════════════════════════════════════════════════════╗"
    echo "║        GAS TOWN SINGLE-RIG RESET (surgical)                  ║"
    echo "╚══════════════════════════════════════════════════════════════╝"
    echo ""
    log_info "Target town: ${YELLOW}${TOWN_ROOT}${NC}"
    log_info "Target rig:  ${YELLOW}${SINGLE_RIG}${NC}"
    echo ""

    # Verify the rig is registered or its directory exists.
    RIG_REGISTERED=false
    if [[ -f "$TOWN_ROOT/rigs.json" ]]; then
        if python3 -c "import json,sys; d=json.load(open('$TOWN_ROOT/rigs.json')); sys.exit(0 if '$SINGLE_RIG' in d.get('rigs', {}) else 1)" 2>/dev/null; then
            RIG_REGISTERED=true
        fi
    fi
    if [[ "$RIG_REGISTERED" != true && ! -d "$RIG_DIR" ]]; then
        log_fatal "Rig '$SINGLE_RIG' is not registered and has no directory at $RIG_DIR"
        log_fatal "Run 'gt rig list' to see known rigs."
        exit 1
    fi

    # Count what we're about to delete.
    RIG_DOLT_DIR="$TOWN_ROOT/.dolt-data/$SINGLE_RIG"
    HAS_DOLT_DB=false
    [[ -d "$RIG_DOLT_DIR" ]] && HAS_DOLT_DB=true

    # Detect a polecat count for the preview (best-effort).
    POLECAT_COUNT=0
    if [[ -d "$RIG_DIR/polecats" ]]; then
        POLECAT_COUNT=$(find "$RIG_DIR/polecats" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | wc -l | tr -d ' ')
    fi

    echo "────────────────────────────────────────────────────────────────"
    echo "  PREVIEW OF WHAT WILL BE DELETED (single-rig mode)"
    echo "────────────────────────────────────────────────────────────────"
    echo ""
    echo "  Rig:                  ${SINGLE_RIG}"
    echo "  Registered in rigs.json: ${RIG_REGISTERED}"
    echo "  Directory:            ${RIG_DIR}"
    [[ -d "$RIG_DIR" ]]      && echo "    → exists, will be rm -rf'd (worktrees, .repo.git, polecats, configs, .beads)"
    [[ ! -d "$RIG_DIR" ]]    && echo "    → already missing, nothing to delete on disk"
    echo "  Polecats on disk:     ${POLECAT_COUNT}"
    echo "  Dolt DB:              ${RIG_DOLT_DIR}"
    [[ "$HAS_DOLT_DB" == true ]]  && echo "    → exists, will be rm -rf'd (loses all beads, wisps, mail for this rig)"
    [[ "$HAS_DOLT_DB" != true ]]  && echo "    → no separate database directory found (nothing to delete)"
    echo ""
    echo "  PRESERVED (other rig data, town config, hq database):"
    echo "    • $TOWN_ROOT/config.json"
    echo "    • $TOWN_ROOT/.dolt-data/hq           (town beads, mayor/planner mail)"
    echo "    • $TOWN_ROOT/daemon/                  (daemon state)"
    echo "    • $TOWN_ROOT/mayor/, planner/, deacon/, mechanic/  (HQ agents)"
    echo "    • All other rig directories in $TOWN_ROOT/"
    echo ""
    echo "────────────────────────────────────────────────────────────────"
    echo ""

    if [[ "$FORCE" != true ]]; then
        if ! confirm "⚠️  Reset the '${SINGLE_RIG}' rig (delete dir + drop its Dolt DB)?"; then
            log_info "Aborted by user. Nothing was deleted."
            exit 0
        fi
    fi

    echo ""
    log_info "Starting single-rig reset for '${SINGLE_RIG}'..."

    # Step 1: ask gt to gracefully shutdown the rig's tmux/nats sessions
    # if gt is installed. Non-fatal on failure.
    if command -v gt >/dev/null 2>&1; then
        log_info "Shutting down rig sessions via 'gt rig shutdown ${SINGLE_RIG}'..."
        gt rig shutdown "$SINGLE_RIG" --force >/dev/null 2>&1 || \
            log_warn "  'gt rig shutdown' failed or rig was already stopped (continuing)"
    else
        log_warn "  gt CLI not on PATH — skipping graceful shutdown"
    fi

    # Step 2: also kill any stragglers that may have a worktree open.
    # We grep for the rig name in the process command line — narrow
    # enough to avoid hitting other rigs' agents.
    log_info "Killing any straggler processes referencing '${SINGLE_RIG}'..."
    pkill -f "gt-agent.*${SINGLE_RIG}" 2>/dev/null || true
    sleep 1
    pkill -9 -f "gt-agent.*${SINGLE_RIG}" 2>/dev/null || true

    # Step 3: unregister from rigs.json via gt rig remove (best-effort).
    # We use --force so it kills any leftover tmux sessions tied to
    # the rig.
    if command -v gt >/dev/null 2>&1 && [[ "$RIG_REGISTERED" == true ]]; then
        log_info "Unregistering rig from rigs.json..."
        if gt rig remove "$SINGLE_RIG" --force >/dev/null 2>&1; then
            log_ok "Unregistered '${SINGLE_RIG}' from $TOWN_ROOT/rigs.json"
        else
            log_warn "'gt rig remove' failed — falling back to manual rigs.json edit"
            # Manual fallback: rewrite rigs.json without this rig.
            if [[ -f "$TOWN_ROOT/rigs.json" ]]; then
                python3 -c "
import json
p = '$TOWN_ROOT/rigs.json'
d = json.load(open(p))
if 'rigs' in d and '$SINGLE_RIG' in d['rigs']:
    del d['rigs']['$SINGLE_RIG']
json.dump(d, open(p, 'w'), indent=2)
" 2>/dev/null && log_ok "Removed '${SINGLE_RIG}' from rigs.json (manual fallback)" || \
                log_warn "Could not edit rigs.json — please remove the entry by hand"
            fi
        fi
    fi

    # Step 4: delete the rig directory.
    if [[ -d "$RIG_DIR" ]]; then
        log_info "Deleting $RIG_DIR..."
        rm -rf "$RIG_DIR"
        log_ok "Deleted rig directory"
    fi

    # Step 5: drop the rig's Dolt database (rig-specific subdir only —
    # NEVER touch ../hq).
    if [[ -d "$RIG_DOLT_DIR" ]]; then
        log_info "Dropping Dolt database at $RIG_DOLT_DIR..."
        # Safety: refuse if the path doesn't end in the rig name or if
        # it resolves to hq/, the dolt-data root, or anywhere outside.
        BASENAME="$(basename "$RIG_DOLT_DIR")"
        if [[ "$BASENAME" != "$SINGLE_RIG" || "$BASENAME" == "hq" || "$BASENAME" == ".dolt-data" ]]; then
            log_fatal "Refusing to delete $RIG_DOLT_DIR (basename safety check failed)"
            exit 1
        fi
        rm -rf "$RIG_DOLT_DIR"
        log_ok "Dropped Dolt DB for '${SINGLE_RIG}' (preserved hq + other rigs)"
    fi

    # Step 6: clean any stray nudge queues, runtime locks for this rig
    # (best-effort, narrow patterns).
    if [[ -d "$TOWN_ROOT/.runtime" ]]; then
        find "$TOWN_ROOT/.runtime" -maxdepth 3 -name "*${SINGLE_RIG}*" -exec rm -rf {} + 2>/dev/null || true
    fi

    echo ""
    echo "╔══════════════════════════════════════════════════════════════╗"
    echo "║              SINGLE-RIG RESET COMPLETE                       ║"
    echo "╚══════════════════════════════════════════════════════════════╝"
    echo ""
    log_ok "Rig '${SINGLE_RIG}' has been reset."
    log_ok "Preserved: hq database, daemon, mayor/planner/deacon, other rigs."
    echo ""
    echo "  Next steps:"
    echo "    1. ${YELLOW}gt rig add ${SINGLE_RIG} <git-url>${NC}    # Re-create the rig fresh"
    echo "    2. ${YELLOW}gt up${NC}                                 # Bring services up"
    echo "    3. ${YELLOW}gt status -v${NC}                          # Verify clean state"
    echo ""
    exit 0
fi

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
    # Try python3 for robust JSON parsing, fall back to grep
    RIG_NAMES=($(python3 -c "import json; d=json.load(open('$TOWN_ROOT/rigs.json')); print(' '.join(d.get('rigs', {}).keys()))" 2>/dev/null || \
                 cat "$TOWN_ROOT/rigs.json" | grep -oP '"\K[^"]+(?=":\s*\{)' | grep -vE "^(rigs|beads|version)$" 2>/dev/null || true))
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

# Find stray beads directories
STRAY_BEADS=()
[[ -d "$HOME/.beads" ]] && STRAY_BEADS+=("$HOME/.beads")
[[ -d "$HOME/gt/.beads" ]] && STRAY_BEADS+=("$HOME/gt/.beads")

# Find unregistered rig directories in TOWN_ROOT
UNREGISTERED_RIGS=()
for d in "$TOWN_ROOT"/*/; do
    [[ -d "$d" ]] || continue
    name=$(basename "$d")
    # Skip known infrastructure dirs
    [[ "$name" =~ ^(logs|daemon|mayor|deacon|planner|settings|static|templates|bin|cmd|internal|scripts|vendor)$ ]] && continue
    [[ "$name" =~ ^(\.git|\.dolt-data|\.beads|\.runtime|\.gt-nats-pids|\.github|\.vscode)$ ]] && continue
    
    # Check if it's in RIG_NAMES
    is_registered=false
    for r in "${RIG_NAMES[@]}"; do
        if [[ "$name" == "$r" ]]; then
            is_registered=true
            break
        fi
    done
    
    if [[ "$is_registered" == false ]]; then
        # Check if it looks like a rig (contains .beads, gt-agent-state.json, or rigs.json)
        if [[ -d "$d/.beads" || -f "$d/gt-agent-state.json" || -f "$d/rigs.json" ]]; then
            UNREGISTERED_RIGS+=("$name")
        fi
    fi
done

# Find global Dolt data
GLOBAL_DOLT_DATA=""
[[ -d "$HOME/.dolt-data" ]] && GLOBAL_DOLT_DATA="$HOME/.dolt-data"

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

if [[ ${#UNREGISTERED_RIGS[@]} -gt 0 ]]; then
    echo ""
    echo "  Unregistered rigs (dirs in town root that look like rigs but aren't in rigs.json):"
    for rig in "${UNREGISTERED_RIGS[@]}"; do
        echo "    • ${rig}/"
    done
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

if [[ ${#STRAY_BEADS[@]} -gt 0 || -n "$GLOBAL_DOLT_DATA" ]]; then
    echo ""
    echo "  Stray global directories:"
    for d in "${STRAY_BEADS[@]}"; do
        echo "    • ${d}  (global beads cache)"
    done
    [[ -n "$GLOBAL_DOLT_DATA" ]] && echo "    • ${GLOBAL_DOLT_DATA}  (global dolt data)"
fi

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

# Nuclear option for all related processes
log_info "Cleaning up all remaining gt, bd, and dolt processes..."
pkill -f "gt " 2>/dev/null || true
pkill -f "bd " 2>/dev/null || true
pkill -f "dolt " 2>/dev/null || true
log_ok "Sent SIGTERM to all gt, bd, and dolt processes"

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

if [[ ${#UNREGISTERED_RIGS[@]} -gt 0 ]]; then
    log_info "Deleting unregistered rig directories..."
    for rig in "${UNREGISTERED_RIGS[@]}"; do
        rm -rf "$TOWN_ROOT/$rig"
        log_ok "Deleted unregistered rig: ${rig}/"
    done
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

if [[ ${#STRAY_BEADS[@]} -gt 0 ]]; then
    log_info "Cleaning up stray beads directories..."
    for d in "${STRAY_BEADS[@]}"; do
        rm -rf "$d"
        log_ok "Deleted ${d}"
    done
fi

if [[ -n "$GLOBAL_DOLT_DATA" ]]; then
    log_info "Cleaning up global dolt data..."
    rm -rf "$GLOBAL_DOLT_DATA"
    log_ok "Deleted ${GLOBAL_DOLT_DATA}"
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
for role_dir in mayor deacon planner; do
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
