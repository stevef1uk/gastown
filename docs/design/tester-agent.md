# Tester Agent — design

**Status:** Proposed (branch `tester`)
**Objective:** Hands-free software builds that work first time.
**Applies to:** both orchestrator workflows — **rig-flow** (`internal/orchestrator/town/templates/rig-flow.yaml`) and **req-flow** (`req-flow.yaml`).

## 1. The problem

Today the pipeline guarantees *"the code compiles and the tests pass"* but not
*"the right tests exist in the right places, at the right depth, for every
requirement"*. Observed failure modes (see [verify-and-smoke-gaps.md](./verify-and-smoke-gaps.md)):

- Tests pass but never exercise a requirement (SPEC/REQUIREMENTS coverage gap).
- Tests exist but are trivial (`assert True`, import-only, empty bodies).
- Tests use `os.Chdir` "fiction" instead of the real `web/` layout, so the
  production server still 404s.
- UI behaviour is never exercised (no Playwright specs) even though SPEC
  defines a web surface.
- QA catches defects late, after the polecat already `bd close`d a bead —
  expensive rework loops.

The **Tester** closes the loop *upstream*: it plans the tests (so polecats write
the right ones TDD-style) and audits them *after* implementation (so nothing
untested ships), mapping every requirement from **REQUIREMENTS.md** (optional),
**SPEC.md**, and **architecture.md** to a concrete unit / integration / UI test.

## 2. Role definition

| Attribute | Value |
|-----------|-------|
| **Role id** | `tester` |
| **Scope** | Rig-scoped (`{rig}/tester`), like `qa` |
| **Pipeline class** | `IsPipelineRole("tester") = true` → `gt-agent --orchestrated` |
| **Persistence** | Persistent identity bead `<prefix>-<rig>-tester` (rig-level) |
| **Write authority** | `TEST_PLAN.md`, `test-report.md` only. Read-only everywhere else (same class of guard as QA, see §11) |
| **Model** | `models.json` → `"tester"` key (default: judge/QA tier, see §11) |
| **Never** | Writes/edits source or test files under `{{layout_root}}/`, closes beads, runs `bd create` |

### Division of labour (keeps every gate single-purpose)

| Agent | Owns |
|-------|------|
| **Analyst** (req-flow) | REQUIREMENTS.md → SPEC.md |
| **Architect** | SPEC.md → architecture.md |
| **Planner** | plan.md bead map + acceptance bullets (one bead per `required_files` path, incl. `*_test.go` / `tests/test_*.py`) |
| **Tester (new)** | TEST_PLAN.md (requirement→test matrix, per phase) + test-report.md (adequacy verdict) |
| **Polecat** | Implements code **and** the tests named in TEST_PLAN.md; `bd close` when verify passes |
| **QA** | Existing two-stage review (spec compliance + code quality) + runtime smoke; reads test-report.md as the source of truth for Stage-1 "tests cover acceptance" |

## 3. Where the Tester sits in the FSM

Two new states per workflow, both **per delivery phase** (they re-run exactly like
`planning` / `qa_review` do today when the orchestrator advances phases).

```
rig-flow / req-flow (per active delivery phase)
===============================================================
          ┌──────────────────┐
  ───────▶│  planning        │  planner: plan.md + beads
          └────────┬─────────┘
                   ▼
          ┌──────────────────┐
          │  plan_review     │  QA: bead set / triad coherence
          └────────┬─────────┘
                   ▼
  ██████████████████████████████████████████████████████████████
  █  NEW  ┌──────────────────┐
  █       │  test_plan       │  tester: TEST_PLAN.md requirement→test matrix
  █       └────────┬─────────┘
  █                ▼
  █       ┌──────────────────┐
  █       │  project_setup   │  setup: stack/manifests green
  █       └────────┬─────────┘
  █                ▼
  █       ┌──────────────────┐
  █       │  implementation  │  polecat: code + tests from TEST_PLAN.md
  █       └────────┬─────────┘
  █                ▼
  █  NEW  ┌──────────────────┐
  █       │  test_review     │  tester: run tests + audit vs matrix → test-report.md
  █       └────────┬─────────┘
  █                ▼
  ██████████████████████████████████████████████████████████████
          ┌──────────────────┐
          │  qa_review       │  QA: spec compliance + runtime smoke + quality
          └────────┬─────────┘
                   ▼
          advance_phase  ──▶ planning (next phase)  |  completed
```

In **req-flow** the same two states appear at the same two places. The only
difference is that the Tester also consumes `REQUIREMENTS.md` (produced by the
analyst in `analysis`) as the top of the traceability chain. In **rig-flow**
REQUIREMENTS.md is optional — if absent the Tester derives requirements from
SPEC.md alone and records that fact in TEST_PLAN.md. A single prompt template
serves both flows; a `{{has_requirements}}` variable toggles the extra
REQUIREMENTS.md pass.

### Why two states and why these positions

- **test_plan after plan_review** — the plan is already coherent (bead map, full
  paths, acceptance bullets). The Tester audits it *before* anyone builds, so a
  missing/untestable acceptance criterion costs a cheap planning rework instead
  of an expensive implementation rework. This is the "test-first" half.
- **test_review after implementation, before QA** — tests must demonstrably
  exist and pass *before* QA spends its turns on runtime smoke / code quality.
  QA then trusts `test-report.md` instead of re-deriving test adequacy. This is
  the "audit" half.

## 4. Artifacts

### 4.1 `TEST_PLAN.md` — `{rig}/mayor/rig/TEST_PLAN.md` (per phase)

The Tester's primary output at `test_plan`. Phase-scoped. Markdown with a strict
machine-checkable shape (mirrors `plan.md` conventions):

```markdown
# Test plan — <phase-id> (<phase-title>)

Source: REQUIREMENTS.md (present/absent), SPEC.md, architecture.md, plan.md
Requirements source: REQUIREMENTS.md#FR-1.3 / SPEC.md §Data model / ...

## Requirement → test matrix

### FR-1.3 — "users can place limit orders"  [req: REQUIREMENTS.md §2.4]
- Level: integration            (unit | integration | ui)
- Phase: api-handlers
- Test file: linkshelf/internal/api/handlers_test.go
- Bead: ap-123 (Implement linkshelf/internal/api/handlers_test.go per architecture)
- Scenarios:
  - POST /api/orders with valid limit order → 201, body echoed
  - POST /api/orders with price < 0 → 400
  - GET /api/orders?status=open → only open orders
- Assertions: status code, response JSON fields, store state
- Acceptance mapping: plan.md#ap-321 bullet 2

### UI-1 — "order form submits to /api/orders"  [req: SPEC.md §Frontend]
- Level: ui
- Phase: web-shell
- Test file: linkshelf/e2e/trading_flow.spec.ts
- Bead: ap-999 (Implement linkshelf/e2e/trading_flow.spec.ts per architecture)
- Scenarios: open page, fill form, submit, assert success toast + table row
- Run: cd linkshelf && npx playwright test e2e/trading_flow.spec.ts

## Phase verify command
qa_verify_command: cd linkshelf && go test -count=1 ./internal/api/...

## Gaps (must be empty on success)
- (none)
```

Rules the deterministic gate enforces (see §7):

1. One `### <req-id>` block per requirement in scope for the active phase.
2. Every `Level:` is one of `unit | integration | ui`.
3. Every `Test file:` is a path in the active phase's `required_files`
   (test paths are already phase-paired by `pairPhaseTests`).
4. Every `Test file:` has a real open/in-progress implement bead (title contains
   `{{bead_title_contains}}` and the exact path) — the Tester **names**, never
   creates, beads; missing beads are reported back to planning.
5. `Scenarios:` non-empty; each scenario is concrete and ends with an
   observable `Assertions:` line.
6. Every SPEC/REQUIREMENTS requirement reachable from the phase's source files
   appears in at least one row (traceability check, §7).

### 4.2 `test-report.md` — `{rig}/mayor/rig/test-report.md` (per phase)

The Tester's output at `test_review`. Rows in, verdicts out:

```markdown
# Test adequacy report — <phase-id>

## Execution
- command: cd linkshelf && go test -count=1 ./internal/api/...  → exit 0 (12 passed)
- ui:      npx playwright test e2e/trading_flow.spec.ts           → exit 0 (3 passed)

## Requirement → verdict
| req | level | test file | present | non-trivial | passes | verdict |
|-----|-------|-----------|---------|-------------|--------|---------|
| FR-1.3 | integration | linkshelf/internal/api/handlers_test.go | yes | yes | yes | COVERED |
| UI-1 | ui | linkshelf/e2e/trading_flow.spec.ts | yes | yes | yes | COVERED |

## Gaps (must be empty for all_passed)
- (none)
```

## 5. Requirement traceability — the core loop

The Tester is only as good as its map from *requirement* → *test*. Chain:

```
REQUIREMENTS.md ──(req-flow analyst; optional in rig-flow)──▶ SPEC.md
SPEC.md ──(architect)───────────────────────────────────────▶ architecture.md
architecture.md + SPEC ──(planner)──────────────────────────▶ plan.md (+ beads)
SPEC + architecture + plan (+ REQUIREMENTS.md) ──(tester)──▶ TEST_PLAN.md
TEST_PLAN.md ──(polecat)────────────────────────────────────▶ code + tests
TEST_PLAN.md + tests ──(tester)─────────────────────────────▶ test-report.md
test-report.md ──(QA)───────────────────────────────────────▶ all_passed
```

Requirements are addressed by **ID** so the chain is greppable end-to-end:

- req-flow: REQUIREMENTS.md requirement IDs (e.g. `REQ-3`, `FR-1.3`) are carried
  into SPEC.md (`[req: REQUIREMENTS.md §2.4]`) by the analyst — this already has
  a partial home in `spec_review`'s "nothing dropped" checklist.
- rig-flow without REQUIREMENTS.md: requirement IDs default to SPEC.md headings
  (`SPEC §HTTP table`, `§Data model`, `§Frontend`) or SPEC acceptance bullets.

`test_review` walks the matrix and fails any row where the test file is missing,
trivial, or failing. QA's Stage-1 test-sufficiency check becomes "read
test-report.md; all rows COVERED" instead of re-deriving coverage.

## 6. Adequacy rules — unit / integration / UI per phase

The Tester assigns each requirement a **level** by phase and stack, never by
whim:

| Level | What it covers | When it applies | Tooling (stack-derived) |
|-------|----------------|-----------------|--------------------------|
| **unit** | Pure logic, functions, types, parsing, validation, store helpers | Any code bead | `go test ./pkg/...`, `pytest tests/...`, `jest`, vitest |
| **integration** | HTTP routes, store↔API wiring, server entrypoint, schema | A phase with `cmd/.../main.go`, handlers, store, DB | Go `httptest` + `go run`+curl; pytest + test client; supertest |
| **ui** | Web surfaces, user flows, static assets served | A phase with `web/`, `frontend/`, HTML+JS | Playwright (`npx playwright test`), driven by `ensure_playwright_config_ready` |

Rules:

1. **Unit tests are mandatory for every production source file** in the phase's
   `required_files` that contains logic (excluded: manifests, config, static
   assets, HTML — matching existing exempt-file handling).
2. **Integration tests are mandatory** for every phase whose `required_files`
   include a server entrypoint, handlers, or store (same trigger the pipeline
   already uses for smoke / integration contract).
3. **UI tests are mandatory** when the phase ships a web surface (SPEC/architecture
   declare `web/` + HTTP static routes) — maps to existing `e2e`/Playwright
   scaffolding.
4. A single test may cover multiple requirements, but every requirement must be
   **reachable** from at least one row (rule 6 of §7).
5. **Depth:** each scenario must be falsifiable — a concrete input + an
   `Assertions:` line. Trivial/stub tests are rejected (deterministic +
   `ValidateTestQualityWithJudge`, §7).

## 7. Success criteria for `test_plan` / `test_review`

Following the pipeline's existing "deterministic first, LLM judge second"
pattern (`llm_judge.go`, `CheckContentNotStub`):

### Deterministic (no LLM — always on)

**test_plan**
1. `TEST_PLAN.md` exists and `wc -c ≥ min_test_plan_bytes` (profile default 400).
2. ≥1 `### <req-id>` block; every `Level:` ∈ {unit, integration, ui}.
3. Every `Test file:` ∈ active-phase `required_files` **and** has an
   open/in-progress bead whose title contains `{{bead_title_contains}}` + the
   exact path.
4. Traceability: every requirement reachable from the phase's source files (SPEC
   HTTP table rows, acceptance bullets for the phase's paths, data-model
   requirements) maps to ≥1 row. A deterministic pass uses a lightweight
   requirement-extractor (§10) seeded from the profile/spec sections; the LLM
   judge catches semantic drift.
5. Every unit-testable production file in the phase has a unit row (rule 1 §6).

**test_review**
6. Every matrix `Test file:` exists on disk.
7. `{{unittest_command_hint}}` runs green for the phase (this is also the phase
   `qa_verify_command`).
8. UI rows: `npx playwright test <file>` (or profile `ui_test_command`) green
   when UI rows exist and Playwright config is present.
9. `ValidateTestQualityWithJudge` per test file: non-trivial names, real
   assertions, SPEC/architecture coverage, realistic data (existing judge, §11).
10. Every matrix row is `COVERED` → `test-report.md` written with `## Gaps: (none)`.

### LLM judge (soft gate, mirrors existing judge fallback)

11. `ValidateTestAdequacyWithJudge(TEST_PLAN.md, test-report.md, SPEC,
    architecture)` — matrix-wide adequacy: "could a defect in any covered
    requirement survive all planned tests?" Judge unreachable ⇒ skip (existing
    connection-refused behaviour).

## 8. FSM wiring (YAML)

Add to **both** `rig-flow.yaml` and `req-flow.yaml`. Shared prompt files under
`internal/orchestrator/town/prompts/rig-flow/` (req-flow reuses them, like it
already reuses `design.md` / `planning.md` / `qa_review.md`).

### State `test_plan` (insert between `plan_review` and `project_setup`)

```yaml
  test_plan:
    role: tester
    prompt_file: prompts/rig-flow/test_plan.md
    instructions: |
      1. Read REQUIREMENTS.md ({{has_requirements}}), SPEC.md, architecture.md, and plan.md under {{rig}}/mayor/rig.
      2. For the active phase {{active_phase_id}} write TEST_PLAN.md: a requirement→test matrix
         mapping every in-scope requirement to a unit/integration/ui test file (paths must be in
         this phase's required_files: {{required_files}}).
      3. Every Test file row must have a real implement bead (bd list). Do NOT bd create — report
         missing beads in the Gaps section and return failure.
      4. Report success only when TEST_PLAN.md satisfies the matrix rules; failure with exact gaps otherwise.
    hooks:
      pre_run: [sync_planning_artifacts]
      state_timeout_seconds: 1800
      max_cmd_turns: 12
      cmd_guard: test_plan
      cmd_rewrites: [rig_placeholders, spec_md_case, plan_md_after_cd, bd_list_limit, unwrap_bash_lc]
      env:
        beads_dir: true
      track: test_plan
      artifacts: test_plan
      retry_hint: |
        One CMD: per line. Read SPEC/architecture/plan from {{rig}}/mayor/rig, bd list --status=open
        for real bead IDs, write TEST_PLAN.md via heredoc, verify `wc -c TEST_PLAN.md` (≥ {{min_test_plan_bytes}}).
      failure_hint: |
        Do not edit files under {{layout_root}}/. If plan.md acceptance bullets are untestable or
        test-file beads are missing, report failure with the exact requirements/paths so planning
        can fix plan.md or add beads.
    transitions:
      success:
        to: project_setup
      failure:
        to: planning
      architecture_failure:
        to: design
```

### State `test_review` (insert between `implementation` and `qa_review`)

```yaml
  test_review:
    role: tester
    prompt_file: prompts/rig-flow/test_review.md
    instructions: |
      1. Run the phase verify ({{unittest_command_hint}}) and any UI rows from TEST_PLAN.md
         ({{ui_test_command}}).
      2. Compare every TEST_PLAN.md row against the on-disk test files; reject missing/trivial/stub tests.
      3. Write test-report.md with a per-requirement verdict. Return all_passed when every row is
         COVERED; failure with exact files otherwise.
    hooks:
      state_timeout_seconds: 1800
      cmd_timeout_seconds: 300
      max_cmd_turns: 12
      pre_run: [ensure_dolt_auto_commit, ensure_dolt_schema_health, reconcile_implement_beads]
      cmd_guard: test_review
      cmd_rewrites: [rig_placeholders, unwrap_bash_lc, bd_list_limit, unittest_workdir, bd_strip_beads_dir]
      env:
        pythonpath: true
        python_venv: activate
      track: test_review
      artifacts: test_review
      retry_hint: |
        One CMD: per line. Run {{unittest_command_hint}} from {{rig}}/mayor/rig, then head each
        TEST_PLAN.md test file to confirm non-trivial assertions, then write test-report.md.
      failure_hint: |
        Run real CMD: lines (tests, ls of test files). Do NOT edit source/test files. Name the exact
        missing/trivial test files and the requirements they leave uncovered.
    transitions:
      all_passed:
        to: qa_review
      task_passed:
        to: implementation
      failure:
        to: implementation
      plan_gap:
        to: test_plan
      architecture_failure:
        to: design
      timeout:
        to: test_review
```

`task_passed` (beads still open) is listed for parity with QA's outcome set even
though test_review runs after implementation; the FSM's fallback order
(`success` → `default`) keeps `all_passed` the primary route.

### Outcome → owner (rework paths)

| Outcome | Next state | Owner | Why |
|---------|-----------|-------|-----|
| test_plan `success` | `project_setup` | — | plan is test-ready |
| test_plan `failure` | `planning` | Planner | untestable acceptance bullets / missing test beads |
| test_plan `architecture_failure` | `design` | Architect | requirement untestable at this level → design fix |
| test_review `all_passed` | `qa_review` | QA | tests adequate; QA does smoke + quality |
| test_review `failure` / `task_passed` | `implementation` | Polecat | missing/trivial/failing test → reopen bead(s) |
| test_review `plan_gap` | `test_plan` | Tester | matrix itself wrong → re-plan tests |
| test_review `architecture_failure` | `design` | Architect | runtime/level mismatch → design fix |

### 8.1 How the Polecat fixes failing tests

`test_review` `failure` → `implementation` hands the polecat back to fix tests.
The Tester is **read-only** (never mutates source/tests), so the polecat is the
only agent that can fix — and it already owns both the code and its tests. It
gets the fixes through the **same rework machinery QA uses today**, extended for
the new state:

1. **Structured failure summary.** `test_review`'s failure summary follows the
   QA-rework convention so the next polecat `fetch_task` prompt ("Prior step
   failed") is actionable. A new branch in `PrepareWorkflowReworkFeedback`
   (`rework_feedback.go`) formats:
   - the exact failing command + output (`cd {{rig}}/mayor/rig && {{unittest_command_hint}}`, UI rows),
   - the failing test names / assertion diffs,
   - the **bead IDs** to reopen (test file + its source file), from
     `ExtractKnownRigBeadIDsFromSummary` (same parser QA summaries use),
   - the requirement IDs left uncovered (rows from `TEST_PLAN.md`).
   Two failure classes, clearly labelled:
   - **red test** (verify fails) → reopen source bead + test bead; polecat fixes code or assertion.
   - **inadequate/missing test** (verify passes but trivial/absent) → reopen the test bead only; polecat strengthens it per the matrix row.

2. **Bead reopen + protection.** `ReopenImplementationBeadsAfterTestFailure`
   mirrors `ReopenImplementationBeadsAfterQAFailure` (`beads_rework.go`): it
   reopens the named beads so the polecat doesn't need a manual `bd update`.
   `QAReopenedBeadIDs` is generalized to also match `PendingRework.FromState ==
   "test_review"` so reconcile does **not** auto-close them mid-rework (the
   `implement_bead_context` injector shows the SPEC contract + matrix row).

3. **Polecat rework guidance.** `implementation.md`'s `failure_hint` (and the
   `system_prompt_footer`) gains a `test_review` branch:
   > After **test_review** failure rework: READ the failing test and its
   > `TEST_PLAN.md` row first. Run the phase verify `{{unittest_command_hint}}`
   > and fix from its actual output (code or assertion). Do **NOT** delete,
   > weaken, or skip the test to make it pass — the Tester audits assertions, and
   > a weakened test will fail `test_review` again. If the requirement is truly
   > untestable, return the outcome that routes to `test_plan`/`design` instead.

4. **Loop safety.** A single fix attempt is capped by the existing
   `implementation` state_timeout (7200s) + workflow stuck monitor. In addition,
   the orchestrator tracks consecutive `test_review → implementation` failures on
   the **same matrix row** (persisted in the instance `PendingRework`); after
   `tester.max_review_retries` (default 3) it forces outcome `plan_gap` (→
   `test_plan`, matrix wrong) or `architecture_failure` (→ `design`, level
   impossible) instead of spinning. This converts a "test can't be fixed" loop
   into a design/plan fix — the hands-free exit.

## 9. Prompt files

- `internal/orchestrator/town/prompts/rig-flow/test_plan.md`
- `internal/orchestrator/town/prompts/rig-flow/test_review.md`

Both are structured like the existing `planning.md` / `qa_review.md` prompts
(directory map, hard rules, allowed/forbidden CMD table, outcome table, anti-
hallucination section). New prompt vars required (added in
`prompt_context.go` / `prompts.go`):

| Var | Source |
|-----|--------|
| `{{has_requirements}}` | whether `mayor/rig/REQUIREMENTS.md` exists |
| `{{min_test_plan_bytes}}` | `WorkflowValidation.MinTestPlanBytes` |
| `{{ui_test_command}}` | profile `ui_test_command` or Playwright default |
| `{{phase_required_files}}` | active-phase `required_files` (already `{{required_files}}` in prompts) |

## 10. Deterministic helpers (Go)

New functions, following `delivery_phase.go` / `qa_smoke_spec.go` style:

- `ParseTestPlan(rigDir string, v WorkflowValidation) (TestPlan, error)` — parse
  `TEST_PLAN.md` `### <req-id>` blocks into `[]TestPlanRow{ReqID, Level,
  TestFile, BeadID, Scenarios}`.
- `ExtractPhaseRequirements(rigDir string, v WorkflowValidation) []string` —
  requirement IDs in scope for the active phase from SPEC.md (HTTP table,
  acceptance bullets, data-model headings) + architecture.md + plan.md.
- `TestPlanCoversRequirements(plan TestPlan, reqs []string) []string` — missing
  requirement IDs (rule 4 §7).
- `TestPlanBeadsExist(plan TestPlan, townRoot, rig string) []string` — rows whose
  `Test file:` has no open/in-progress implement bead.
- `TestFilesPresent(plan TestPlan, rigDir string) []string` — missing files.
- `CheckTestAdequacy(rigDir string, v WorkflowValidation) TestAdequacy` —
  non-triviality (byte length, placeholder patterns, `assert True`, empty body)
  reusing `CheckContentNotStub`; per-file verdicts.
- `ValidateTestAdequacyWithJudge(ctx, client, cfg)` — new judge in
  `llm_judge.go` (matrix-wide, §7.11).
- `WriteTestReport(rigDir string, rows []TestReportRow) error` — writes
  `test-report.md` (never overwrites a passing report with a partial one).

## 11. Code changes (implementation list)

| File | Change |
|------|--------|
| `internal/constants/constants.go` | `RoleTester = "tester"` + emoji |
| `internal/orchestrator/prompts.go` | add `"tester"` to `IsPipelineRole`; `rigScopedPipelineRoles["tester"] = true` |
| `internal/orchestrator/town/templates/rig-flow.yaml`, `req-flow.yaml` | add `test_plan` + `test_review` states (§8) |
| `internal/orchestrator/town/prompts/rig-flow/test_plan.md`, `test_review.md` | new prompts (§9) |
| `internal/templates/roles/tester.md.tmpl` | legacy/prime role template (mirrors `qa.md.tmpl`) |
| `models.json` | `"tester": "google/gemini-3.5-flash"` (judge/QA tier) |
| `cmd/gt-agent/state_runner_registry.go` | `cmd_guard` presets `test_plan`/`test_review` (tester scope: writes only TEST_PLAN.md/test-report.md), `track` + `artifacts` presets (`ArtifactTestPlanOK`, `ArtifactTestReviewOK`) |
| `cmd/gt-agent/orchestrated.go` | tester write-guard (like QA guard; §2), analyst-style file allowlist §11 note |
| `internal/orchestrator/validation.go`, `types.go` | `MinTestPlanBytes`, `UICommand` (profile + YAML validation fields); `delivery_phase` may carry `test_verify_command` |
| `internal/orchestrator/prompt_context.go`, `prompts.go` | new prompt vars §9 |
| `internal/orchestrator/llm_judge.go` | `ValidateTestAdequacyWithJudge` |
| `internal/orchestrator/beads_rework.go` | `ReopenImplementationBeadsAfterTestFailure` (mirrors `ReopenImplementationBeadsAfterQAFailure`); generalize `QAReopenedBeadIDs` to `test_review` (protect reopened beads from reconcile auto-close) |
| `internal/orchestrator/rework_feedback.go` | new `test_review → implementation` branch in `PrepareWorkflowReworkFeedback`: failing command+output, test names, bead IDs, uncovered requirement IDs (§8.1) |
| `internal/orchestrator/state_hooks.go` / `types.go` | `tester.max_review_retries` (default 3) — consecutive-failure cap routes to `plan_gap`/`architecture_failure` (§8.1 step 4) |
| `internal/orchestrator/*_test.go` | unit tests for §10 helpers (naming convention: `tester_agent_test.go`) |
| `internal/orchestrator/qa_write_guard.go` | block QA from writing TEST_PLAN.md/test-report.md; allow Tester writes |
| `internal/orchestrator/implement_read_guard.go` | allow polecat to read TEST_PLAN.md |
| `internal/cmd/up.go`, `down.go` | start/stop `{rig}/tester` session (`rigPipelineRoles` + architect/qa parity) |
| `internal/orchestrator/workflow_stuck*.go` | add `test_plan` / `test_review` to stuck-monitor state set |
| `internal/specprofile/` | (req-flow) ensure SPEC.md carries REQUIREMENTS.md IDs; expose `## Testing strategy` to the Tester |

**Write-guard summary** (class of guard = QA's in `qa_write_guard.go`):
- `tester` may write only `TEST_PLAN.md`, `test-report.md` (both at `mayor/rig/`).
- `tester` may read REQUIREMENTS.md, SPEC.md, architecture.md, plan.md,
  TEST_PLAN.md, source, and test files; run test commands.
- `tester` may **not** run `bd create` / `bd update` / `bd close`, `sed -i`,
  `> file` on source, or `EDIT:`/`WRITE:` on files under `{{layout_root}}/`.
- QA gains a new forbidden path: `TEST_PLAN.md` / `test-report.md` (Tester-owned).

## 12. Hands-free guarantee — what "works first time" means

The Tester makes the pipeline **fail forward with the cheapest rework possible**:

1. **Nothing ships untested** — every requirement in scope has a row, and every
   row is `COVERED` with a real, passing, non-trivial test (unit/integration/UI
   at the level the phase's stack requires).
2. **Defects surface where they are cheapest** — missing tests are caught at
   test_plan (planning rework) or test_review (implementation rework) *before*
   QA's runtime smoke, which is where today's incidents (linkshelf `/` 404,
   `os.Chdir` fiction) were only caught after `bd close`.
3. **QA is decisive** — QA no longer re-derives test sufficiency; it verifies
   runtime behaviour, spec compliance, and code quality on top of a guaranteed
   green, adequate test suite. `all_passed` becomes meaningful.
4. **Evidence is machine-readable** — `test-report.md` is the artifact Refinery
   and humans can trust at merge time.

## 13. Phased rollout (keep the pipeline green while shipping)

| Phase | Scope | Gate |
|-------|-------|------|
| **1. Deterministic test_plan only** | New state after `plan_review` in both flows; Tester writes TEST_PLAN.md; `ArtifactTestPlanOK` deterministic checks only | Existing `rig-flow` tests + new `tester_agent_test.go`; no LLM judge yet |
| **2. test_review** | New state after `implementation`; runs tests + `CheckTestAdequacy` + writes test-report.md; QA prompt reads report | `go test ./internal/orchestrator/...`; manual run on a fixture rig (testgt2-style) |
| **3. LLM adequacy judge** | `ValidateTestAdequacyWithJudge` wired into `ArtifactTestReviewOK` (soft-gate, connection-refused skip) | `llm_judge_integration_test.go` via freeride proxy |
| **4. req-flow parity + traceability IDs** | REQUIREMENTS.md → SPEC.md ID propagation in analyst; `{{has_requirements}}` prompt branch | `req-flow` manual run: `gt mayor workflow start req-flow --rig <rig>` |

Each phase ships independently; nothing is force-migrated. Operators can disable
the Tester per-rig via the workflow-profile (`tester.enabled: false`) without
code changes.

## 14. Testing the Tester itself

- `internal/orchestrator/tester_agent_test.go` — ParseTestPlan happy/malformed,
  level validation, traceability gaps, bead-existence, file-presence,
  non-triviality.
- `internal/orchestrator/tester_agent_fsm_test.go` — FSM transitions for
  `test_plan`/`test_review` in both templates (mirror `delivery_phase_flow_test.go`).
- `cmd/gt-agent/orchestrated_test.go` — tester write-guard (writes TEST_PLAN.md
  allowed; writes under `{{layout_root}}/` rejected; bd mutations rejected).
- `internal/orchestrator/llm_judge_integration_test.go` — adequacy judge.
- E2E: a fixture rig with a deliberately incomplete test suite must loop
  test_review→implementation until coverage is real, then pass QA.

## 15. Open questions

1. **UI test stack portability** — UI rows depend on Playwright/`ensure_playwright_config_ready`.
   Should test_plan skip UI rows (soft warning) when Playwright is unavailable,
   or hard-fail? Proposal: fail on `all_passed` only when the phase ships web and
   Playwright is configured; otherwise warn. (§6 rule 3)
2. **test_plan as its own LLM turn vs. folded into planning** — separate state
   costs one extra LLM turn per phase but keeps the Planner prompt small (which
   the codebase favours for weaker models). Proposal: separate state; revisit
   with a `tester.fold_into_planning` profile flag if latency matters.
3. **Coverage tooling** — should `test_review` run `go test -cover` / `pytest
   --cov` and fail below a floor (e.g. 70%)? Proposal: report only in
   test-report.md (informational) in v1; gate in v2 after fixture data exists.
4. **REQUIREMENTS.md in rig-flow** — treat as advisory (verify SPEC already
   satisfies it) or as authoritative (reject SPEC drift)? Proposal: advisory in
   v1, authoritative in v2 behind `tester.requirements_enforced`.
5. **Bead per test file** — currently planner already creates test-file beads
   (plan.md rule "include implement beads for unit test paths"). Confirm no
   planner change needed beyond guaranteeing test paths appear in `required_files`.

## 16. Related

- [Orchestrator (technical design)](./orchestrator.md) — FSM, states, outcome mapping
- [verify-and-smoke-gaps.md](./verify-and-smoke-gaps.md) — the incidents this prevents
- [architecture.md](../architecture.md) — agent taxonomy, work orchestration pipeline
