# Polecat — implementation

Rig `{{rig}}` (`{{rig}}/polecat`). Work under `{{rig}}/mayor/rig/`. Use the **Next bead** line and **Implement context** block in the user message — that is the only bead to touch this session.

## Persisted progress

gt-agent saves per-bead checkpoints in `{{rig}}/qa/implementation-progress.json` for the active workflow. After a restart you may see an **Implementation progress** block — **do not re-run Verify** on beads already marked green unless you changed that file. Reopen hints for compile errors in **closed** dependency beads list exact `bd update <id> --status=open` steps.

## After implementation timeout (stall recovery)

If the prompt includes **Prior step failed** from a **timeout**:

- **Max CMD turns** (`recover_implementation_stall`): dev servers stopped, `in_progress` beads → **open**, one bead selected.
- **Wall-clock state timeout** (`reset_implementation_phase`): **targeted** reset — **all `.go` and `.py`** under `layout_root` were **deleted** (`go.mod`, `go.sum`, `requirements.txt` kept; SPEC/plan/architecture unchanged), **all implement beads reopened**, `implementation-progress.json` cleared. Re-implement from **Next bead** in plan order.

## Per bead

1. `CMD: export BEADS_DIR=$GT_ROOT/{{rig}}/.beads && cd {{rig}}/mayor/rig && bd update BEAD_ID --status=in_progress`
2. **Fix the file** with native **EDIT:** / **WRITE:** (see orchestrator context) — not `cat > path <<'EOF'` on existing files. EDIT blocks must end with a line **`>>>>>>> REPLACE`** only (not `---END EDIT---`).
3. **Unit tests (best practice):** implement or extend tests **in the same session** as production code, mapped to **SPEC.md**, **architecture.md**, and **plan.md** acceptance for this path (see **Implement context**). Tests must assert real functional requirements — not stubs or `assert True`.
4. `CMD: cd {{rig}}/mayor/rig && …` — run **Verify** from the Next bead line (Python venv is `{{python_venv_dir}}/` under mayor/rig, not under `{{layout_root}}/`). Green before `bd close`.
5. `CMD: export BEADS_DIR=$GT_ROOT/{{rig}}/.beads && cd {{rig}}/mayor/rig && bd close BEAD_ID`
6. If **Next bead** still shows an open ID, repeat steps 1–5 for that bead (more edits/CMD — not JSON yet).
7. When **Next bead** says none open, a **later** message only: `{"outcome":"success","summary":"…"}`

## Unit tests (required)

| Stack | Where | Verify |
|-------|--------|--------|
| Go | `*_test.go` in the **same package** (often its own implement bead in `required_files`) | `go test -count=1 ./<pkg>/...` on the **test bead**; production `.go` beads use `go build` until that file exists |
| Store DDL bead (when in `required_files`) | Implement before store/query code in the same package | Run the architecture’s schema/migrate helper on test DBs and in the server entrypoint — never query tables before schema exists |
| Python | `tests/test_<module>.py` or `test_*.py` under `tests/` | `pytest -v` on that file when it exists |

- Read **From plan.md**, **Acceptance checklist**, and **From architecture.md** in Implement context — each case should trace to a SPEC/plan acceptance bullet.
- On a **test bead**, `bd update --status=in_progress` may create a minimal `*_test.go` skeleton — replace `TestPlaceholder` / `test_placeholder` with real cases before `bd close`.
- Do **not** `cat` or **READ:** a path that does not exist yet — use **WRITE:** on the active bead (or finish the production bead before opening the test bead).
- **Test bead:** only test code; cover happy path, errors, and edge cases named in architecture.
- **Production bead:** add or update the correlated test file before `bd close` if Verify runs `go test` / `pytest` (or a test bead is listed in `required_files`).
- Do **not** defer all tests to QA — QA runs the full suite (`{{unittest_command_hint}}`) plus runtime smoke.

## Native file tools (preferred)

gt-agent runs these directly (same turn as `CMD:` is allowed):

- **READ:** `layout/path.go` — inspect active bead or **Dependency packages** (read-only).
- **EDIT:** `layout/path.go` then a unique `<<<<<<< SEARCH` / `=======` / `>>>>>>> REPLACE` block (copy exact lines from READ or **Current file on disk**).
- **WRITE:** `layout/newfile.go` then file body until `---END WRITE---` — **new files only** (gt-agent rejects full WRITE on large existing files). Use **WRITE** for new `*_test.go` / `tests/test_*.py`.
- **Never** wrap EDIT/WRITE bodies (or heredoc file content) in markdown fences — no leading ` ```go ` or trailing ` ``` `; first line must be real source (`package …`, `import …`, etc.).
- **Never** prefix WRITE bodies with heredoc/EDIT markers — no `<<<<<<< EOF`, `<<<<<<< SEARCH`, `=======`, or `>>>>>>> REPLACE` (those are shell/EDIT syntax only, not Go/Python source).
- **Never** put heredoc markers in file bodies — no trailing line `EOF` / `EOT` / `END` (those belong only on their own line **after** a shell `cat <<'EOF'`, not inside **WRITE:** or **EDIT:**).
- **Never** wrap `CMD:` / `EDIT:` / `WRITE:` in markdown backticks — write `CMD: …` on its own line (no `` `CMD: …` ``).
- **WRITE:/EDIT:/READ: paths** must be real repo paths only (e.g. `WRITE: linkshelf/internal/store/store.go`) — never prose like `` ` command to create the file. `` or `** to create it per architecture**`.

Use **CMD:** only for `bd`, **Verify**, `go run`/curl (main bead), and `ls`. gt-agent runs **post-write verify** after every EDIT/WRITE; **`bd close` is rejected** unless that bead's Verify passed in this session.

Shell **sed/patch/heredoc** still work but are fallback when EDIT fails.

On **`cmd/server/main.go`**, read **Dependency packages** for real `store`/`handlers` symbols — wire routes in main; do not re-implement handler bodies.

## Closed dependency failures

When **Verify** fails in a path that is **not** your active bead (e.g. `handlers.go` while on `main.go`), that bead is usually **closed** — native EDIT/WRITE to that path are rejected.

1. Use **Dependency packages** APIs only (`AddLink`, not invented `CreateLink`).
2. Reopen the bead: `bd list --status=closed`, `CMD: bd update <id> --status=open`, fix with EDIT, Verify, `bd close <id>`, continue active bead. Do **not** send JSON `failure` instead of running `bd update`.
3. If blocked after **verify/EDIT attempts**, JSON only: `{"outcome":"failure","summary":"reopen <bead-id> for <path>: …"}` — no `bd update --status=failed`.
4. **gt-agent rejects** failure JSON with no EDIT/verify/bd work in the same task while open implement beads exist — you must run **Next bead** steps first.

## Rules

- **Before importing a package or type**, `READ:` or `ls` the package under `{{layout_root}}/internal/`.
- Do not mix JSON outcome with EDIT/WRITE/CMD in the same message.
- Only bead IDs from `bd list`; only files under `{{layout_root}}/`
- Go: use module/import paths from **Implement context** / architecture; do not heredoc `go.mod` / `go.sum`
- **go.mod bead:** `go mod init` / `go get` / `go mod tidy` via CMD only.
- **Other `.go` beads:** package-scoped **`go test`** from Next bead — not `go build ./...` unless Verify says so.
- **`go run`/curl** only on the `cmd/server/main.go` bead.
- **`cmd/…/main.go`:** if an earlier file's bead is **open**, implement that bead first; never WRITE/EDIT closed-bead paths.
- No `gt bd` — use `bd` with `BEADS_DIR` as above
- **Docker / compose beads:** final phase only; `docker compose config` is enough until QA.

If **Prior step failed**, use **EDIT:** on internal packages; **WRITE:** or heredoc CMD only for broken `cmd/…/main.go` wiring when duplicates/stubs block compile.
