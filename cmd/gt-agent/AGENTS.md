# gt-agent — agent instructions

## Config first; do not branch on FSM state names

Orchestrated rig-flow behavior is defined in **`internal/orchestrator/town/templates/rig-flow.yaml`** and **`prompts/rig-flow/*.md`**, not with Go like `if task.State == "implementation"`.

Before editing this package for prompt framing, bead queues, or retry text:

1. Read `internal/orchestrator/prompt_context.go` (maintainer note).
2. Read `internal/orchestrator/town/README.md` (§ FSM behavior belongs in YAML).
3. Prefer new YAML hook fields on `StateHooks` only when many states need the same mechanism.

Per-rig values: `{rig}/mayor/rig/.gastown/workflow-profile.json`.
