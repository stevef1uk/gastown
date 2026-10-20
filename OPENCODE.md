# OpenCode Instructions

This file provides instructions for OpenCode when working in the gastown repository.

## Core Principles

- **Surgical and strategic fixes only** — Make minimal, targeted changes that address the root cause. Avoid broad refactors or shotgun fixes.
- **Fix in gastown, not gt** — The gastown repository (`/home/stevef/dev/freeride/gastown`) is the source of truth. The `gt` binary and town directory (`~/gt`) are runtime artifacts. Fix the source in gastown so changes propagate correctly through `make install`.
- **No project-specific instructions** — The code must handle any project generically. Never hardcode paths like `pingapp/`, `linkshelf/`, `finally/`, etc. Use `v.LayoutRoot`, `v.LayoutRootDir()`, and dynamic path resolution from the workflow profile.

## Development Workflow

1. **Run tests before committing** — Ensure all tests pass: `go test ./internal/orchestrator ./cmd/gt-agent`
2. **Build and install** — `make install` to update the gt binary
3. **Test end-to-end** — Run the sample test script: `./scripts/run_simple_go_test.sh`
4. **Commit and push** — Follow the session close protocol in AGENTS.md

## Key Constraints

- **No hardcoded project paths** — Never use `pingapp/`, `linkshelf/`, `finally/`, `tasklist/`, etc. in source code. Use `v.LayoutRoot`, `v.LayoutRootDir()`, `v.ForActivePhase().RequiredFiles`, etc.
- **Dynamic path resolution** — All file operations must resolve paths through the workflow profile's `required_files`, `delivery_phases`, and `layout_root`.
- **Prompt context injection** — Use the YAML/prompt system (`internal/orchestrator/town/templates/rig-flow.yaml`, `internal/orchestrator/town/prompts/rig-flow/*.md`) for agent behavior, not Go conditionals on state names.
- **Configuration over code** — Prefer YAML hooks and prompt context blocks over Go `if task.State == "..."` branches.

## Testing

- Run `go test ./internal/orchestrator ./cmd/gt-agent` before pushing
- Run `./scripts/run_simple_go_test.sh` for end-to-end validation
- All unit tests must pass before pushing

## Code Style

- No comments unless explicitly requested
- Follow existing patterns in the codebase
- Keep functions focused and single-purpose
- Use existing libraries and utilities from the codebase

## Key Files

- `internal/orchestrator/town/templates/rig-flow.yaml` — FSM definition, state hooks
- `internal/orchestrator/town/prompts/rig-flow/*.md` — Agent prompts
- `internal/orchestrator/prompt_context.go` — Dynamic prompt context blocks
- `internal/orchestrator/validation.go` — Profile validation and normalization
- `cmd/gt-agent/orchestrated.go` — Agent execution loop
- `cmd/gt-agent/orchestrated_validation.go` — Command validation logic