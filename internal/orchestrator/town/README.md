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
| `{{required_files}}` | Profile (comma-separated in prompts) |
| `{{min_architecture_bytes}}` / `{{min_plan_bytes}}` | Profile |
| `{{unittest_command_hint}}` | Profile (`qa_verify_command` or unittest module) |

Do not hard-code example project names or paths in prompts — use these variables.

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
