# gt-agent Migration Plan: Replacing Claude/OpenCode with Headless NATS Agents

## Context

Gas Town previously used Claude Code and OpenCode (TUI-based agents) running inside tmux sessions. We are migrating to `gt-agent` — a lightweight, headless, NATS-based agent that:
- Runs as ephemeral OS processes (no tmux, no TUI)
- Communicates via NATS message bus on port 4222
- Calls LLMs through the Freeride proxy (localhost:11434)
- Executes shell commands and `gt` subcommands autonomously

---

## Functional Summary

### What Works Now (Phase 0 + 1 Complete)

1. **`gt down` stops NATS sessions correctly**
   - Refineries, witnesses, crew, town sessions all stopped via provider interface
   - No more orphaned `gt-agent` or `nats-wrapper` processes
   - `NatsProvider.IsAgentRunning()` checks actual PID instead of returning `true`

2. **`gt-agent` polecat can execute single-shot work**
   - Drains nudges from queue
   - Calls `gt prime --hook` for full role context + work injection
   - Calls LLM via Freeride proxy
   - Executes generated shell commands
   - Calls `gt done` after work completes
   - Exits cleanly

3. **Build & install**
   - `make install` builds `gt` + `gt-agent` and installs to `~/.local/bin`
   - Daemon auto-restarts on binary update
   - Tests pass (`go test ./internal/cmd/`, `./internal/session/`)

---

## Remaining Phases

### Phase 2: Long-Lived Event Loop (IN PROGRESS)

**Goal**: `gt-agent` becomes a persistent process that polls for work, sleeps with backoff, and handles multiple cycles before exiting.

**Required changes** (`cmd/gt-agent/main.go`):
- [ ] Wrap execution in a `for { ... }` loop
- [ ] Add backoff sleep: 30s → 60s → 120s → ... → 15min max
- [ ] Add `SIGTERM` handler for graceful shutdown
- [ ] Read/write `state.json` (patrol_count, extraordinary_action, last_patrol)
- [ ] Detect handoff markers and include handoff context in LLM prompt
- [ ] After each cycle, update state and decide: loop / handoff / exit
- [ ] Support idle timeout: exit after N cycles with no work (daemon respawns)

**Why this matters**: Deacon, Witness, Mayor, and Refinery are not one-shot workers. They run patrol cycles, monitor health, and wait for events. Without an event loop, each cycle spawns a new process and loses all context.

---

### Phase 3: Deacon (mol-deacon-patrol)

**Goal**: Deacon runs 25-step patrol cycles autonomously.

**Required changes**:
- [ ] `gt-agent` parses formula steps from `gt prime --hook` output
- [ ] Step tracking: call `bd close <step-id>`, check `bd mol current` for next step
- [ ] Execute each step via LLM (fresh call per step or per batch)
- [ ] Update `deacon/heartbeat.json` at start of each patrol
- [ ] Run orphan cleanup, test-pollution cleanup, health scan, wisp compact
- [ ] Write patrol report via `gt patrol report`
- [ ] Handle `await-signal` / sleep between patrols

**Template updates** (`internal/templates/roles/deacon.md.tmpl`):
- [ ] Remove `tmux has-session` references → replace with `gt session status`
- [ ] Update health check instructions for NATS

---

### Phase 4: Witness (mol-witness-patrol)

**Goal**: Witness monitors polecats and handles lifecycle requests.

**Required changes**:
- [ ] Add `gt polecat list`, `gt session status` to LLM toolkit
- [ ] Support `gt peek` equivalent for NATS sessions (read wrapper logs)
- [ ] Zombie detection: check if polecat process exists but hasn't made progress
- [ ] Nudge stuck polecats toward completion
- [ ] Process LIFECYCLE:Shutdown, SPAWN:, HANDOFF mail

**Template updates** (`internal/templates/roles/witness.md.tmpl`):
- [ ] Replace `tmux has-session` with `gt session status`
- [ ] Replace `gt peek` (tmux-specific) with NATS log reading
- [ ] Update zombie detection instructions

---

### Phase 5: Mayor & Refinery

**Goal**: Coordinator and merge queue agents.

**Mayor**:
- [ ] Dispatch work via `gt sling <bead> <rig>`
- [ ] Monitor convoys (`gt convoy list`)
- [ ] Handle escalations from witnesses
- [ ] Undock/dock rigs based on work load

**Refinery**:
- [ ] Poll merge queue for open MRs
- [ ] Process each MR: review, merge, call `PostMerge()`
- [ ] Handle CI failures and retry logic

---

### Phase 6: Remove opencode Crew TUI

**Goal**: All agents use `gt-agent`, no more interactive TUI.

**Required changes** (`settings/config.json`):
- [ ] Remove `crew: opencode` from `role_agents`
- [ ] Remove `opencode` agent definition
- [ ] Set `crew: gt-agent-local` (or remove crew if not needed)

**Note**: Crew agents are persistent collaborators (digital twins). They need the same event loop + state persistence as infrastructure agents, but may also support interactive stdin mode for human collaboration.

---

## Critical Files Modified So Far

| File | Change |
|------|--------|
| `cmd/gt-agent/main.go` | Call `gt prime --hook` instead of bare `gt prime` |
| `internal/cmd/down.go` | Use provider interface for all session stops; remove tmux hardcoding |
| `internal/session/nats_provider.go` | Fix `IsAgentRunning()` to check actual PID |

---

## Next Immediate Action

Commit the Phase 0+1 changes, then implement Phase 2 (event loop) in `cmd/gt-agent/main.go`.

## Test Plan for Phase 2

1. `gt up` → spawn deacon `gt-agent`
2. Verify process stays alive (`ps aux | grep gt-agent`)
3. Verify it loops: poll → sleep → poll
4. Send nudge while sleeping → verify wake + process
5. `gt down` → verify SIGTERM received → process exits cleanly
6. Check `state.json` for patrol_count increment
