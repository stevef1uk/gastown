# Polecat Technology-Specific Coding Standards & Self-Learning

## Problem

1. **Cross-stack prompt noise:** Monolithic coding standards waste context window tokens and risk confusing the model when rules for unrelated stacks (e.g., Python `pytest`/`FastAPI` conventions in a Go project, or Go `httptest`/`go.mod` rules in a Python project) are injected together.
2. **Lost lessons across iterations:** `implementation-progress.json` is cleared whenever the workflow leaves `implementation` (such as transitioning to `test_review` or `qa_review`, or advancing delivery phases). Mistakes that caused compile errors, test failures, or QA rework on earlier beads are forgotten on subsequent beads and phases.

---

## Part 1: Technology-Specific Town Hall Coding Standards (`coding_standards`)

### Asset Locations

Embedded in `internal/orchestrator/town/standards/` and synced to `{townRoot}/orchestrator/standards/` by `SyncTownAssets`:

```
{townRoot}/orchestrator/standards/
  go.md          # Package layout, %w error wrapping, context.Context, RWMutex, table-driven httptest
  python.md      # Python 3.11+ typing, Pydantic v2, FastAPI static/route ordering, pytest fixtures
  typescript.md  # Vanilla browser JS vs bundled TS/React boundaries, DOM/API alignment, jsdom
  sql.md         # Idempotent DDL (IF NOT EXISTS), explicit constraints, parameterized queries
  docker.md      # Layer-cached builds, non-stub server runtime, Compose port/service alignment
```

Operators can customize `{townRoot}/orchestrator/standards/<stack>.md` on disk; `FormatCodingStandardsBlock` reads the town's on-disk file first and falls back to the embedded `townAssets` filesystem.

### Stack Detection & Strict Isolation

`DetectActiveCodingStacks(townRoot, rig, v)` in `internal/orchestrator/coding_standards.go` resolves canonical stack IDs (`go`, `python`, `typescript`, `sql`, `docker`) in two priority steps:

1. **Active implement bead path (narrowest scope):**
   - `.go`, `go.mod`, `go.sum` → `["go"]` (plus `"sql"` if the path is a store/db/schema file and the active phase includes `.sql` files)
   - `.py`, `requirements.txt`, `pyproject.toml` → `["python"]` (plus `"sql"` if store/db/schema with `.sql` in phase)
   - `.ts`, `.tsx`, `.js`, `.jsx`, `.html`, `.css`, `package.json`, `tsconfig.json` → `["typescript"]`
   - `.sql` → `["sql"]`
   - `Dockerfile`, `docker-compose.yml`, `compose.yml` → `["docker"]`
2. **Active delivery phase fallback (`v.ForActivePhase()`):**
   - Uses `WorkflowUsesGo`, `WorkflowUsesPython`, `WorkflowUsesNodeJS` / web file scan, `.sql` file scan, and `WorkflowUsesDocker`.
   - Go and Python are mutually exclusive at the phase level when one is primary, ensuring Go projects never receive Python standards and vice versa.

### Context Budget

- `maxCodingStandardsContextBytes = 900` bytes maximum across all active sections, truncated cleanly at a line boundary if needed.
- Registered in `templates/rig-flow.yaml` and `templates/req-flow.yaml` under `implementation.hooks.prompt_context`.

---

## Part 2: Concise Polecat Self-Learning (`polecat_learnings`)

### Storage Locations (Two-Tier Persistence)

```
{rig}/mayor/rig/.gastown/polecat-learnings.json       # Project-scoped (persists across phases & rework)
{townRoot}/orchestrator/learnings/<stack>.json        # Town-scoped by stack (go, python, typescript, sql, docker)
```

### Schema

```json
{
  "version": 1,
  "entries": [
    {
      "stack": "go",
      "rule": "Always initialize slices as []Item{} so JSON marshals [] instead of null",
      "source": "agent",
      "bead_path": "linkshelf/internal/store/store.go",
      "hits": 2,
      "updated_at": "2026-11-02T07:30:00Z"
    }
  ]
}
```

### How Learnings Are Captured

1. **Native `LEARN:` Directive (`source: "agent"`):**
   Polecat can emit `LEARN: <concise rule>` or `LEARN[<stack>]: <concise rule>` in any turn. `parseOrchestratedLearnings` in `cmd/gt-agent/orchestrated_native_edit.go` extracts and persists the rule, while `stripNativeToolBlocksForCmdParse` ensures `LEARN:` is never executed as a shell command.
2. **Terminal Outcome JSON (`source: "agent"`):**
   `orchestratedTaskResult` accepts optional `"learning": "..."` or `"learnings": ["..."]` fields in the completion JSON and records them in `recordTerminalTaskLearnings`.
3. **Automatic Verify-Fail → Green Recovery (`source: "verify_fix"`):**
   When an active bead fails verification (`LastVerifyFailOutput` is non-empty) and subsequently passes verification (`r.track.verifyOK`), `persistImplementationProgress` calls `ExtractVerifyFixLearning` to distil the compiler/test error line into a single-line rule.
4. **Automatic QA / Test Review Rework Resolution (`source: "qa_rework"`):**
   When `implementation` completes with `outcome: "success"` after a `PendingRework` from `qa_review` or `test_review`, `ExtractQAReworkLearning` records the first sentence of the rework summary.

### Context Bounds & Ring-Buffer Eviction

| Constant | Value | Purpose |
|----------|-------|---------|
| `MaxLearningRuleChars` | `140` | Maximum length of a single learning rule (single line) |
| `MaxProjectLearnings` | `8` | Maximum entries retained in `.gastown/polecat-learnings.json` |
| `MaxTownStackLearnings` | `8` | Maximum entries retained per stack in `orchestrator/learnings/<stack>.json` |
| `MaxLearningsPromptBytes` | `800` | Maximum bytes injected into the prompt by `FormatPolecatLearningsBlock` |

- **Deduplication & reinforcement:** Rules with identical normalized text increment `hits` and move to the most-recent slot instead of creating duplicate bullets.
- **Eviction:** When a file exceeds 8 entries, `upsertLearningFile` evicts the oldest entry with the lowest `hits` count, preserving reinforced lessons.
- **Stack filtering on injection:** `FormatPolecatLearningsBlock` only loads town-scoped learnings matching `DetectActiveCodingStacks(townRoot, rig, v)` and filters out mismatched stack entries from the project store.
