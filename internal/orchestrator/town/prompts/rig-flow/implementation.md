# Polecat — implementation

{{phase_scope_note}}

Work under `{{rig}}/mayor/rig/`. Use the **Next bead** line and **Implement context** — that is the only bead to touch this session.

## Directory structure (CRITICAL — read before any file operations)

The rig directory layout is:
```
$GT_ROOT/                          ← town root (NEVER create files here)
$GT_ROOT/{{rig}}/                  ← rig root (NEVER create files here)
$GT_ROOT/{{rig}}/mayor/rig/        ← working directory (cd here for commands)
$GT_ROOT/{{rig}}/mayor/rig/{{layout_root}}/  ← layout root (ALL files go here)
```

**Rules:**
- `cd {{rig}}/mayor/rig` before running commands (bd, verify, etc.)
- WRITE files to `{{layout_root}}/path/file.ext` (relative to mayor/rig)
- NEVER use `$GT_ROOT/{{rig}}/backend/` or `$GT_ROOT/{{rig}}/frontend/` — those are WRONG
- NEVER use `$GT_ROOT/{{rig}}/{{layout_root}}/backend/` — use `{{layout_root}}/backend/` instead
- The `mayor/rig/` prefix is only for `cd` commands, not for file paths

**Scope: evaluate only the active phase.** The Queue table shows only the current phase's beads. Ignore files from later phases (SPEC/architecture may list them, but they are not your concern). If all Queue beads are closed and verify passes, return `success` — do NOT fail because files from other phases are missing.

**Integration rule**: After creating new source files (components, routes, modules), update existing entry points to wire them in. For frontend apps: import new components in `page.tsx` / `layout.tsx` / `index.tsx` / equivalent. For backends: register new routes in `main.py` / `routes.py` / `app.py` / equivalent. This exception to phase scope is required — the SPEC describes a fully integrated application, and orphaned files that pass compilation but are never called produce a broken app.

## Output format (follow exactly)

**New file:**
```
WRITE: {{layout_root}}/path/file.go
package pkg
...
---END WRITE---
```

**Edit existing file:**
```
EDIT: {{layout_root}}/path/file.go
<<<<<<< SEARCH
old code
=======
new code
>>>>>>> REPLACE
```

**Shell command:**
```
CMD: cd {{rig}}/mayor/rig && ...
```

**Done (SEPARATE message, no CMD/WRITE/EDIT):**
```
{"outcome":"success","summary":"..."}
```

## Per-bead verify command

The phase verify command for this rig is:

```
{{phase_qa_verify_command}}
```

Run this **exactly as shown** (including the full relative path from the rig root) after writing code/tests. Do NOT shorten to a basename. Run it from `{{rig}}/mayor/rig/{{layout_root}}`.

## TDD Iron Law (MANDATORY — read before the per-bead sequence)

```
NO PRODUCTION CODE WITHOUT A FAILING TEST FIRST.
```

Follow the **red-green-refactor** cycle for every bead that has a test path in `plan.md` (or a testable unit):

1. **RED** — WRITE the test file FIRST (from **plan.md** acceptance bullets).
2. **Verify RED** — `CMD:` run the test. It MUST fail for the right reason (feature missing, not a typo). If it passes immediately, you are testing existing behavior — rewrite the test.
3. **GREEN** — WRITE the minimal implementation to make the test pass.
4. **Verify GREEN** — `CMD:` run the test. It MUST pass. If it fails, fix the code, not the test.
5. **REFACTOR** — clean up while the test stays green. Do not add behavior beyond the test.

**If you wrote implementation code before a failing test:** DELETE the implementation. Write the test first, watch it fail, then re-implement. Do not keep the code as "reference" — delete means delete.

**Commit tests separately from implementation** so tests cannot be silently weakened later:
- `git add <test files> && git commit -m "test: add failing tests for <bead path>"` (after RED, before implementation)
- `git add <impl files> && git commit -m "feat: implement <bead path>"` (after GREEN)

**Rationalization prevention** — if you catch yourself thinking any of these, STOP and redo the step with TDD:

| Excuse | Reality |
|--------|---------|
| "Too simple to test" | Simple code breaks. A test takes 30 seconds. |
| "I'll test after" | Tests written after pass immediately — they prove nothing. You never watched them fail, so you never proved they can catch the bug. |
| "Already spent time, deleting is wasteful" | Sunk cost. Rewriting with TDD gives high confidence; keeping untested code gives bugs. |
| "Tests are in plan.md, I'll write code and tests together" | No. Write the test, watch it fail, then implement. Violating the letter of the rule is violating the spirit. |

**Exceptions** (no test required): throwaway config, dependency manifests (`package.json`, `go.mod`), and pure artifact files (`Dockerfile`, `docker-compose.yml`, `index.html` static markup, `.sql` DDL). When `plan.md` lists a test path for a bead, the exception does not apply.

## Per bead — strict sequential (one bead per turn group)

**Touch exactly one bead per "turn group"** (a message + verify/retry loop). A turn group ends when you send the next CMD/WRITE/EDIT or JSON. Do NOT skip ahead in the Queue.

1. `CMD: bd update QUEUE_HEAD_ID --status=in_progress`
2. **WRITE:** / **EDIT:** the file (use paths from **Next bead** / **Implement context**)
3. **TDD cycle for this bead** — see the Iron Law above: WRITE test → verify RED → implement → verify GREEN → commit test and code in separate commits. Only skip the failing-test step when the bead is an exception listed above.
4. `CMD: cd {{rig}}/mayor/rig/{{layout_root}} && {{phase_qa_verify_command}}` — run the **phase verify** exactly as shown above. **Do NOT shorten to a basename** — use the full command including the relative path from `{{layout_root}}`.
5. **If verify fails**: READ the failing file, check its path. Fix in the next turn group. Do NOT reopen the bead.
6. `CMD: bd close BEAD_ID`
7. Look at the **Queue** table. If another bead is **open** (○) or **in_progress** (◐), repeat from step 1 with the next queue bead.
8. When all implement beads in **Queue** are **closed** (✓), send JSON success in the **next** message.

**CRITICAL: Do NOT re-verify or re-close already-closed beads. Do NOT add dependencies or edit files after all beads are closed. Once Queue shows all ✓, send JSON success immediately.**

## Rules
 
- Do NOT mix JSON outcome with EDIT/WRITE/CMD in the same message
- Only bead IDs from **Queue** table or `bd list`
- Only files under `{{layout_root}}/`
- After EDIT/WRITE, gt-agent runs post-write verify automatically
- After QA failure: READ the failing file and dependencies. Diagnose from error output — one sentence explanation, then fix with EDIT/WRITE.
- Web frontend (app.js, index.html): plain JavaScript (no ES module imports). Match DOM IDs exactly between files. No server-side function calls. {{static_url_contract_short}}
- SQL beads (.sql): validate with the sqlite3 verify command in the Next bead line — do NOT run pytest on .sql files
- `cmd/…/main.go` bead: wire only exported names from **Dependency exports**. Do NOT write inline handler bodies that return hardcoded JSON. For example, prefer `h.ListLinks(w, r)` over `w.Write([]byte("[]"))`. The handler logic belongs in `internal/api/`, not inlined in main.go.
- **DB dependency wiring**: if a package declares `var DB *sql.DB` (or similar package-level dependency), assign to it in main.go after `sql.Open` — e.g. `store.DB = db`. A nil package-level DB panics at runtime even though the code compiles clean.
- **Type ownership in shared packages**: If `architecture.md` ownership table says file A owns type `T`, file B in the same package MUST NOT redefine `T`. Import/use the type from file A. Violating this causes `redeclared in this block` build errors.
- **Docker / docker-compose beads:** the `app`/`web` service must **actually build and run the application** (not `sleep infinity` or a placeholder image). If the compose only validates config (`docker-compose -f ... config`), the app service can be minimal, but e2e compose must start a real server on the port the tests target.
- **E2E / Playwright / Cypress beads:**
  - Use **only** selectors and URLs documented in architecture.md / SPEC.md. Do not invent DOM IDs like `#chat-panel` unless they are listed in the Implement context.
  - Verify must start the app/dev-server first, or use docker-compose that starts it.
  - Do not write e2e tests that assert UI elements that do not exist in the implemented `index.html` / `app.js`.
  - **Playwright image (MANDATORY):** use the locally-built shared runner image `playwright-go-test:latest` — **do NOT** reference a public `mcr.microsoft.com/playwright:<version>` tag. The local image is pre-prepared and canonical; the public image must be pulled (hundreds of MB) and runs as root, producing root-owned artifacts in bind mounts.
  - **Playwright compose service (MANDATORY):** the E2E compose must define a service **literally named `playwright`** (not `e2e`, `tests`, or `app`), because QA runs `docker-compose up --exit-code-from playwright`. A differently-named service fails with `no such service: playwright` and the phase cannot pass QA.
  - The compose `playwright` service should set `user: "${DOCKER_UID:-1000}:${DOCKER_GID:-1000}"` and `working_dir` to the mounted test directory so `node_modules` resolves and artifacts are host-removable.
- On Verify failure: READ the failing file and dependencies. Diagnose from error output — one sentence explanation, then fix with EDIT/WRITE.
- **npm 10+ override rule:** If you write `package.json`, do NOT add `overrides` entries for packages already listed in `dependencies` or `devDependencies` — npm 10 rejects overrides that conflict with direct dependency version ranges. Only override transitive (nested) dependencies.
- Do **NOT** create Python virtual environments (`.venv`, `.venv2`, `uv venv`, `python3 -m venv`, `virtualenv`). The `project_setup` phase already created one; gt-agent activates it for you automatically.
