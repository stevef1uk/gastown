# Orchestrator town assets (source of truth in gastown)

Workflow FSM templates and per-state prompts are **authored here** in the gastown repo and
installed into your town at `{townRoot}/orchestrator/` by:

- `make install` (development — syncs missing + changed files)
- `gt install` (provisions missing files on a new town)
- `gt orchestrator sync --update-changed` (manual sync from embedded assets)

The running orchestrator loads templates from `{townRoot}/orchestrator/templates/` when
`gt orchestrator run` / `gt up` starts the service. Workflow state persists in
`{townRoot}/orchestrator/instances.json` (not in git — use `gt mayor workflow reset` to rewind).

## Per-rig workflow profile

Validation and prompt variables for a rig come from:

```
{rig}/mayor/rig/.gastown/workflow-profile.json
```

Generate or refresh from `SPEC.md`:

```bash
gt rig spec-index <rig>
gt rig spec-index <rig> --force
```

`gt rig add` / `gt rig adopt` run spec-index automatically when `SPEC.md` exists.

`rig-flow.yaml` in this directory carries **placeholder** `validation:` defaults only.
At runtime, **profile overrides template overrides Go defaults**.

## HTTP implementation profiles (GT-VERIFY-011)

Stack-specific handler **write guards** and **verify hints** (ServeMux traversal, test cwd, etc.) live in JSON — not in compiled gt-agent:

| Location | Purpose |
|----------|---------|
| `{townRoot}/orchestrator/http-profiles/<name>.json` | Town-wide profiles (synced from gastown) |
| `{rig}/mayor/rig/.gastown/http-implementation.json` | Per-rig profile selection + overrides |

**You do not create these by hand.** `gt rig spec-index`, `project_setup`, and `implementation` pre_run call `ensure_http_implementation_config`, which writes `http-implementation.json` when the rig is Go + HTTP (web/server, handler beads, or `/static/` in architecture). Profile name and `traversal_probe_path` come from **SPEC/architecture** only. Edit the JSON only when you need different stack guards or hints (no gt-agent rebuild).

Example rig file (auto-created; tweak if needed):

```json
{
  "profile": "go-stdlib-servemux",
  "overrides": {
    "hints": {
      "traversal_redirect": {
        "fixes": ["Custom line injected without rebuilding gt-agent."]
      }
    }
  }
}
```

Use `"profile": "generic"` to disable stack-specific guards when architecture uses a non-stdlib router. Routes and smoke paths still come from **architecture.md** via `ParseWebStaticMapping` / `LoadAPISmokeSpecFromRig`.

After editing JSON, restart or nudge the polecat — no `go build` required.

## Prompt variables

Rig-flow prompts (`prompts/rig-flow/*.md`) are spec-driven. Common placeholders:

| Variable | Source |
|----------|--------|
| `{{rig}}` | Workflow instance `--rig` |
| `{{spec_summary}}` | Profile |
| `{{layout_root}}` | Profile (e.g. `defender`, `backend`) |
| `{{bead_title_contains}}` | Profile |
| `{{required_files}}` | Active phase paths (comma-separated); same as union when no phases |
| `{{all_required_files}}` | Full union across all delivery phases |
| `{{active_phase_id}}` / `{{phase_scope_note}}` | Phased delivery (from `delivery_phases` in profile) |
| `{{min_architecture_bytes}}` / `{{min_plan_bytes}}` | Profile |
| `{{unittest_command_hint}}` | Profile (`qa_verify_command` or unittest module) |

Do not hard-code example project names or paths in prompts — use these variables.

## Rig-flow prompt conventions (superpowers)

Rig-flow prompts enforce process guarantees inspired by
[obra/superpowers](https://github.com/obra/superpowers). Keep these conventions
intact when editing the prompts — they are behavioral contracts, not suggestions:

| Convention | Where | Contract |
|------------|-------|----------|
| **TDD Iron Law** | `prompts/rig-flow/implementation.md` | No production code without a failing test first. Per-bead red-green-refactor: write test → verify RED → minimal implementation → verify GREEN → refactor. Tests committed in a **separate commit** before implementation. |
| **YAGNI** | `prompts/rig-flow/planning.md`, `prompts/rig-flow/qa_review.md` | Beads implement exactly what SPEC requires. QA rejects correct-but-overbuilt code. |
| **Bead map contracts** | `prompts/rig-flow/planning.md` | Each `### <id>: <path>` bead block lists `Interfaces` (exact exported symbols from architecture), `Depends on`, and `Consumed by` so the polecat gets dependency wiring without reading the whole plan. |
| **Design self-review gate** | `prompts/rig-flow/design.md` | Architect must pass a placeholder/SPEC-alignment/consistency/path-coverage scan before reporting success. |
| **Two-stage QA review** | `prompts/rig-flow/qa_review.md` | QA reviews in two explicit passes: Stage 1 spec compliance (right thing, wired, tests cover plan.md acceptance), Stage 2 code quality (minimal, no dead code/stubs). |
| **Bead-note ledger** | `internal/formula/formulas/mol-polecat-work.formula.toml` | Polecat persists to `bd update --notes` at mandatory triggers (after writing tests, before verify, before `gt done`) so work survives context loss. |

When changing a prompt, update this table if the contract it documents changes.

## FSM behavior belongs in YAML (not gt-agent Go)

When changing how a workflow step behaves (prompt size, bead queue text, empty-reply nudges, failure hints):

1. Edit **`templates/rig-flow.yaml`** hooks for that state and/or **`prompts/rig-flow/*.md`**.
2. Use hook fields: `omit_orchestrator_context`, `system_prompt_footer`, `user_prompt_wrapper: none`, `failure_prompt_context`, `empty_response_suffix`, `prompt_context`, `failure_hint`, `pre_run`, `state_timeout_seconds`, `on_timeout`, etc.
3. Use **`workflow-profile.json`** for per-rig paths, verify commands, and `required_files`.

Do **not** add `if task.State == "implementation"` (or any state name) in `cmd/gt-agent`. That bypasses town sync and duplicates config.

Reusable hook implementations live in `internal/orchestrator/prompt_context.go` — add a **named** hook only when YAML cannot express it. See the maintainer block at the top of that file.

## State timeout (wall-clock + turn budget)

Some FSM states can declare a **wall-clock limit** and **cleanup hooks** when the step is stuck. This is separate from `max_cmd_turns` (how many LLM rounds one `fetch_task` invocation gets).

| Mechanism | Config | When it fires | Default on `planning` |
|-----------|--------|---------------|------------------------|
| **Turn budget** | `max_cmd_turns` | Planner exhausts CMD turns in one session without valid JSON | `8` |
| **Wall-clock timeout** | `state_timeout_seconds` | `state_entered_at` in `instances.json` is older than the limit when `fetch_task` runs | `1800` (30 min) |
| **Cleanup** | `on_timeout` | Before `complete_task` with outcome `timeout` | `[reset_planning_phase]` |
| **FSM edge** | `transitions.timeout.to` | After cleanup; same as failure for planning (`planning` again) | `planning` |

### Purpose

Planning often leaves the rig in a bad partial state: glued bead titles (`ImplementDockerfile`), fake IDs in `plan.md` (`fi-001`), or duplicate/missing implement beads. Retrying with `failure → planning` alone keeps that dirt. A **timeout** runs **`on_timeout` hooks** first, then transitions with outcome **`timeout`** so the next planner turn starts from a clean scaffold.

For **planning**, `reset_planning_phase`:

1. Deletes open beads whose titles look like implement tasks for this phase (`implement` + `per arch` in the title).
2. Removes stale `plan.md` (too small or placeholder bead IDs).
3. Recreates one canonical `bd create` per active-phase `required_files` path.
4. Dedupes and prunes malformed titles (via `repair_planning_beads` logic).

The planner receives **`pending_rework`** on the next `fetch_task` (same as cross-step failure) explaining that a timeout occurred and artifacts were reset.

### YAML example (`planning` in `rig-flow.yaml`)

```yaml
hooks:
  state_timeout_seconds: 1800
  on_timeout: [reset_planning_phase]
  max_cmd_turns: 8
transitions:
  success:
    to: plan_review
  failure:
    to: planning
  timeout:
    to: planning
```

### Implementation notes

- **`state_entered_at`** is set on `StartWorkflow`, `ResetWorkflow`, and every `Transition` (including `timeout → planning`).
- **`FetchTask`** checks the limit before dispatching work; on timeout it runs `on_timeout` and calls `complete_task` with outcome `timeout` (no agent LLM call that poll).
- **`gt-agent`** also runs `on_timeout` when `max_cmd_turns` is exhausted and the state defines a `timeout` transition (outcome `timeout` instead of `failure`).
- Add new cleanup steps in `RunOnTimeoutHook` in `prompt_context.go` (same pattern as `pre_run`).

### Operator signals

```bash
gt mayor workflow status wf-1          # still planning after long wait
tail -f ~/gt/logs/orchestrator.log     # "state timeout wf=… planning -> planning"
export BEADS_DIR=~/gt/<rig>/.beads && bd list --status=open --flat
```

After timeout, expect fresh implement beads (`Implement <path> per architecture` with a **space** after `Implement`) and no `plan.md` until the planner writes a new one.

### Implementation stall recovery (two tiers)

| Mechanism | Config | When it fires |
|-----------|--------|---------------|
| **Wall-clock timeout** | `state_timeout_seconds: 7200` | Implementation state exceeds 2h without transition (override: `GT_STATE_TIMEOUT_SECONDS`) |
| **Per-CMD timeout** | `cmd_timeout_seconds: 900` | Any single shell CMD (heredoc, `go run`, build) exceeds 15m |
| **Light cleanup** | `on_timeout: [recover_implementation_stall]` | **gt-agent max CMD turns**: stop dev servers, `in_progress` → `open`, one active bead |
| **Hard targeted reset** | `on_state_timeout: [reset_implementation_phase]` | **Orchestrator wall-clock timeout**: delete files for **open / in_progress** implement beads only, reset `in_progress`→`open`, clear `implementation-progress.json` (closed beads untouched) |
| **FSM edge** | `transitions.timeout.to: implementation` | Polecat gets a fresh turn with `pending_rework` |

Manual hard reset (same as `reset_implementation_phase`):

```bash
GT_ROOT=~/gt RIG=testgt3 ./scripts/reset-implementation-phase.sh
# preview: --dry-run
```

Hung `go run` or a stuck heredoc no longer blocks the rig indefinitely: gt-agent kills the command after `cmd_timeout_seconds`, and the mayor orchestrator can time out the whole step and run the recovery hook.

### Native file tools (READ / EDIT / WRITE)

Implementation state sets `native_edit_tools: true` in `rig-flow.yaml`. The polecat can edit without fragile shell heredocs:

| Tool | Purpose |
|------|---------|
| `READ: <path>` | Show file (active bead + dependency packages) |
| `EDIT: <path>` + `<<<<<<< SEARCH` / `=======` / `>>>>>>> REPLACE` | Exact search/replace (must match once) |
| `WRITE: <path>` … `---END WRITE---` | New files only (rejected on large existing files) |

`CMD:` remains for `bd`, **Verify**, and `go run`. Auto-verify runs after EDIT/WRITE. **sed/patch/heredoc** still work as fallback.

### Incremental edits (sed / patch vs full-file heredoc)

When an implement bead’s file **already exists** on disk (and is not a stub), gt-agent **rejects** `cat > path <<'EOF'` and full **`WRITE:`** rewrites; use **`EDIT:`** search/replace instead. **Implement context** includes an **Incremental edit required** block when applicable.

**Codeindex (optional):** dependency blast radius for implement beads — see freeride `README.md` → **Gas Town Integration** → **Polecat host tools (optional)** → **Codeindex**. Summary:

| Item | Detail |
|------|--------|
| Install | `pip install codeindex` on the **polecat** host (`codeindex` on `PATH`) |
| Index file | `{townRoot}/{rig}/mayor/rig/codeindex.json` |
| Analyze root | `{mayor}/rig/{layout_root}/` (e.g. `linkshelf/`) from `workflow-profile.json` |
| When | `refresh_codeindex` in implementation `pre_run`; per-bead `codeindex impact` in **Implement context** |
| Disable | `GT_CODEINDEX=0` or `CODEINDEX=0` in polecat `gt-agent` env |
| Manual | `codeindex analyze <layout> --output codeindex.json` from `mayor/rig`; impact paths relative to layout |

**goimports (Go):** optional; gt-agent runs it on the package after native EDIT/WRITE when verify reports unused imports.

Restart a dead polecat session: `gt up` (pipeline liveness restarts gt-agent without `--orchestrated`), or `scripts/reset-rig-orchestrator.sh` for a full rig rewind.

### Orchestrator MCP liveness (daemon patrol)

The **orchestrator process** (`gt orchestrator run`, PID in `daemon/orchestrator.pid`) can hang while the PID still exists. The **daemon recovery heartbeat** (default ~3 min) now:

| Check | Threshold | Action |
|-------|-----------|--------|
| **NATS ping** | `ping` MCP tool, 5s timeout | Restart if no response |
| **Heartbeat file** | `daemon/orchestrator-heartbeat.json` stale > 2 min (ping must succeed first) | Restart |
| **Process dead** | PID file missing / signal 0 fails | **Start** (not restart) |

After restart, pipeline agents are reconciled on the same daemon tick (`ensureMayorRunning`, `ensureRigPolecatsRunning`, …). Manual: `gt orchestrator stop && gt orchestrator start`, or `gt up`.

## Configuration

Town operators set orchestrator behavior in **`{townRoot}/settings/config.json`**:

```json
"orchestrator": {
  "default_workflow": "rig-flow",
  "auto_start": false
}
```

See `docs/examples/town-settings.example.json` and [Orchestrator (concept)](../../../docs/concepts/orchestrator.md#configuration).

Requires `session_transport: "nats"` in the same file for orchestrator MCP.

## Layout

```
town/
  templates/rig-flow.yaml    # FSM: kickoff → design → planning → plan_review → project_setup → implementation → qa_review
  prompts/rig-flow/*.md      # Per-state system prompts (prompt_file in YAML)
```

## Pause / resume

```bash
gt mayor workflow pause wf-1              # pause + gt rig shutdown <rig> --force
gt mayor workflow pause --rig testgt3     # pause all running workflows on a rig
gt mayor workflow pause wf-1 --no-shutdown
gt mayor workflow resume wf-1             # status=running; then gt rig boot <rig> or gt up
```

Paused workflows keep `current_state` in `instances.json` but do not receive `fetch_task` work and do not block `gt mayor workflow start` on the same rig.

With the orchestrator running, `gt up` starts rig agents **only** for rigs that have a **running** workflow. Paused rigs are skipped (witness/refinery/architect/qa/polecat stay down after `gt rig shutdown`).

## Phased delivery (large SPECs)

When `workflow-profile.json` includes `delivery_phases`, rig-flow scopes planning beads, polecat queue, and QA to the **active** phase only. The architect still documents the full system (`all_required_files`).

**Go + `internal/store/` rigs:** `ClampProfileValidation` may inject `{{layout_root}}/internal/store/schema.go` when the profile lists store `.go` files but omits a DDL owner. The architect documents the real table names in **that rig’s** `architecture.md`; polecat implements the injected path per architecture, not a fixed example schema.

```bash
gt rig spec-index <rig>              # may emit delivery_phases for large SPECs
gt rig normalize-profile <rig>       # rewrite profile (docker → final phase, layout clamp)
gt rig set-phase <rig> --list        # show phases and active id
gt rig set-phase <rig> --list        # show phases and active id
gt rig set-phase <rig> p1-infra      # switch active phase (manual advance)
```

After QA `all_passed` for a phase, the orchestrator **automatically** sets the next `active_phase_id`, syncs beads/plan.md, and transitions the workflow to **planning**. Use `gt rig set-phase` only for manual overrides.

## Start rig-flow

See **[Quickstart: rig-flow on testgt2](../../../docs/concepts/orchestrator.md#quickstart-rig-flow-on-testgt2)** for the full walkthrough (beads DB, QA outcomes, agent console).

```bash
cd ~/gt
gt rig spec-index <rig>    # if not already indexed
gt up
gt mayor workflow start rig-flow --rig <rig>
gt mayor workflow status
tail -f logs/orchestrator.log
```

## Rewind after deleting artifacts

Deleting `architecture.md` or `plan.md` in git does **not** change the FSM:

```bash
gt mayor workflow reset wf-1 --to design
gt up --orchestrator-only
```

## Which sessions to tail

For `rig-flow` with `--rig testgt2`, see the session table in
[Orchestrator (concept)](../../../docs/concepts/orchestrator.md#which-sessions-to-watch-rig-flow-on-testgt2).
Summary: mayor and planner at town root; architect/qa/polecat under `testgt2/` (`testgt2/polecat/` for rig-flow implementation); per-bead workers remain under `testgt2/polecats/*`.

## Edit workflow

1. Change `templates/*.yaml` and/or `prompts/<template>/*.md` in this directory.
2. From the gastown repo: `make install` (or `gt orchestrator sync --update-changed` from town root).
3. Restart is usually not required for template text; restart orchestrator if the service failed to load YAML.
