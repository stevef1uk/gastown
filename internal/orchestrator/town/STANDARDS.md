# STANDARDS.md — Rig Flavour Standards

This file defines the coding and workflow standards for Gas Town rigs. It is
embedded into every new gt town on install and read by agents at runtime.

## Module / Package Standards

- **One primary package per rig.** For Go rigs, the layout root (`{{layout_root}}`)
  should contain a single `package main` entry point (e.g. `cmd/server/main.go`).
  All other Go files should belong to test packages (`*_test.go`) or supporting
  packages under `internal/`. Never mix application and test logic in the same
  package.
- **No runtime logic in module declarations.** `go.mod` must declare only the
  module path and Go version directive (`go 1.22`). All configuration,
  dependencies, and build constraints belong in `go.mod`; application code
  must not import or depend on runtime state at module initialization.
- **Dependency hygiene.** Pin all indirect dependencies. Prefer `go get -u=patch`
  for security updates. Avoid transitive dependency drift — run `go vet ./...`
  and `go mod tidy` before each commit.
- **Interface-first design.** Define interfaces in `internal/` packages; implement
  in `cmd/.../main.go`. Prefer composition over inheritance. External APIs must
  be represented as interfaces, not concrete types embedded in main.

## Architecture Standards

- **Requirement IDs in architecture.md.** Every delivery phase must have at least
  one `### <req-id>` requirement heading in `architecture.md`, matching the
  active phase name (e.g. `### go-module`, `### core`). These IDs are validated
  by the Tester's anti-hallucination check — every `### <req-id>` in
  `TEST_PLAN.md` must have a corresponding `### <req-id>` in `architecture.md` or
  `SPEC.md`. Do not invent requirements; only reference those that explicitly
  appear in sources.
- **Delivery phases map.** The `## Delivery phases` section of `architecture.md`
  must list all phases with concrete `required_files` entries. No wildcard paths
  (e.g. `test_*.py`, `*_test.go`) are permitted — every file must be an exact
  literal path under `{{layout_root}}`.
- **HTTP route and store API verbatim matching.** Any HTTP route table or store
  API definition in `architecture.md` must match `SPEC.md` exactly (path, method,
  status, response contract). gt-agent rejects drift on this check.
- **File path prefix discipline.** All file paths anywhere in
  `architecture.md` must use the `{{layout_root}}/` prefix when
  `{{layout_root}}` ≠ `"."` or empty. When `layout_root` is `"."`, use bare paths
  (`main.go`, `handler.go`). No `./` prefix. Prose may reference packages as
  `store.List` without backticks. Only wrap actual file paths in backticks.

## Testing Standards

- **TDD iron law.** NO PRODUCTION CODE WITHOUT A FAILING TEST FIRST. Follow the
  red-green-refactor cycle for every bead that has a test path in `plan.md` (or
  a testable unit):
  1. **RED** — WRITE the test file FIRST (from plan.md acceptance bullets).
  2. **Verify RED** — run the test. It MUST fail for the right reason (feature
     missing, not a typo). If it passes immediately, rewrite the test.
  3. **GREEN** — WRITE the minimal implementation to make the test pass.
  4. **Verify GREEN** — run the test. It MUST pass. If it fails, fix the code,
     not the test.
  5. **REFACTOR** — clean up while the test stays green. Do not add behavior
     beyond the test.
- **Test file shape per level.**
  - **unit** — `*_test.go` or `tests/test_*.py` next to the code it tests.
  - **integration** — tests that cross packages/servers (httptest, pytest client,
    compose E2E) — a test package/dir listed in `required_files`.
  - **ui** — tests against the running UI via a browser or DOM harness — a
    `test/e2e`/playwright dir listed in `required_files`.
- **Do not inflate levels.** A unit test is not a UI test. Integration tests
  serve API/store/HTTP wiring; UI tests serve user-visible flows only when the
  phase ships UI files.

## Prompt & Workflow Standards

- **Read STANDARDS.md before starting work.** Every agent session should confirm
  the current standards version by running `cat {town_root}/orchestrator/STANDARDS.md`
  at the start of a new rig-flow.
- **Prompt files are authoritative.** Agent behavior is defined by
  `prompts/rig-flow/*.md` under the town root, not by Go conditionals. When in
  doubt, reconfigure YAML (`templates/rig-flow.yaml`) or add a hook, not Go.
- **One bead per turn.** Touch exactly one bead per "turn group" (message +
  verify/retry loop). A turn group ends when you send the next CMD/WRITE/EDIT or
  JSON. Do NOT skip ahead in the Queue.

## Version

This file is versioned alongside the gt binary. On reinstall, the embedded copy
is refreshed from the gastown repository. Edits to `~/.gt/orchestrator/STANDARDS.md`
are overwritten on reinstall — make changes in the gastown source and rebuild.