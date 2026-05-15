# Orchestrator (technical design)

Technical reference for the Gas Town workflow FSM and MCP service introduced on the
`mcp-orchestrator` branch. For operators, see [Orchestrator (concept)](../concepts/orchestrator.md).

**Status:** Phases 1–3 implemented on `mcp-orchestrator`. Rig-flow FSM, NATS MCP,
persistence, auto-start, per-state prompts, orchestrated agent loop, topology guards,
and unit tests are in place. Remaining gaps: QA outcome aliases, duplicate hq agents
with multiple rigs, full template schema unification for all bundled YAML examples.

## Status summary

| Area | Status | Notes |
|------|--------|-------|
| YAML template load | Done | `{townRoot}/orchestrator/templates/*.yaml` |
| FSM transition | Done | `WorkflowInstance.Transition` in `types.go` |
| MCP over NATS | Done | Subject `gt.orchestrator.mcp` |
| `gt-agent --orchestrated` | Done | Poll, CMD loop, JSON outcome, state guards |
| `gt orchestrator run` | Done | Subprocess from `gt up`; PID + log |
| `gt mayor workflow start\|status\|complete` | Done | CLI + MCP `start_workflow` |
| Per-state `prompt_file` | Done | `internal/orchestrator/town/prompts/rig-flow/*.md` |
| Structured JSON outcome | Done | `outcome` / `summary` validated vs transitions |
| Idle poll-only (no LLM) | Done | Empty `fetch_task` → sleep, no model call |
| Pipeline roles orchestrator-only | Done | `OrchestratedForRole`; no `gatherWork` when orchestrated |
| Workflow persistence | Done | `{townRoot}/orchestrator/instances.json` |
| Auto-start on `gt up` / `gt rig add` | Done | `orchestrator.auto_start` + `default_workflow` |
| Variable substitution `{{rig}}` | Done | `SubstituteVars` in prompts and instructions |
| `get_workflow_status` MCP tool | Done | + `gt mayor workflow status` |
| Town asset sync | Done | `//go:embed town/`; `make install`, `gt orchestrator sync` |
| Patrol agents not orchestrated | Done | `IsPatrolRole` / `OrchestratedForRole` |
| hq-polecat + legacy pause | Done | `LegacyPolecatsPaused`; `GT_POLECAT` identity fix |
| Planning / implementation guards | Done | `cmd/gt-agent/orchestrated.go` |
| Bundled non-rig-flow templates | Partial | Some examples still use old schema |
| QA outcome mapping | Partial | Prefer `task_passed` / `all_passed` in FSM |
| Unit tests | Done | `manager_test.go`, `prompts_test.go`, `legacy_test.go`, … |

## Architecture

```mermaid
sequenceDiagram
  participant Op as Operator
  participant Mayor as gt mayor
  participant NATS as NATS gt.orchestrator.mcp
  participant Orch as gt orchestrator run
  participant Agent as gt-agent orchestrated

  Op->>Mayor: workflow start rig-flow
  Mayor->>NATS: start_workflow
  NATS->>Orch: Manager.StartWorkflow
  loop Poll
    Agent->>NATS: fetch_task agent_id=architect
    NATS->>Orch: Manager.FetchTask
    Orch-->>Agent: workflow_id state instructions
    Agent->>Agent: LLM + CMD blocks
    Agent->>NATS: complete_task outcome
    NATS->>Orch: Manager.CompleteTask
    Orch->>Orch: Transition FSM
  end
```

### Components

| Component | Package / command | Role |
|-----------|-------------------|------|
| **Manager** | `internal/orchestrator/manager.go` | Templates + instances; `FetchTask`, `CompleteTask`, `StartWorkflow` |
| **Server** | `internal/orchestrator/mcp.go` | JSON-RPC 2.0; stdio + NATS subscriber |
| **Client** | `internal/orchestrator/orchestrator.go` | `Call`, `FetchTask`, `CompleteTask`, `StartWorkflow`; PID file |
| **Types** | `internal/orchestrator/types.go` | `WorkflowTemplate`, `State`, `WorkflowInstance` |
| **CLI** | `internal/cmd/orchestrator.go` | `gt orchestrator start\|stop\|status\|run` |
| **Mayor CLI** | `internal/cmd/mayor.go` | `workflow start\|status\|complete` |
| **Prompts / matching** | `internal/orchestrator/prompts.go` | `AgentMatchesTask`, `OrchestratedForRole`, `LoadPromptFile` |
| **Legacy pause** | `internal/orchestrator/legacy.go` | `LegacyPolecatsPaused` during active `rig-flow` |
| **Agent loop** | `cmd/gt-agent/main.go` | `runOrchestrated()` when `--orchestrated` |
| **Session flag** | `internal/session/lifecycle.go` | Appends `--orchestrated` when orchestrator PID exists |
| **Town up** | `internal/cmd/up.go` | Starts orchestrator; sets `Orchestrated` on agents |

**Production entry:** `gt orchestrator run` (not the stale `cmd/gt-orchestrator/main.go` binary).

**PID file:** `{townRoot}/daemon/orchestrator.pid`  
**Logs:** `{townRoot}/logs/orchestrator.log`

## Workflow template schema (canonical)

Templates must match Go structs in `types.go` (today). **Target** adds `prompt_file`:

```yaml
id: rig-flow
description: Standard rig pipeline
initial_state: kickoff
states:
  kickoff:
    role: mayor
    prompt_file: prompts/rig-flow/kickoff.md   # relative to {townRoot}/orchestrator/
    instructions: |                            # short user-task hint (optional)
      Verify SPEC for {{rig}}.
    transitions:
      success:
        to: design
      failure:
        to: kickoff
```

### Prompt file layout (target)

```
{townRoot}/orchestrator/
  templates/rig-flow.yaml
  prompts/rig-flow/
    kickoff.md      # system prompt for this state only
    design.md
    planning.md
    implementation.md
    qa_review.md
```

- Lift rules from `internal/templates/roles/*.md.tmpl` into state files; keep each file
  focused on **one FSM step** for weaker LLMs.
- `FetchTask` loads file content, substitutes `{{rig}}` etc. from instance variables,
  returns `system_prompt` + `task_prompt` to `gt-agent`.

**Runtime YAML** (`rig-flow.yaml`) sets both `prompt_file` and short `instructions`.
`FetchTask` returns `system_prompt` (file) and task text (instructions + substituted vars).

**Terminal states:** Transitioning *into* state name `completed` or `failed` sets instance
`Status` to that value.

**Invalid bundled format** (do not use):

```yaml
# WRONG for current loader
agent_role: architect
transitions:
  success: prd-review    # must be success: { to: prd-review }
```

## Prompt assembly

### Legacy (`gt-agent` default loop) — not used for pipeline when orchestrator owns dispatch

1. `buildSystemPrompt()` + `gt prime --hook` + `gatherWork()`
2. Being **removed** for pipeline roles (mayor, architect, planner, polecat, qa) when orchestrator runs

### Orchestrated — target (confirmed design)

| Message | Source |
|---------|--------|
| **System** | Contents of `prompt_file` for current state (+ small wrapper: workflow_id, state, allowed outcomes) |
| **User** | “Complete this step only.” + optional YAML `instructions` / substituted vars |

**One LLM task per `fetch_task`:** execute step → structured result → `complete_task` → poll again.

**Structured output (target):**

```json
{
  "outcome": "success",
  "summary": "architecture.md written",
  "commands_run": ["wc -c testgt2/mayor/rig/architecture.md"]
}
```

Validate `outcome` against keys in `state.transitions` before advancing FSM.

**Idle:** `fetch_task` empty → `sleep(poll_interval)` → **no LLM**.

### Orchestrated — current (`runOrchestrated`)

`cmd/gt-agent/orchestrated.go`:

1. `fetch_task` → load `system_prompt` from `prompt_file`
2. Multi-turn **CMD:** loop with per-state command guards
3. JSON `{"outcome","summary",...}` or auto-complete when artifacts validate
4. `complete_task` → FSM transition

**State guards** (reject before running CMD):

| State | Rejects (examples) |
|-------|---------------------|
| `planning` | `gt bd add`, `git add/commit/push`, `mkdir`, `python3`, writing `backend/` |
| `implementation` | `gt bd *`, `git push`, `git add .`, agent junk paths |

**Artifact validation** before accepting `success`:

| State | Checks |
|-------|--------|
| `planning` | Successful `bd create`, no failed CMDs, `plan.md` ≥ 200 bytes |
| `design` | `architecture.md` present and non-empty |
| `implementation` | Bead closed / backend artifacts per prompt |

## Agent matching (`AgentMatchesTask`)

`Manager.FetchTask` scans active instances and calls `AgentMatchesTask(agentID, state.Role, inst.Variables)` (`prompts.go`):

```go
// Rig-scoped roles (architect, polecat, qa): require "{rig}/{role}" when vars["rig"] set.
// Town-level roles (mayor, planner): bare "{role}" matches.
// agent_id "any" always matches.
```

| `vars["rig"]` | State role | Agent ID | Matches? |
|---------------|------------|----------|------------|
| `testgt2` | architect | `testgt2/architect` | Yes |
| `testgt2` | architect | `architect` | No |
| `testgt2` | qa | `testgt2/qa` | Yes |
| `testgt2` | qa | `qa` | No |
| (any) | mayor | `mayor` | Yes |
| (any) | planner | `planner` | Yes |
| `testgt2` | polecat | `testgt2/polecat` | Yes |
| `testgt2` | polecat | `polecat` | No (legacy town `hq-polecat` only when zero rigs) |

**`gt-agent` agent_id:** from `GT_AGENT_ID` / session name (e.g. `testgt2/architect`).

**Topology (`gt up`):** with orchestrator running and ≥1 rig, rig-scoped architect/qa/polecat start under `{town}/{rig}/`; town `hq-architect` / `hq-qa` / `hq-polecat` are skipped. Per-rig pipeline polecat at `{town}/{rig}/polecat/` handles `implementation` for rig-flow (`agent_id` `{rig}/polecat`).

## `OrchestratedForRole` vs patrol

```go
func OrchestratedForRole(orchestratorRunning bool, role string) bool {
    if !orchestratorRunning || IsPatrolRole(role) { return false }
    return IsPipelineRole(role)
}
```

| Category | Roles | `--orchestrated` when orch running |
|----------|-------|-------------------------------------|
| Pipeline | mayor, architect, planner, polecat, qa | Yes |
| Patrol | witness, refinery, mechanic, deacon | No (legacy patrol) |

Rig pipeline polecat uses `OrchestratedForRole` like architect/qa. Legacy town `hq-polecat` uses `OrchestratedForTownPolecat` only when no rigs are registered.

## `LegacyPolecatsPaused`

While an active `rig-flow` instance exists for a rig, `gt up --restore` skips
`startPolecatsWithWork` for per-bead polecats under `{rig}/polecats/`. Witness sling may
still spawn legacy polecats outside the FSM.

## MCP tools (NATS, not IDE MCP)

Transport: NATS request/reply on subject **`gt.orchestrator.mcp`**.  
This is **not** registered in Cursor/Claude `mcpServers`; only `gt-agent` and CLI use it.

| Tool | Arguments | Returns |
|------|-----------|---------|
| `fetch_task` | `agent_id` | `workflow_id`, `template_id`, `state`, `role`, `system_prompt`, `instructions`, `allowed_outcomes` |
| `complete_task` | `workflow_id`, `outcome`, optional `summary` | `next_state`, `status` |
| `start_workflow` | `template_id`, `variables` (e.g. `rig`) | `workflow_id` |
| `get_workflow_status` | optional `workflow_id` | instance list or single instance JSON |

JSON-RPC methods: `initialize`, `list_tools`, `call_tool`.

### Outcome → transition

`WorkflowInstance.Transition` looks up `state.Transitions[outcome]`. Fallback order:

1. Exact outcome key
2. `success`
3. `default`
4. Stay in current state

**QA example** (`rig-flow.yaml`):

```yaml
qa_review:
  transitions:
    task_passed:
      to: implementation
    all_passed:
      to: completed
    failure:
      to: implementation
```

Agents should emit transition keys from `allowed_outcomes` (e.g. QA: `task_passed`,
`all_passed`, `failure`). `success` / `fail` are accepted as fallbacks where mapped.

## Integration points

### `gt up`

- Starts orchestrator subprocess (`orchestrator.Start`)
- `OrchestratedForRole` → `--orchestrated` only on pipeline roles (not witness/refinery/mechanic/deacon)
- Rig-scoped architect/qa/polecat when rigs exist; legacy `hq-polecat` only with zero rigs
- `MaybeAutoStartWorkflow` when `settings.config.orchestrator.auto_start`
- `RepairIdentityFiles` for polecat worktrees; `LegacyPolecatsPaused` skips legacy polecat restore

### `gt down`

- Stops orchestrator with other daemons

### Session transport

Independent of orchestrator MCP:

- Town `session_transport: nats` → `gt nats-wrapper` for agent sessions
- Orchestrator uses same broker, different subject

### Template directories

| Location | Loaded when |
|----------|-------------|
| `{townRoot}/orchestrator/templates/` | `gt orchestrator run` (runtime) |
| `internal/orchestrator/town/` (gastown) | Source; embedded + synced via `make install` / `gt orchestrator sync` |
| `internal/orchestrator/templates/` | Legacy examples only; not embedded |

`make install` copies `town/templates` and `town/prompts` into `{townRoot}/orchestrator/`.

## File map

### Gastown source

| Path | Purpose |
|------|---------|
| `internal/orchestrator/types.go` | FSM types, `Transition`, `GetCurrentTask` |
| `internal/orchestrator/manager.go` | Load templates, instances, fetch/complete |
| `internal/orchestrator/mcp.go` | MCP server |
| `internal/orchestrator/orchestrator.go` | NATS client, start/stop, PID |
| `internal/cmd/orchestrator.go` | CLI |
| `internal/cmd/mayor.go` | `workflow start` |
| `internal/cmd/up.go` | Orchestrator + orchestrated agents |
| `cmd/gt-agent/main.go` | `runOrchestrated`, `buildSystemPrompt` |
| `internal/session/lifecycle.go` | `--orchestrated` on command line |
| `internal/templates/roles/*.md.tmpl` | Legacy role prompts |
| `internal/orchestrator/town/templates/rig-flow.yaml` | Canonical rig-flow (embedded) |
| `internal/orchestrator/town/prompts/rig-flow/*.md` | Per-state system prompts |
| `cmd/gt-agent/orchestrated.go` | Orchestrated loop + guards |
| `internal/orchestrator/provision.go` | `//go:embed town/` |

### Town runtime (example)

| Path | Purpose |
|------|---------|
| `orchestrator/templates/rig-flow.yaml` | Rig pipeline FSM |
| `orchestrator/prompts/rig-flow/*.md` | Per-state prompts |
| `orchestrator/instances.json` | Persisted workflow instances |
| `daemon/orchestrator.pid` | Running indicator |
| `logs/orchestrator.log` | MCP + manager debug |
| `settings/config.json` | `orchestrator.*`, `session_transport`, `role_agents` |

## Known gaps and bugs

1. **QA outcomes** — FSM keys `task_passed` / `all_passed` vs agent habit of `success` / `fail`
2. **Legacy sling polecats** — can still run parallel to hq-polecat; identity repair helps but does not remove witness path
3. **Bundled non-rig-flow YAML** — some files under `internal/orchestrator/templates/` use old schema
4. **Completed instances** — may still be scanned until status filter tightened
5. **Multi-rig towns** — hq vs rig agent duplication returns when multiple rigs registered
6. **`cmd/gt-orchestrator/main.go`** — stale; use `gt orchestrator run`
7. **Beads/Dolt backing** — instances are JSON file only (not beads yet)

## Roadmap (implementation phases)

### Phase 1 — agent + prompts (done)

Per-state `prompt_file`, orchestrated poll loop, JSON outcomes, idle without LLM,
pipeline roles skip `gatherWork`, `template_id` in `fetch_task`.

### Phase 2 — operations (done)

`orchestrator/instances.json` persistence, `get_workflow_status`, `orchestrator.auto_start`
/ `default_workflow`, `gt mayor workflow status|complete`, `gt orchestrator sync`.

### Phase 3 — topology + guards (done)

`AgentMatchesTask` rig affinity, `OrchestratedForRole`, hq-polecat, `LegacyPolecatsPaused`,
polecat identity repair, planning/implementation CMD guards, rig-flow prompt pack in
`internal/orchestrator/town/`.

### Future

| Change | Notes |
|--------|-------|
| Persist instances in beads/Dolt | Replace or mirror JSON file |
| Unify all bundled template YAML | Single schema with loader validation |
| Stop legacy sling polecats during rig-flow | Beyond restore skip |
| Full QA outcome aliases + prompt tables | Match FSM keys exactly |
| Hybrid molecules ↔ orchestrator | Optional; formulas remain separate today |

## Testing

### Manual (testgt2, from town root)

```bash
cd ~/gt
gt up
gt orchestrator status
gt mayor workflow start rig-flow --rig testgt2
tail -f logs/orchestrator.log
# Expect: kickoff → design → planning → implementation → qa_review → completed
```

Verify:

- `testgt2/architect` only active in `design`
- `~/gt/polecat` only in `implementation` (`testgt2/polecat`)
- `testgt2/qa` only in `qa_review` (not `~/gt/qa`)

### Automated

```bash
go test ./internal/orchestrator/...
```

Covers `AgentMatchesTask`, `OrchestratedForRole`, `LegacyPolecatsPaused`, FSM transitions,
`CompleteTask`, `FetchTask`, `GetWorkflowStatus`, template validation.

## Related documentation

- [Orchestrator (concept)](../concepts/orchestrator.md)
- [Architecture](architecture.md) — beads, roles, work orchestration pipeline diagram
- [Molecules](../concepts/molecules.md)
- [Agent provider integration](../agent-provider-integration.md)

## Legacy parallel: molecules and formulas

`.beads/formulas/*.formula.toml` (e.g. `mol-idea-to-plan`) remain a separate coordination
system. They are not wired to the MCP orchestrator. New rig pipelines should prefer
orchestrator YAML once parity is reached; patrol and convoy work stay on molecules/mail.
