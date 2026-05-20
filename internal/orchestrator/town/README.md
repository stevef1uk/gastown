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

```bash
gt rig spec-index <rig>              # may emit delivery_phases for large SPECs
gt rig set-phase <rig> --list        # show phases and active id
gt rig set-phase <rig> p1-infra      # switch active phase (manual advance)
```

After QA `all_passed` for a phase, set the next phase id and restart or reset the workflow from planning (see operator runbook in plan docs).

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
