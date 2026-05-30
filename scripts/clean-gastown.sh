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
        if [[ -f "$dir/settings/config.json" ]]; then
            echo "$dir"
            return 0
        fi
        dir="$(dirname "$dir")"
    done
    return 1
}

# Populate RIG_NAMES from rigs.json (python3 only — portable on macOS; no grep -P).
load_rig_names() {
    RIG_NAMES=()
    [[ -f "$TOWN_ROOT/rigs.json" ]] || return 0
    while IFS= read -r name; do
        [[ -n "$name" ]] && RIG_NAMES+=("$name")
    done < <(python3 -c "
import json
try:
    d = json.load(open('$TOWN_ROOT/rigs.json'))
    for name in d.get('rigs', {}):
        print(name)
except Exception:
    pass" 2>/dev/null)
}

# get_rig_prefix prints the beads prefix used for a rig's session log
# filenames and tmux session names. Used to glob `<prefix>-*.log` files in
# logs/sessions/. Mirrors internal/rig/manager.go:deriveBeadsPrefix.
#
# Resolution order:
#   1. mayor/rigs.json (canonical) — rigs[<name>].beads.prefix
#   2. <town>/rigs.json (fallback copy)
#   3. Algorithmic fallback (mirrors deriveBeadsPrefix in Go):
#      - Strip "-py" / "-go" suffixes
#      - Split on '-' or '_': take initials ("gas-town" -> "gt")
#      - Single word with compound-place suffix
#        (town/ville/port/place/land/field/wood/ford):
#        split and take initials ("gastown" -> "gt")
#      - Single word camelCase: split and take initials ("myProj" -> "mp")
#      - Otherwise first 2 chars ("testgt2" -> "te")
#
# Used after a rig has been removed (rigs.json may be empty), so the
# algorithmic fallback MUST work without any town state.
get_rig_prefix() {
    local rig="$1"
    local rigs_json prefix

    for rigs_json in "$TOWN_ROOT/mayor/rigs.json" "$TOWN_ROOT/rigs.json"; do
        [[ -f "$rigs_json" ]] || continue
        prefix=$(python3 -c "
import json, sys
try:
    d = json.load(open('$rigs_json'))
    rig = d.get('rigs', {}).get('$rig', {})
    beads = rig.get('beads', {}) if isinstance(rig.get('beads'), dict) else {}
    p = beads.get('prefix', '')
    if p:
        print(str(p).rstrip('-'))
except Exception:
    pass" 2>/dev/null)
        if [[ -n "$prefix" ]]; then
            echo "$prefix"
            return
        fi
    done

    # Algorithmic fallback — must match internal/rig/manager.go:deriveBeadsPrefix
    python3 - <<PYEOF
import re
name = "$rig"
name = name.removesuffix("-py").removesuffix("-go")
parts = re.split(r"[-_]", name)
if len(parts) == 1:
    word = parts[0]
    wlow = word.lower()
    # CamelCase: split on lower->upper transitions
    cc = re.findall(r"[A-Z]+(?=[A-Z][a-z])|[A-Z]?[a-z]+|[A-Z]+", word)
    if len(cc) >= 2:
        parts = cc
    else:
        # Compound place names
        for sfx in ("town", "ville", "port", "place", "land", "field", "wood", "ford"):
            if wlow.endswith(sfx) and len(wlow) > len(sfx):
                parts = [wlow[: -len(sfx)], sfx]
                break
if len(parts) >= 2:
    print("".join(p[0] for p in parts if p).lower())
elif len(name) <= 3:
    print(name.lower())
else:
    print(name[:2].lower())
PYEOF
}

# ─── Parse arguments ─────────────────────────────────────────────────

FORCE=false
TOWN_ROOT=""
SINGLE_RIG=""
PRUNE_REMOTE_BRANCHES=false
CLEAR_HQ_MAIL=false
PURGE_HQ_WISPS=false
# Cutoff (hours) for --also-purge-hq-wisps. Anything older than this in the
# HQ issues table and matching the hq-wisp-*.* ephemeral ID pattern is
# closed and purged. Default 1h is generous enough to spare beads created
# by the current session while still nuking everything carried over from
# yesterday/last-week's runs.
PURGE_HQ_WISPS_HOURS=1
PURGE_HQ_PROJECTS=false
# Sibling flag of --also-purge-hq-wisps. Targets the OPEN, non-wisp,
# non-role, non-convoy hq-* work beads (project beads and their
# children: `hq-7wl`, `hq-7wl.2`, `hq-ars`, etc.). These survive
# surgical rig resets and were misleading the Mayor into picking them
# up as "fresh project beads" on the very next kickoff — see Fix #108.
# Reuses the same cutoff variable (PURGE_HQ_WISPS_HOURS) since the
# semantics are identical: spare beads from the current session, nuke
# leftovers from previous runs.

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
        --prune-remote-branches)
            # Single-rig mode only: also delete every non-main branch
            # from the rig's git origin. This stops a freshly-recreated
            # rig from re-fetching ancient polecat/feature branches and
            # entering a MERGE_FAILED loop on its first refinery patrol.
            PRUNE_REMOTE_BRANCHES=true
            shift
            ;;
        --also-clear-hq-mail)
            # Single-rig mode only: also drain inboxes for hq-mayor,
            # hq-planner, hq-deacon, hq-mechanic. The hq database is
            # preserved by single-rig mode, but if those agents had
            # been chatting about the now-removed rig, their inbox
            # threads still reference it.
            #
            # NOTE (Fix #107): the rig's OWN agent inboxes (architect,
            # qa, refinery, witness) are now ALWAYS drained as part of
            # the rig reset — they're rig-scoped state that has no
            # reason to survive a rig nuke. Pre-Fix-#107, stale `QA
            # Request` / `Coverage Report Request` mail in QA's inbox
            # caused QA to immediately spam the freshly-up mayor on
            # the next `gt up`, drowning real project kickoffs.
            CLEAR_HQ_MAIL=true
            shift
            ;;
        --also-purge-hq-wisps)
            # Single-rig mode only: close + purge stale `hq-wisp-*.*`
            # ephemeral issues from the preserved HQ Dolt DB. Without
            # this, the planner's first `bd ready` after a rig reset
            # sees hundreds of P0 wisps from prior molecule pours
            # (Fizz/ML/Hello-API/etc.) and either hallucinates new
            # tasks against them or trips duplicate-key SQL errors
            # trying to re-create them. Older-than-cutoff filter
            # protects beads from the current session.
            PURGE_HQ_WISPS=true
            shift
            ;;
        --purge-hq-wisps-hours=*)
            # Override the default 1h cutoff for --also-purge-hq-wisps.
            # Useful when the operator KNOWS the rig hasn't been touched
            # in N hours and wants to spare nothing.
            PURGE_HQ_WISPS_HOURS="${1#--purge-hq-wisps-hours=}"
            shift
            ;;
        --also-purge-hq-projects)
            # Single-rig mode only: close + delete open hq-* work beads
            # (project beads + their child tasks) that survived prior
            # surgical resets. Excludes role identity beads (hq-mayor,
            # hq-planner, hq-deacon, hq-mechanic), convoy beads (hq-cv-*),
            # and wisp ephemerals (covered by --also-purge-hq-wisps).
            # Reuses --purge-hq-wisps-hours for the cutoff.
            #
            # Background (Fix #108): without this, prior project beads
            # (e.g. `hq-7wl.2 Implement fizzbuzz function`) remain OPEN in
            # HQ across rig resets. The Mayor's LLM then sees them via
            # `bd ready` and picks them up as Stage 3 work on the next
            # patrol — completely bypassing the new Stage 0 kickoff
            # signal sitting in its inbox. Cleaning these out makes the
            # Mayor's only actionable signal the new operator mail.
            PURGE_HQ_PROJECTS=true
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [options] [town-root]"
            echo ""
            echo "Reset Gas Town workspaces. Two modes:"
            echo "  - Full town reset (default): deletes everything."
            echo "  - Single rig reset (--rig=NAME): preserves hq + other rigs."
            echo ""
            echo "Options:"
            echo "  --rig=NAME                 Reset only the named rig (surgical)."
            echo "  --prune-remote-branches    (--rig only) Also delete every"
            echo "                             non-main branch from the rig's git"
            echo "                             origin. Stops fresh rig from"
            echo "                             re-fetching ancient polecat refs."
            echo "  --also-clear-hq-mail       (--rig only) Drain inboxes for"
            echo "                             hq-mayor/planner/deacon/mechanic."
            echo "  --also-purge-hq-wisps      (--rig only) Close + purge stale"
            echo "                             hq-wisp-*.* ephemeral issues from"
            echo "                             the preserved HQ Dolt DB so the"
            echo "                             fresh rig's planner sees a clean"
            echo "                             bd ready (default cutoff: 1h)."
            echo "  --also-purge-hq-projects   (--rig only) Close + purge stale"
            echo "                             hq-* project/task beads from prior"
            echo "                             runs. Excludes role beads, convoys,"
            echo "                             and wisps. Uses same cutoff as"
            echo "                             --also-purge-hq-wisps (default 1h)."
            echo "                             Without this, Mayor's LLM picks up"
            echo "                             old project beads on bd ready and"
            echo "                             bypasses the new Stage 0 kickoff."
            echo "  --purge-hq-wisps-hours=N   Override cutoff (default: 1). Applies"
            echo "                             to both --also-purge-hq-wisps and"
            echo "                             --also-purge-hq-projects."
            echo "  -f, --force                Skip confirmation prompts (DANGEROUS)"
            echo "  -h, --help                 Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0 ~/gt                          # Full nuclear reset (all rigs)"
            echo "  $0 --force ~/gt                  # Same, non-interactive"
            echo "  $0 --rig=testgt2 ~/gt            # Reset only the testgt2 rig"
            echo "  $0 --rig=testgt2 --force ~/gt    # Same, non-interactive"
            echo "  $0 --rig=testgt2 --prune-remote-branches --also-clear-hq-mail \\"
            echo "      --also-purge-hq-wisps --also-purge-hq-projects --force ~/gt"
            echo "                                   # Maximum-surgical: rig dir +"
            echo "                                   # dolt db + session logs +"
            echo "                                   # remote feature/polecat refs +"
            echo "                                   # hq agents' inboxes +"
            echo "                                   # stale hq-wisp-*.* issues"
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

if [[ "$PRUNE_REMOTE_BRANCHES" == true && -z "$SINGLE_RIG" ]]; then
    log_fatal "--prune-remote-branches requires --rig=NAME"
    exit 1
fi
if [[ "$CLEAR_HQ_MAIL" == true && -z "$SINGLE_RIG" ]]; then
    log_fatal "--also-clear-hq-mail requires --rig=NAME"
    exit 1
fi
if [[ "$PURGE_HQ_WISPS" == true && -z "$SINGLE_RIG" ]]; then
    log_fatal "--also-purge-hq-wisps requires --rig=NAME"
    exit 1
fi
if [[ "$PURGE_HQ_PROJECTS" == true && -z "$SINGLE_RIG" ]]; then
    log_fatal "--also-purge-hq-projects requires --rig=NAME"
    exit 1
fi
if [[ "$PURGE_HQ_WISPS" == true ]]; then
    if ! [[ "$PURGE_HQ_WISPS_HOURS" =~ ^[0-9]+$ ]] || (( PURGE_HQ_WISPS_HOURS < 0 )); then
        log_fatal "--purge-hq-wisps-hours must be a non-negative integer (got: $PURGE_HQ_WISPS_HOURS)"
        exit 1
    fi
fi

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

if [[ -z "$TOWN_ROOT" || ! -f "$TOWN_ROOT/settings/config.json" ]]; then
    log_fatal "Could not find a Gas Town workspace (no settings/config.json found)."
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

    # Residual-state-only mode: rig has no on-disk or registry presence,
    # but session logs / HQ inboxes / origin branches may still survive a
    # prior cleanup that ran before Fix 98. Allow the script to run iff the
    # user opted into at least one residual cleanup (--also-clear-hq-mail
    # or --prune-remote-branches); otherwise there's nothing to do.
    RESIDUAL_ONLY=false
    if [[ "$RIG_REGISTERED" != true && ! -d "$RIG_DIR" ]]; then
        if [[ "$CLEAR_HQ_MAIL" != true && "$PRUNE_REMOTE_BRANCHES" != true && "$PURGE_HQ_WISPS" != true && "$PURGE_HQ_PROJECTS" != true ]]; then
            log_fatal "Rig '$SINGLE_RIG' is not registered and has no directory at $RIG_DIR"
            log_fatal "Run 'gt rig list' to see known rigs."
            log_fatal "Pass --also-clear-hq-mail, --prune-remote-branches,"
            log_fatal "--also-purge-hq-wisps, or --also-purge-hq-projects to"
            log_fatal "clean residual session logs / HQ inboxes / origin"
            log_fatal "branches / stale HQ wisps / stale HQ project beads"
            log_fatal "for a rig that has already been removed."
            exit 1
        fi
        RESIDUAL_ONLY=true
        log_warn "Rig '$SINGLE_RIG' has no directory or registry entry."
        log_warn "Running in residual-state-only mode — skipping rig-dir / Dolt /"
        log_warn "rigs.json steps (already gone). Will still clean session logs,"
        log_warn "HQ mail, origin branches, and stale HQ wisps as requested."
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

    # Capture origin URL BEFORE deleting the rig dir — needed later if
    # --prune-remote-branches is set. We try config.json first (the
    # canonical record), then the bare repo's git remote, then the
    # mayor's rigs.json mirror (the only source still available in
    # residual-only mode after the rig dir is gone).
    RIG_ORIGIN_URL=""
    if [[ -f "$RIG_DIR/config.json" ]]; then
        RIG_ORIGIN_URL=$(python3 -c "
import json, sys
try:
    d = json.load(open('$RIG_DIR/config.json'))
    print(d.get('git_url') or d.get('url') or d.get('origin', ''))
except Exception:
    pass
" 2>/dev/null)
    fi
    if [[ -z "$RIG_ORIGIN_URL" && -d "$RIG_DIR/.repo.git" ]]; then
        RIG_ORIGIN_URL=$(git --git-dir="$RIG_DIR/.repo.git" remote get-url origin 2>/dev/null || true)
    fi
    if [[ -z "$RIG_ORIGIN_URL" ]]; then
        for rigs_json in "$TOWN_ROOT/mayor/rigs.json" "$TOWN_ROOT/rigs.json"; do
            [[ -f "$rigs_json" ]] || continue
            RIG_ORIGIN_URL=$(python3 -c "
import json
try:
    d = json.load(open('$rigs_json'))
    rig = d.get('rigs', {}).get('$SINGLE_RIG', {})
    print(rig.get('git_url') or rig.get('url') or rig.get('origin', '') or '')
except Exception:
    pass
" 2>/dev/null)
            [[ -n "$RIG_ORIGIN_URL" ]] && break
        done
    fi

    # Resolve the rig's beads/session prefix. Session log files in
    # $TOWN_ROOT/logs/sessions/ are named after tmux session names:
    #
    #   <prefix>-<polecat>.log              (polecat session)
    #   <prefix>-<rigName>-architect.log    (architect)
    #   <prefix>-<rigName>-qa.log           (QA)
    #
    # …where <prefix> is derived from the rig name by
    # internal/rig/manager.go:deriveBeadsPrefix (e.g. "testgt2" -> "te",
    # "gas-town" -> "gt"). The earlier implementation globbed
    # `te-<rig>*.log` which caught HQ-style logs but MISSED polecat logs
    # (which use just `<prefix>-<polecat>.log`, with no rig name). We now
    # glob `<prefix>-*.log` so all rig-scoped session logs are matched.
    RIG_PREFIX="$(get_rig_prefix "$SINGLE_RIG")"
    RIG_LOG_PATTERN="${RIG_PREFIX}-*.log"
    RIG_LOG_COUNT=0
    if [[ -d "$TOWN_ROOT/logs/sessions" ]]; then
        RIG_LOG_COUNT=$(find "$TOWN_ROOT/logs/sessions" -maxdepth 1 \
            -name "$RIG_LOG_PATTERN" 2>/dev/null | wc -l | tr -d ' ')
    fi

    # Detect stale remote branches on origin (anything that isn't
    # `main`). When a fresh `gt rig add` re-clones, these branches
    # come back along with their merge state, and the refinery's first
    # patrol promptly tries to merge them. Listing requires the origin
    # URL we captured above.
    STALE_REMOTE_BRANCH_COUNT=0
    STALE_REMOTE_BRANCHES=""
    if [[ -n "$RIG_ORIGIN_URL" ]]; then
        # Use ls-remote so we don't need a local clone to be present.
        # The tab-separated output is `<sha>\t<ref>`; we want the ref.
        STALE_REMOTE_BRANCHES=$(git ls-remote --heads "$RIG_ORIGIN_URL" 2>/dev/null \
            | awk '$2 != "refs/heads/main" {print $2}' || true)
        if [[ -n "$STALE_REMOTE_BRANCHES" ]]; then
            STALE_REMOTE_BRANCH_COUNT=$(echo "$STALE_REMOTE_BRANCHES" | wc -l | tr -d ' ')
        fi
    fi

    echo "────────────────────────────────────────────────────────────────"
    if [[ "$RESIDUAL_ONLY" == true ]]; then
        echo "  PREVIEW OF WHAT WILL BE DELETED (residual-state-only mode)"
    else
        echo "  PREVIEW OF WHAT WILL BE DELETED (single-rig mode)"
    fi
    echo "────────────────────────────────────────────────────────────────"
    echo ""
    echo "  Rig:                  ${SINGLE_RIG}"
    echo "  Prefix (logs/tmux):   ${RIG_PREFIX}"
    echo "  Registered in rigs.json: ${RIG_REGISTERED}"
    echo "  Origin URL:           ${RIG_ORIGIN_URL:-<unknown>}"
    echo "  Directory:            ${RIG_DIR}"
    [[ -d "$RIG_DIR" ]]      && echo "    → exists, will be rm -rf'd (worktrees, .repo.git, polecats, configs, .beads)"
    [[ ! -d "$RIG_DIR" ]]    && echo "    → already missing, nothing to delete on disk"
    echo "  Polecats on disk:     ${POLECAT_COUNT}"
    echo "  Dolt DB:              ${RIG_DOLT_DIR}"
    [[ "$HAS_DOLT_DB" == true ]]  && echo "    → exists, will be rm -rf'd (loses all beads, wisps, mail for this rig)"
    [[ "$HAS_DOLT_DB" != true ]]  && echo "    → no separate database directory found (nothing to delete)"
    echo "  Session logs:         ${RIG_LOG_COUNT} file(s) matching $RIG_LOG_PATTERN"
    [[ "$RIG_LOG_COUNT" -gt 0 ]] && echo "    → will be deleted (prevents LLM-context contamination on next boot)"

    if [[ -n "$RIG_ORIGIN_URL" ]]; then
        echo ""
        if [[ "$STALE_REMOTE_BRANCH_COUNT" -eq 0 ]]; then
            echo "  Remote branches:      origin is clean (only main)"
        elif [[ "$PRUNE_REMOTE_BRANCHES" == true ]]; then
            echo "  Remote branches:      ${STALE_REMOTE_BRANCH_COUNT} non-main branch(es)"
            echo "    --prune-remote-branches set — these will be DELETED from origin:"
            echo "$STALE_REMOTE_BRANCHES" | sed 's|^|      • |'
        else
            log_warn "  Remote branches:      ${STALE_REMOTE_BRANCH_COUNT} non-main branch(es) on origin"
            log_warn "    These will REMAIN on origin and re-poison the fresh rig:"
            echo "$STALE_REMOTE_BRANCHES" | sed 's|^|      • |'
            log_warn "    Pass --prune-remote-branches to delete them."
        fi
    fi

    if [[ "$CLEAR_HQ_MAIL" == true ]]; then
        echo ""
        echo "  HQ inboxes:           will be drained (mayor, planner, deacon, mechanic)"
    fi

    if [[ "$PURGE_HQ_WISPS" == true ]]; then
        # Best-effort count BEFORE we do anything. dolt may be unreachable
        # if services are down; in that case we just say "unknown".
        HQ_DOLT_DIR="$TOWN_ROOT/.dolt-data/hq"
        STALE_HQ_WISP_COUNT="?"
        if [[ -d "$HQ_DOLT_DIR" ]] && command -v dolt >/dev/null 2>&1; then
            # IMPORTANT: `created_at` is stored as UTC-naive datetime, but
            # MySQL/Dolt `NOW()` returns *local* time (TZ-shifted). On a
            # machine offset +0200 from UTC, NOW() - INTERVAL 1 HOUR would
            # be ~1h *in the future* relative to the stored UTC times,
            # so we'd accidentally classify perfectly-fresh wisps as
            # stale and purge them. Use UTC_TIMESTAMP() instead.
            STALE_HQ_WISP_COUNT=$(dolt --data-dir "$HQ_DOLT_DIR" sql -q "SELECT COUNT(*) FROM issues WHERE id LIKE 'hq-wisp-%' AND status IN ('open','in_progress','hooked') AND created_at < UTC_TIMESTAMP() - INTERVAL ${PURGE_HQ_WISPS_HOURS} HOUR;" 2>/dev/null \
                | awk 'NR>3 && /^\| *[0-9]+/ {gsub(/[| ]/,""); print; exit}')
            [[ -z "$STALE_HQ_WISP_COUNT" ]] && STALE_HQ_WISP_COUNT="?"
        fi
        echo ""
        echo "  HQ stale wisps:       ${STALE_HQ_WISP_COUNT} open hq-wisp-*.* issue(s)"
        echo "                        older than ${PURGE_HQ_WISPS_HOURS}h will be closed + purged"
        echo "                        (planner's bd ready will be clean on next boot)"
    fi

    if [[ "$PURGE_HQ_PROJECTS" == true ]]; then
        STALE_HQ_PROJECT_COUNT="(unknown — services down)"
        if command -v dolt >/dev/null 2>&1 && [[ -d "$TOWN_ROOT/.dolt-data/hq" ]]; then
            STALE_HQ_PROJECT_COUNT=$(dolt --data-dir "$TOWN_ROOT/.dolt-data/hq" sql -q "SELECT COUNT(*) FROM issues WHERE id LIKE 'hq-%' AND id NOT LIKE 'hq-wisp-%' AND id NOT LIKE 'hq-cv-%' AND id NOT IN ('hq-mayor','hq-planner','hq-deacon','hq-mechanic') AND status IN ('open','in_progress','hooked') AND created_at < UTC_TIMESTAMP() - INTERVAL ${PURGE_HQ_WISPS_HOURS} HOUR;" 2>/dev/null \
                | awk 'NR>3 && /^\| *[0-9]+/ {gsub(/[| ]/,""); print; exit}')
            [[ -z "$STALE_HQ_PROJECT_COUNT" ]] && STALE_HQ_PROJECT_COUNT="?"
        fi
        echo ""
        echo "  HQ stale projects:    ${STALE_HQ_PROJECT_COUNT} open hq-* work bead(s)"
        echo "                        older than ${PURGE_HQ_WISPS_HOURS}h will be closed + purged"
        echo "                        (project/task beads from prior runs; excludes"
        echo "                        role beads hq-mayor/planner/deacon/mechanic,"
        echo "                        convoys hq-cv-*, and wisps hq-wisp-*)"
    fi

    echo ""
    echo "  PRESERVED (other rig data, town config, hq database):"
    echo "    • $TOWN_ROOT/settings/config.json"
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

    # Step 7: delete the rig's session log files. Without this the
    # freshly-recreated witness/refinery re-read their own historical
    # `te-<rig>-*.log` on boot and the LLM picks up references to
    # already-merged/closed polecat branches. Symptom we observed:
    # witness emitting `MERGE_FAILED <polecat/rust/te-189@m>` to the
    # refinery on every patrol for 30+ minutes after the rig was wiped
    # and the remote branch deleted, purely from log-replay.
    if [[ "$RIG_LOG_COUNT" -gt 0 ]]; then
        log_info "Deleting ${RIG_LOG_COUNT} session log file(s)..."
        find "$TOWN_ROOT/logs/sessions" -maxdepth 1 \
            -name "$RIG_LOG_PATTERN" -delete 2>/dev/null || true
        log_ok "Deleted session logs (LLM context will be clean on next boot)"
    fi

    # Step 8: prune stale remote branches if requested. Fresh `gt rig
    # add` re-clones origin, so any non-main branches that survived
    # from prior runs (polecat/* refs preserved by `gt polecat nuke`,
    # abandoned feature/* refs from earlier polecats) immediately
    # poison the new rig: refinery's first patrol fetches them and
    # tries to merge them.
    if [[ "$PRUNE_REMOTE_BRANCHES" == true && "$STALE_REMOTE_BRANCH_COUNT" -gt 0 ]]; then
        log_info "Pruning ${STALE_REMOTE_BRANCH_COUNT} stale remote branch(es) on ${RIG_ORIGIN_URL}..."
        # Build a colon-prefixed delete refspec per branch. We use
        # individual `git push :refs/heads/<name>` calls instead of
        # one batched push so we can report partial failure.
        local_failures=0
        while IFS= read -r ref; do
            [[ -z "$ref" ]] && continue
            if git push "$RIG_ORIGIN_URL" ":${ref}" >/dev/null 2>&1; then
                log_ok "  deleted origin/${ref#refs/heads/}"
            else
                log_warn "  could not delete origin/${ref#refs/heads/}"
                ((local_failures++)) || true
            fi
        done <<< "$STALE_REMOTE_BRANCHES"
        if [[ "$local_failures" -eq 0 ]]; then
            log_ok "All stale remote branches pruned"
        else
            log_warn "${local_failures} remote branch(es) failed to delete — may need manual cleanup"
        fi
    fi

    # Step 9a: ALWAYS drain rig agent inboxes (architect/qa/refinery/witness
    # plus any discovered polecats/crew). Mail addressed to `<rig>/<agent>`
    # lives in the HQ database keyed by the full address — so even after the
    # rig directory + its own dolt-data are wiped, the agents that come back
    # up in the next `gt rig add` inherit the old mail queue. We saw this
    # cause QA to immediately spam the (also-freshly-up) mayor with stale
    # "QA Request" / "Coverage Report Request" wisps from the previous
    # session, drowning the new project kickoff. The agents that the
    # operator actually wants to interact with cleanly are dead conversations
    # away from a real reset, so we always clear them.
    if command -v gt >/dev/null 2>&1; then
        log_info "Draining rig agent inboxes for '$SINGLE_RIG'..."
        # Start with the four standard rig agents. Polecats and crew are
        # enumerated below as a best-effort — those dirs may already be
        # gone depending on script step ordering, in which case the
        # globs expand to nothing and the loop body is skipped.
        rig_targets=(
            "$SINGLE_RIG/architect"
            "$SINGLE_RIG/qa"
            "$SINGLE_RIG/refinery"
            "$SINGLE_RIG/witness"
        )
        if [[ -d "$TOWN_ROOT/$SINGLE_RIG/polecats" ]]; then
            for pc in "$TOWN_ROOT/$SINGLE_RIG/polecats"/*/; do
                [[ -d "$pc" ]] || continue
                pc_name="$(basename "$pc")"
                rig_targets+=("$SINGLE_RIG/polecats/$pc_name")
            done
        fi
        if [[ -d "$TOWN_ROOT/$SINGLE_RIG/crew" ]]; then
            for cw in "$TOWN_ROOT/$SINGLE_RIG/crew"/*/; do
                [[ -d "$cw" ]] || continue
                cw_name="$(basename "$cw")"
                rig_targets+=("$SINGLE_RIG/crew/$cw_name")
            done
        fi
        for tgt in "${rig_targets[@]}"; do
            result=$(gt mail clear "$tgt" 2>&1 | tail -1 || true)
            log_ok "  ${tgt}: ${result}"
        done
    fi

    # Step 9b: drain HQ inboxes if requested. The hq database is
    # preserved by single-rig mode, but if the mayor/planner/deacon/
    # mechanic had been chatting about the now-removed rig (assignment
    # threads, alert threads, escalations) their inbox queues still
    # reference it and they'll spend their next patrol replying to
    # dead conversations. Opt-in because it's a town-wide change.
    if [[ "$CLEAR_HQ_MAIL" == true ]] && command -v gt >/dev/null 2>&1; then
        log_info "Draining HQ inboxes (mayor, planner, deacon, mechanic)..."
        for hq in hq-mayor hq-planner hq-deacon hq-mechanic; do
            # Output line examples:
            #   "✓ Cleared 7 messages from hq-mayor"
            #   "○ Inbox hq-mayor is already empty"
            result=$(gt mail clear "$hq" 2>&1 | tail -1 || true)
            log_ok "  ${hq}: ${result}"
        done
    fi

    # Step 10: purge stale `hq-wisp-*.*` ephemeral issues from the HQ Dolt
    # DB. Without this, the freshly-recreated rig's planner sees hundreds
    # of P0 wisps from prior molecule pours on its first `bd ready` and
    # either hallucinates new tasks against them or trips duplicate-key
    # SQL errors trying to re-create the same IDs. We close + zero out
    # any open `hq-wisp-*.*` row older than the cutoff, then delete via
    # SQL (bd purge --force only removes already-closed wisps, but we
    # need to also reach `open` ones; direct DELETE is safer than
    # multi-step gt/bd plumbing here).
    if [[ "$PURGE_HQ_WISPS" == true ]]; then
        HQ_DOLT_DIR="$TOWN_ROOT/.dolt-data/hq"
        if [[ ! -d "$HQ_DOLT_DIR" ]]; then
            log_warn "Skipping --also-purge-hq-wisps: $HQ_DOLT_DIR does not exist"
        elif ! command -v dolt >/dev/null 2>&1; then
            log_warn "Skipping --also-purge-hq-wisps: dolt binary not on PATH"
        else
            log_info "Purging stale hq-wisp-*.* issues from HQ Dolt DB (cutoff: ${PURGE_HQ_WISPS_HOURS}h)..."

            # IMPORTANT: `created_at` is stored UTC-naive; MySQL/Dolt
            # `NOW()` returns LOCAL time. On non-UTC machines that
            # mismatch would either spare too much OR nuke fresh wisps.
            # We use UTC_TIMESTAMP() so the comparison is sound regardless
            # of the operator's TZ. Build a CTE-style WHERE filter once
            # and reference it from each DELETE to keep the cutoff
            # consistent if the wall clock ticks mid-statement.
            cutoff_pred="id LIKE 'hq-wisp-%' AND created_at < UTC_TIMESTAMP() - INTERVAL ${PURGE_HQ_WISPS_HOURS} HOUR"
            count_pred="${cutoff_pred} AND status IN ('open','in_progress','hooked')"
            stale_id_subq="SELECT id FROM issues WHERE ${cutoff_pred}"

            # Capture the count FIRST (the only meaningful number to
            # report — the trailing DELETE only confirms it landed).
            # Dolt's MySQL dialect doesn't support `SELECT @var := COUNT(*)`
            # so we just take the count via a separate query.
            before_out=$(dolt --data-dir "$HQ_DOLT_DIR" sql -q "SELECT COUNT(*) FROM issues WHERE ${count_pred};" 2>&1 || true)
            before_count=$(echo "$before_out" | awk 'NR>3 && /^\| *[0-9]+/ {gsub(/[| ]/,""); print; exit}')
            [[ -z "$before_count" ]] && before_count="?"

            # Now cascade-delete the related rows (labels, dependencies,
            # comments, events, issue_snapshots) then the issues
            # themselves. Each statement is independent so a failure on
            # one (e.g. table absent in older HQ schemas) doesn't block
            # the rest. We swallow errors per-statement and report the
            # final issue count at the end.
            purge_sql=$(cat <<PURGE_SQL
DELETE FROM labels          WHERE issue_id IN (${stale_id_subq});
DELETE FROM dependencies    WHERE issue_id IN (${stale_id_subq}) OR depends_on_id IN (${stale_id_subq});
DELETE FROM comments        WHERE issue_id IN (${stale_id_subq});
DELETE FROM events          WHERE issue_id IN (${stale_id_subq});
DELETE FROM issue_snapshots WHERE issue_id IN (${stale_id_subq});
DELETE FROM issues          WHERE ${cutoff_pred};
PURGE_SQL
)
            purge_out=$(dolt --data-dir "$HQ_DOLT_DIR" sql -q "$purge_sql" 2>&1 || true)

            # Verify by re-counting — should be zero stale rows remaining.
            after_out=$(dolt --data-dir "$HQ_DOLT_DIR" sql -q "SELECT COUNT(*) FROM issues WHERE ${count_pred};" 2>&1 || true)
            after_count=$(echo "$after_out" | awk 'NR>3 && /^\| *[0-9]+/ {gsub(/[| ]/,""); print; exit}')
            [[ -z "$after_count" ]] && after_count="?"

            if [[ "$before_count" != "?" && "$after_count" == "0" ]]; then
                log_ok "Purged ${before_count} stale hq-wisp-*.* issue(s) from HQ Dolt DB (remaining: 0)"
            else
                log_warn "Wisp purge completed with caveats: before=${before_count} after=${after_count}"
                log_warn "  dolt output:"
                echo "$purge_out" | sed 's|^|    |' | tail -20
            fi
        fi
    fi

    # Step 10b (Fix #108): purge stale OPEN hq-* PROJECT/TASK beads
    # from the HQ Dolt DB. Mirrors the wisps purge above but targets
    # non-wisp, non-role, non-convoy work beads that survived prior
    # surgical resets. Without this, the Mayor's LLM sees old
    # `hq-7wl.2 Implement fizzbuzz function` etc. on `bd ready` /
    # `gt hook` and slings polecats on them, completely ignoring the
    # new Stage-0 kickoff mail sitting in its inbox.
    if [[ "$PURGE_HQ_PROJECTS" == true ]]; then
        HQ_DOLT_DIR="$TOWN_ROOT/.dolt-data/hq"
        if [[ ! -d "$HQ_DOLT_DIR" ]]; then
            log_warn "Skipping --also-purge-hq-projects: $HQ_DOLT_DIR does not exist"
        elif ! command -v dolt >/dev/null 2>&1; then
            log_warn "Skipping --also-purge-hq-projects: dolt binary not on PATH"
        else
            log_info "Purging stale hq-* project/task beads from HQ Dolt DB (cutoff: ${PURGE_HQ_WISPS_HOURS}h)..."

            # Exclude rules:
            #   - hq-wisp-*       → handled by --also-purge-hq-wisps
            #   - hq-cv-*         → convoy coordination state
            #   - hq-mayor/planner/deacon/mechanic → role identity beads
            # Cutoff predicate uses UTC_TIMESTAMP for the same reason
            # the wisps purge does — `created_at` is UTC-naive.
            proj_cutoff_pred="id LIKE 'hq-%' AND id NOT LIKE 'hq-wisp-%' AND id NOT LIKE 'hq-cv-%' AND id NOT IN ('hq-mayor','hq-planner','hq-deacon','hq-mechanic') AND created_at < UTC_TIMESTAMP() - INTERVAL ${PURGE_HQ_WISPS_HOURS} HOUR"
            proj_count_pred="${proj_cutoff_pred} AND status IN ('open','in_progress','hooked')"
            proj_stale_id_subq="SELECT id FROM issues WHERE ${proj_cutoff_pred}"

            proj_before_out=$(dolt --data-dir "$HQ_DOLT_DIR" sql -q "SELECT COUNT(*) FROM issues WHERE ${proj_count_pred};" 2>&1 || true)
            proj_before_count=$(echo "$proj_before_out" | awk 'NR>3 && /^\| *[0-9]+/ {gsub(/[| ]/,""); print; exit}')
            [[ -z "$proj_before_count" ]] && proj_before_count="?"

            proj_purge_sql=$(cat <<PROJ_PURGE_SQL
DELETE FROM labels          WHERE issue_id IN (${proj_stale_id_subq});
DELETE FROM dependencies    WHERE issue_id IN (${proj_stale_id_subq}) OR depends_on_id IN (${proj_stale_id_subq});
DELETE FROM comments        WHERE issue_id IN (${proj_stale_id_subq});
DELETE FROM events          WHERE issue_id IN (${proj_stale_id_subq});
DELETE FROM issue_snapshots WHERE issue_id IN (${proj_stale_id_subq});
DELETE FROM issues          WHERE ${proj_cutoff_pred};
PROJ_PURGE_SQL
)
            proj_purge_out=$(dolt --data-dir "$HQ_DOLT_DIR" sql -q "$proj_purge_sql" 2>&1 || true)

            proj_after_out=$(dolt --data-dir "$HQ_DOLT_DIR" sql -q "SELECT COUNT(*) FROM issues WHERE ${proj_count_pred};" 2>&1 || true)
            proj_after_count=$(echo "$proj_after_out" | awk 'NR>3 && /^\| *[0-9]+/ {gsub(/[| ]/,""); print; exit}')
            [[ -z "$proj_after_count" ]] && proj_after_count="?"

            if [[ "$proj_before_count" != "?" && "$proj_after_count" == "0" ]]; then
                log_ok "Purged ${proj_before_count} stale hq-* project/task bead(s) from HQ Dolt DB (remaining: 0)"
            else
                log_warn "Project purge completed with caveats: before=${proj_before_count} after=${proj_after_count}"
                log_warn "  dolt output:"
                echo "$proj_purge_out" | sed 's|^|    |' | tail -20
            fi
        fi
    fi

    echo ""
    echo "╔══════════════════════════════════════════════════════════════╗"
    echo "║              SINGLE-RIG RESET COMPLETE                       ║"
    echo "╚══════════════════════════════════════════════════════════════╝"
    echo ""
    log_ok "Rig '${SINGLE_RIG}' has been reset."
    log_ok "Preserved: hq database, daemon, mayor/planner/deacon, other rigs."

    # Surface the "you should have done this" hints when the user
    # didn't opt into the deeper cleanups but we detected reasons to.
    if [[ "$PRUNE_REMOTE_BRANCHES" != true && "$STALE_REMOTE_BRANCH_COUNT" -gt 0 ]]; then
        echo ""
        log_warn "Origin still has ${STALE_REMOTE_BRANCH_COUNT} non-main branch(es)."
        log_warn "Re-run with --prune-remote-branches if you want a truly fresh rig."
    fi

    echo ""
    echo "  Next steps:"
    printf "    1. ${YELLOW}gt rig add ${SINGLE_RIG} ${RIG_ORIGIN_URL:-<git-url>}${NC}\n"
    printf "    2. ${YELLOW}gt up${NC}                                 # Bring services up\n"
    printf "    3. ${YELLOW}gt status -v${NC}                          # Verify clean state\n"
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
load_rig_names

# Find agent state files
AGENT_STATES=($(find "$TOWN_ROOT" -maxdepth 3 -name "gt-agent-state.json" 2>/dev/null || true))

# Count Dolt issues
DOLT_ISSUES=0
DOLT_WISPS=0
if command -v dolt &>/dev/null && [[ -d "$TOWN_ROOT/.dolt-data/hq" ]]; then
    DOLT_ISSUES=$(cd "$TOWN_ROOT/.dolt-data/hq" && dolt sql -q "SELECT COUNT(*) FROM issues;" 2>/dev/null | tail -1 || echo "0")
    DOLT_WISPS=$(cd "$TOWN_ROOT/.dolt-data/hq" && dolt sql -q "SELECT COUNT(*) FROM wisps;" 2>/dev/null | tail -1 || echo "0")
fi

# Find stray beads directories. Skip anything that lives inside
# $TOWN_ROOT — the town's own .beads/ is the canonical one and is
# handled by Phase 5 (which preserves formulas/). If we listed it
# here too, the Phase 5 second-pass `rm -rf` further down would
# clobber the freshly-re-provisioned formulas/ subdir.
STRAY_BEADS=()
for candidate in "$HOME/.beads" "$HOME/gt/.beads"; do
    [[ -d "$candidate" ]] || continue
    # Resolve to absolute path for comparison
    abs_candidate="$(cd "$candidate" 2>/dev/null && pwd)" || continue
    abs_town_beads="$(cd "$TOWN_ROOT/.beads" 2>/dev/null && pwd)" || true
    if [[ -n "$abs_town_beads" && "$abs_candidate" == "$abs_town_beads" ]]; then
        continue
    fi
    STRAY_BEADS+=("$candidate")
done

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
    if ((${#RIG_NAMES[@]} > 0)); then
        for r in "${RIG_NAMES[@]}"; do
            if [[ "$name" == "$r" ]]; then
                is_registered=true
                break
            fi
        done
    fi
    
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
echo "    • settings/config.json     (town configuration)"
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
if ((${#RIG_NAMES[@]} > 0)); then
    for rig in "${RIG_NAMES[@]}"; do
        rig_dir="$TOWN_ROOT/$rig"
        if [[ -d "$rig_dir" ]]; then
            rm -rf "$rig_dir"
            log_ok "Deleted rig directory: ${rig}/"
            ((DELETED_RIGS++)) || true
        fi
    done
fi
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

# ─── Phase 5: Delete Beads Cache (preserve formulas) ────────────────
#
# `.beads/formulas/*.formula.toml` files are static, content-addressable
# workflow definitions. They are provisioned by `gt install` from the
# binary's embedded FS, and the sling-time `verifyFormulaExists`
# helper looks them up via `bd formula show` (which only sees the
# on-disk copies, not the embedded ones). If we wipe them on a nuclear
# reset, the next `gt sling shiny` / `gt sling mol-idea-to-plan` /
# `gt sling code-review` fails with:
#
#   Error: formula 'shiny' not found (check 'bd formula list')
#
# breaking the Mayor → Architect → Planner → Polecat → QA pipeline.
# Since the files are static, we preserve `.beads/formulas/` across
# the wipe; everything else under `.beads/` (DB credentials, port
# file, routing, backup, ISSUES export, etc.) is removed normally.

log_info "Clearing beads cache (preserving formulas/)..."
if [[ -d "$TOWN_ROOT/.beads" ]]; then
    # Move formulas/ aside, wipe everything else, restore formulas/.
    PRESERVE_FORMULAS_TMP=""
    if [[ -d "$TOWN_ROOT/.beads/formulas" ]]; then
        PRESERVE_FORMULAS_TMP="$(mktemp -d -t gt-formulas.XXXXXX)"
        mv "$TOWN_ROOT/.beads/formulas" "$PRESERVE_FORMULAS_TMP/formulas"
    fi
    rm -rf "$TOWN_ROOT/.beads"
    if [[ -n "$PRESERVE_FORMULAS_TMP" ]]; then
        mkdir -p "$TOWN_ROOT/.beads"
        mv "$PRESERVE_FORMULAS_TMP/formulas" "$TOWN_ROOT/.beads/formulas"
        rmdir "$PRESERVE_FORMULAS_TMP" 2>/dev/null || true
        formula_count=$(find "$TOWN_ROOT/.beads/formulas" -maxdepth 1 -name '*.formula.toml' 2>/dev/null | wc -l)
        log_ok "Deleted .beads/ (preserved $formula_count formula(s) at .beads/formulas/)"
    else
        log_ok "Deleted .beads/ (no formulas to preserve)"
    fi
else
    log_warn "No .beads/ found"
fi

# Belt-and-braces re-provision: if `.beads/formulas/` is still empty
# (no prior formulas to preserve, e.g. someone wiped manually before
# us) AND we can locate this script's sibling source tree
# `internal/formula/formulas/`, copy *.formula.toml from there. This
# matches `formula.ProvisionFormulas()` in the Go code minus the
# checksum tracking (which bd does not require at sling time).
if [[ ! -d "$TOWN_ROOT/.beads/formulas" || -z "$(ls -A "$TOWN_ROOT/.beads/formulas" 2>/dev/null)" ]]; then
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    FORMULA_SRC="$SCRIPT_DIR/../internal/formula/formulas"
    if [[ -d "$FORMULA_SRC" ]]; then
        mkdir -p "$TOWN_ROOT/.beads/formulas"
        cp_count=0
        for f in "$FORMULA_SRC"/*.formula.toml; do
            [[ -f "$f" ]] || continue
            cp "$f" "$TOWN_ROOT/.beads/formulas/"
            ((cp_count++)) || true
        done
        if (( cp_count > 0 )); then
            log_ok "Re-provisioned $cp_count formula(s) from $FORMULA_SRC"
        fi
    else
        log_warn "No formulas to restore (expected $FORMULA_SRC); next 'gt sling shiny' may fail"
    fi
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
if ((${#AGENT_STATES[@]} > 0)); then
    for f in "${AGENT_STATES[@]}"; do
        if [[ -f "$f" ]]; then
            rm -f "$f"
            rel="${f#$TOWN_ROOT/}"
            log_ok "Deleted ${rel}"
            ((DELETED_STATES++)) || true
        fi
    done
fi
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

# Per-channel event spool (events/<channel>/*.event). These pre-date
# the centralized .events.jsonl and previously survived every nuclear
# reset. Stale entries (e.g. SLOT_OPEN events from a prior polecat
# named "rust") aren't load-bearing for `gt up`, but they pollute any
# grep for current rig state and confuse session-aware tooling. The
# spool is fully rebuilt by the daemon on first event after `gt up`,
# so it is safe to remove entirely.
if [[ -d "$TOWN_ROOT/events" ]]; then
    rm -rf "$TOWN_ROOT/events"
    log_ok "Deleted events/ (per-channel event spool)"
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

# ─── Phase 9: Reset rigs.json + residual rig metadata ───────────────
#
# Phase 11 used to preserve `mayor/rigs.json` and `daemon.json` under
# the "configs are safe" heuristic, but on a nuclear reset that left
# stale rig entries (e.g. testgt2) behind. After the rig directory
# itself is gone from Phase 4-5, the next `gt up` still saw the old
# rig in `mayor/rigs.json`, tried to start its witness/refinery/
# architect/qa, and printed:
#     [orphan-recovery] skipping rig <name> (failed to load)
#     ✖ Refinery (<name>): rig '<name>' not found
#     ✖ Architect (<name>): rig '<name>' not found
# until the operator nuked the file by hand.
#
# A nuclear reset means "start from a blank rig slate", so we now
# truncate BOTH `rigs.json` files. Town config is still preserved
# (config.json, town.json, overseer.json).

log_info "Resetting rigs.json (top-level + mayor/)..."
for rigs_file in "$TOWN_ROOT/rigs.json" "$TOWN_ROOT/mayor/rigs.json"; do
    if [[ -f "$rigs_file" ]]; then
        echo '{"version": 1, "rigs": {}}' > "$rigs_file"
        rel="${rigs_file#$TOWN_ROOT/}"
        log_ok "Reset ${rel} (emptied rigs list)"
    fi
done

# Stale per-rig beads-wisp configs and witness respawn-count entries
# also survived old nuclear resets and tripped up the next `gt rig
# list` / planner-side `bd ready` invocations.
log_info "Wiping residual per-rig metadata..."
if [[ -d "$TOWN_ROOT/.beads-wisp/config" ]]; then
    # Each file is one-per-rig: <rig>.json. Safe to delete entirely.
    found=0
    for f in "$TOWN_ROOT/.beads-wisp/config"/*.json; do
        [[ -f "$f" ]] || continue
        rm -f "$f"
        ((found++)) || true
    done
    if (( found > 0 )); then
        log_ok "Deleted ${found} stale .beads-wisp/config/*.json entries"
    fi
fi

if [[ -f "$TOWN_ROOT/witness/bead-respawn-counts.json" ]]; then
    echo '{"beads": {}}' > "$TOWN_ROOT/witness/bead-respawn-counts.json"
    log_ok "Reset witness/bead-respawn-counts.json"
fi

# Stray top-level logs that pin a previous rig's name in grep output
# and confuse session-aware tooling.
for stray in witness.log freeride_live.log console.log; do
    if [[ -f "$TOWN_ROOT/$stray" ]]; then
        rm -f "$TOWN_ROOT/$stray"
        log_ok "Deleted ${stray}"
    fi
done

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

# ─── Phase 12: Write post-`gt up` HQ healer (Fix #101) ───────────────
#
# When the HQ Dolt DB is wiped (Phase 4) and then recreated by `gt up`,
# the new `hq` database comes up with an empty `config` table — no
# `issue_prefix=hq` row. The first HQ-bound `gt mail send` then fails
# with "database not initialized: issue_prefix config is missing",
# which blocks every mayor → architect/qa handoff.
#
# `gt rig add` runs `bd init --prefix <p> --database <db>` for each
# new rig DB which inserts the row, but there's no equivalent
# bootstrap for the HQ database, so post-wipe HQ never gets seeded.
#
# Until `gt up` learns to self-heal (Fix #101 follow-up: Go-level
# repair in the daemon's HQ-init code path), we emit a one-shot
# healer script alongside the wipe. The summary tells the operator
# to run it as `gt up && bash $TOWN_ROOT/.gt-post-reset-init.sh`.
# The script uses INSERT IGNORE so it's safe to run repeatedly and
# safe to run when the row already exists.

HEALER="$TOWN_ROOT/.gt-post-reset-init.sh"
log_info "Writing post-up HQ healer (${HEALER})..."
cat > "$HEALER" <<'HEALER_EOF'
#!/usr/bin/env bash
#
# Post-reset HQ healer (written by clean-gastown.sh / Fix #101).
#
# Run AFTER `gt up` has brought the Dolt server back online. Seeds the
# `hq.config.issue_prefix` row so HQ-bound mail sends (mayor inbox,
# planner inbox, etc.) no longer fail with "database not initialized".
#
# Safe to run multiple times: the INSERT IGNORE is a no-op if the row
# is already present.
set -euo pipefail

DOLT_HOST="${DOLT_HOST:-127.0.0.1}"
DOLT_PORT="${DOLT_PORT:-3307}"
HQ_PREFIX="${HQ_PREFIX:-hq}"

if ! command -v dolt >/dev/null 2>&1; then
    echo "[FATAL] dolt CLI not found in PATH" >&2
    exit 1
fi

if ! DOLT_CLI_PASSWORD="" dolt --host "$DOLT_HOST" --port "$DOLT_PORT" \
        --user root --no-tls sql -q "SELECT 1;" >/dev/null 2>&1; then
    echo "[FATAL] Dolt server not reachable on ${DOLT_HOST}:${DOLT_PORT}." >&2
    echo "         Run 'gt up' (or 'gt dolt start') first, then re-run me." >&2
    exit 1
fi

DOLT_CLI_PASSWORD="" dolt --host "$DOLT_HOST" --port "$DOLT_PORT" \
    --user root --no-tls sql -q "
USE hq;
INSERT IGNORE INTO config (\`key\`, value) VALUES ('issue_prefix', '${HQ_PREFIX}');
SELECT 'issue_prefix' AS \`key\`, value FROM config WHERE \`key\`='issue_prefix';
" 2>&1

echo "✓ HQ issue_prefix seeded (${HQ_PREFIX}) — gt mail send mayor/ should now work."
HEALER_EOF

chmod +x "$HEALER"
log_ok "Wrote post-up healer: ${HEALER}"

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
echo "    ${YELLOW}gt up${NC}                                          # Start NATS, Dolt, daemon, agents"
echo "    ${YELLOW}bash ${TOWN_ROOT}/.gt-post-reset-init.sh${NC}"
echo "                                                # ↑ Seed HQ issue_prefix (Fix #101)"
echo "                                                #   Required: without it, the first"
echo "                                                #   'gt mail send mayor/' will fail."
echo "    ${YELLOW}gt doctor${NC}                                      # Verify everything is healthy"
echo ""
echo "  One-liner:"
echo "    ${YELLOW}cd ${TOWN_ROOT} && gt up && bash ./.gt-post-reset-init.sh${NC}"
echo ""
echo "  Note: If agents were repeatedly crashing before cleanup, the daemon"
echo "  may have them in exponential backoff. 'gt up' will restart them cleanly."
echo ""
log_info "Next steps:"
echo "  1. Run '${YELLOW}gt rig add <name> <git-url>${NC}' to create new rigs"
echo "  2. Run '${YELLOW}gt mountain <epic>${NC}' to start building"
echo ""
