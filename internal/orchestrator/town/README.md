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

### Docker scaffold contracts

Scaffolding selects the application stack from required application files and
explicit SPEC declarations; Playwright/test-only Node files do not imply a Node
application. An explicit Next.js frontend plus FastAPI backend selects the hybrid
template even when an incomplete profile or LLM plan lists only Python files.
The SPEC's concrete Docker image tags, static-export COPY source/destination,
startup CMD and readiness URL override scaffold defaults. These substitutions
do not rewrite the workflow profile or existing non-empty agent-owned files.

Hybrid defaults build the frontend in /frontend, copy its out directory to
/app/frontend/out, install the backend project in /app/backend and run its ASGI
entrypoint. uv projects use their lockfile when declared and the same project
venv for installation and startup. The app image and Playwright runner use
separate Dockerfiles. A root compose file that declares an application Dockerfile
builds the app container rather than replacing it with a browser-runner image.

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
| **Two-stage QA review** | `prompts/rig-flow/qa_review.md` | QA reviews in two explicit passes: Stage 1 spec compliance (right thing, wired, tests cover plan.md acceptance), Stage 2 code quality (minimal, no dead code/stubs). |
| **Incremental architecture rework** | `prompts/rig-flow/design.md` | Existing architecture is read in full and amended only for named defects and directly affected references. Preserve unrelated and completed-phase contracts, implementation, tests, and databases. Full-document heredocs are for initial creation only. Missing files/beads are planning-sync/implementation recovery, not a redesign. |
| **Bead-note ledger** | `internal/formula/formulas/mol-polecat-work.formula.toml` | Polecat persists to `bd update --notes` at mandatory triggers (after writing tests, before verify, before `gt done`) so work survives context loss. |

**Both workflows share these contracts.** `req-flow.yaml` reuses `prompts/rig-flow/*.md` for its
`design`, `planning`, `plan_review`, `project_setup`, `implementation`, and `qa_review` states —
only `kickoff`, `analysis`, and `spec_review` are req-flow-specific. Editing the shared prompt
files above changes behavior for **both** rig-flow and req-flow. The `mol-polecat-work` formula is
workflow-agnostic (any polecat work lifecycle).

When changing a prompt, update this table if the contract it documents changes.

## FSM behavior belongs in YAML (not gt-agent Go)

When changing how a workflow step behaves (prompt size, bead queue text, empty-reply nudges, failure hints):

1. Edit **`templates/rig-flow.yaml`** hooks for that state and/or **`prompts/rig-flow/*.md`**.
2. Use hook fields: `omit_orchestrator_context`, `system_prompt_footer`, `user_prompt_wrapper: none`, `failure_prompt_context`, `empty_response_suffix`, `prompt_context`, `failure_hint`, `pre_run`, `state_timeout_seconds`, `on_timeout`, etc.
3. Use **`workflow-profile.json`** for per-rig paths, verify commands, and `required_files`.

Do **not** add `if task.State == "implementation"` (or any state name) in `cmd/gt-agent`. That bypasses town sync and duplicates config.

Reviews can set `require_explicit_outcome: true` to require an agent verdict instead of artifact auto-completion. This also accepts early failure verdicts without asking the reviewer to repair implementation. Both workflows enable this for QA plan review and Tester test review. Plan review cannot convert a reviewer failure into success based on mechanical artifact checks. Successful file inspection cannot clear a failed verification, and failed verification invalidates any earlier green result.

Planning sync regenerates missing or misaligned plans and preserves valid nested-path plans, including Planner-authored acceptance and verification corrections. Planner may amend plan.md for nested layouts; exact bead paths are validated on completion. Rework requires a plan amendment during the attempt before explicit or automatic success; read-only inspection is insufficient. Non-Go artifacts in phased Go projects use the active phase's verification command rather than Go compilation.

When implementation fails in a phased Go project, recovery also checks existing dependencies with a bounded Go build using `-mod=readonly` and discarding binary output. Compiler paths owned by an earlier phase rewind to that phase and reopen its beads; repair stays with Polecat, preserves compiler feedback, and records the original phase to resume afterward. Active-phase errors, unowned files, toolchain failures, and missing external modules do not trigger this dependency rewind.

Python dependency recovery uses the captured failed-verification traceback rather than importing application modules or replaying startup commands. Syntax/name/type errors use the terminal first-party frame; missing imported symbols or module attributes use the implicated provider's owned path. The same owning-phase rewind applies even when the active phase is a browser harness or Python uses no virtualenv. Third-party providers, declared external dependencies, virtualenv files, ambiguous module names, and active/future-phase files do not trigger an earlier-phase source rewind.

Test-only Go compilation failures and Python test import/collection errors can also select a uniquely owned source/test pair within the current phase. Recovery reopens the source bead, returns the blocked entrypoint bead to open, and keeps Polecat in implementation with the failure evidence. Delivery progress and any original phase to resume are preserved. The queue honors the in-progress repair bead; read/write scope includes only its identified test companion, and Python per-source verification includes existing conventional companions. Unrelated, ambiguous, and future-phase tests are not claimed.

Tester verification credit requires the complete configured command (allowing mayor/rig directory prefixes and harness-managed rewrites). Backgrounded suites, health probes, discovery-only flags, alternate configurations, and exit-masking wrappers cannot substitute for that command. Only recognized Node development-server commands are eligible for smoke rewriting; `npx playwright test` stays a foreground test runner.

Tester rejection resolves scoped planned tests to their explicit test bead or unique Go/Python source companion owner before handing back to Polecat. Repairs select one owning delivery phase at a time, preserve any original resume phase, reopen the selected beads, and carry their IDs in rework feedback so reconciliation cannot auto-close them. Invalid or unresolved ownership routes through the configured `plan_gap` transition and unfreezes the test plan. Tester review is limited to active/completed phases and SPEC-backed requirements; passing verification alone cannot dismiss pending coverage rework without a repair attempt.

Reusable hook implementations live in `internal/orchestrator/prompt_context.go` — add a **named** hook only when YAML cannot express it. See the maintainer block at the top of that file.

Design entry must never reset the implementation layout. Fresh-clone cleanup is performed by rig creation/adoption, not by repeatable workflow states. The legacy `reset_layout_pre_implementation` workflow hook is a preserving no-op so older town templates or cached tasks cannot delete implementation files on rework.

## State timeout (wall-clock + turn budget)

Some FSM states can declare a **wall-clock limit** and **cleanup hooks** when the step is stuck. This is separate from `max_cmd_turns` (how many LLM rounds one `fetch_task` invocation gets).

| Mechanism | Config | When it fires | Default on `planning` |
|-----------|--------|---------------|------------------------|
| **Turn budget** | `max_cmd_turns` | Planner exhausts CMD turns in one session without valid JSON | `8` |
| **Completion no-progress budget** | `no_progress_limit` | Rejected completion claims in one agent attempt without changes to required files, effective profile, or workflow docs | `5` for implementation |
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
| `LEARN: <rule>` / `LEARN[<stack>]: <rule>` | Persist a concise (≤140 chars) self-learning rule to project and town stores |

`CMD:` remains for `bd`, **Verify**, and `go run`. Auto-verify runs after EDIT/WRITE. **sed/patch/heredoc** still work as fallback.

### Technology-specific Town Hall coding standards (`coding_standards` hook)

Stack-specific coding standards live under `{townRoot}/orchestrator/standards/` (synced from `internal/orchestrator/town/standards/`):

| File | Stack | When injected |
|------|-------|---------------|
| `standards/go.md` | `go` | Active `.go` / `go.mod` bead or `WorkflowUsesGo` phase |
| `standards/python.md` | `python` | Active `.py` / `requirements.txt` bead or `WorkflowUsesPython` phase |
| `standards/typescript.md` | `typescript` | Active `.ts`/`.tsx`/`.js`/`.jsx`/`.html`/`.css` bead or Node/web phase |
| `standards/sql.md` | `sql` | Active `.sql` bead, or store/schema bead in a phase with `.sql` files |
| `standards/docker.md` | `docker` | Active `Dockerfile` / `docker-compose.yml` bead or `WorkflowUsesDocker` phase |

- **Strict stack isolation:** `DetectActiveCodingStacks` inspects the active implement bead path first and falls back to `v.ForActivePhase()`. Go projects never receive Python standards and vice versa.
- **Context budget:** `FormatCodingStandardsBlock` substitutes profile variables (such as `{{layout_root}}`) and enforces a hard `900 byte` cap.

### Polecat self-learning (`polecat_learnings` hook)

Polecat records and reuses concise lessons across beads, QA rework cycles, delivery phases, and rigs:

| Tier | Path | Scope & Retention |
|------|------|-------------------|
| **Project-scoped** | `{rig}/mayor/rig/.gastown/polecat-learnings.json` | Persists across all beads/phases of the rig (`max 8` rules) |
| **Town-scoped by stack** | `{townRoot}/orchestrator/learnings/<stack>.json` | Shared across rigs using the same technology stack (`max 8` rules per stack) |

**How learnings are captured:**
1. **Explicit directive:** Polecat emits `LEARN: <one-line rule>` (or `LEARN[go]: <rule>`) in an implementation turn.
2. **Outcome JSON:** Polecat includes `"learning": "..."` (or `"learnings": [...]`) in its terminal outcome JSON.
3. **Automatic recovery extraction:** When a bead fails verification (`LastVerifyFailOutput`) and subsequently passes verification, or when a `qa_review` / `test_review` rework (`PendingRework`) is resolved, `gt-agent` distils a 1-line rule automatically.

**Context & ring-buffer bounds:**
- Each rule is normalized to a single line of at most `140 characters`.
- Duplicate rules increment `hits` and refresh `updated_at`; when a store exceeds `8 entries`, the oldest entry with the lowest `hits` is evicted.
- `FormatPolecatLearningsBlock` filters entries to the active stack(s) only and caps injected prompt text at `800 bytes`.

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
  STANDARDS.md               # General architecture, TDD, and workflow standards
  SECURITY_STANDARDS.md      # Deterministic secret-scanner rules
  standards/*.md             # Stack-specific Town Hall coding standards (go, python, typescript, sql, docker)
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
