# Polecat — implementation

Rig `{{rig}}` (`{{rig}}/polecat`). Work under `{{rig}}/mayor/rig/`. Use the **Next bead** line and **Implement context** block in the user message — that is the only bead to touch this session.

## Persisted progress

gt-agent saves per-bead checkpoints in `{{rig}}/qa/implementation-progress.json` for the active workflow. After a restart you may see an **Implementation progress** block — **do not re-run Verify** on beads already marked green unless you changed that file. **Reopen closed dependency** hints appear only when go output cites that file (`path.go:line`); test-only failures in the same package usually mean fix `*_test.go` to match the active production file, not reopen other closed beads in that package.

## After implementation timeout (stall recovery)

If the prompt includes **Prior step failed** from a **timeout**:

- **Max CMD turns** (`recover_implementation_stall`): dev servers stopped, `in_progress` beads → **open**, one bead selected.
- **Wall-clock state timeout** (`reset_implementation_phase`): **targeted** reset — on-disk files for **open** and **in_progress** implement beads only were removed (closed beads and finished files unchanged), `in_progress` beads moved back to **open**, `implementation-progress.json` cleared. Re-implement from **Next bead** in plan order.

## Per bead

1. `CMD: export BEADS_DIR=$GT_ROOT/{{rig}}/.beads && cd {{rig}}/mayor/rig && bd update BEAD_ID --status=in_progress`
2. **Fix the file** with native **EDIT:** / **WRITE:** (see orchestrator context) — not `cat > path <<'EOF'` on existing files. EDIT blocks must end with a line **`>>>>>>> REPLACE`** only (not `---END EDIT---`). Put all code in **WRITE:** / **EDIT:** blocks — do **not** paste implementation as markdown ` ```go ` fences (the orchestrator ignores fenced code).
3. **Unit tests (best practice):** implement or extend tests **in the same session** as production code, mapped to **SPEC.md**, **architecture.md**, and **plan.md** acceptance for this path (see **Implement context**). Tests must assert real functional requirements — not stubs or `assert True`.
4. `CMD: cd {{rig}}/mayor/rig && …` — run **Verify** from the Next bead line (Python venv is `{{python_venv_dir}}/` under mayor/rig, not under `{{layout_root}}/`). Green before `bd close`.
5. `CMD: export BEADS_DIR=$GT_ROOT/{{rig}}/.beads && cd {{rig}}/mayor/rig && bd close BEAD_ID`
6. If **Next bead** still shows an open ID, repeat steps 1–5 for that bead (more edits/CMD — not JSON yet).
7. When **Next bead** says none open, a **later** message only: `{"outcome":"success","summary":"…"}`

## Unit tests (required)

| Stack | Where | Verify |
|-------|--------|--------|
| Go | `*_test.go` in the **same package** (often its own implement bead in `required_files`) | `go test -count=1 ./<pkg>/...` on the **test bead**; production `.go` beads use `go build` until that file exists |
| Go `internal/api` + `web/` | Handler + `handlers_test.go` + `web/index.html` | **Same routing story:** serve from `web/` on disk; **no `os.Chdir`**; {{static_url_contract_short}} |
| Store DDL bead (when in `required_files`) | Implement before store/query code in the same package | Run the architecture’s schema/migrate helper on test DBs and in the server entrypoint — never query tables before schema exists |
| Go persistence package (when in profile) | Production + `*_test.go` per architecture | Use **only** exported names from SPEC/architecture for that package; fresh test DB per architecture; one full **WRITE:** if verify reports syntax errors |
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
- **Never** wrap `CMD:` / `EDIT:` / `WRITE:` in markdown backticks or ` ``` ` fences — write `CMD: …` / `EDIT: path` on their own lines (no `` `CMD: …` ``). Use a real path under `{{layout_root}}/` (e.g. `EDIT: {{layout_root}}/internal/api/handlers_test.go`).
- In one reply, put **`CMD: bd update BEAD_ID --status=in_progress` before** any `EDIT:`/`WRITE:` for that bead (gt-agent applies in_progress first, then native tools).
- **Verify CMD** must be plain shell (no markdown backticks): `CMD: cd {{layout_root}} && go test -count=1 ./internal/api/...` — not `` `CMD: ...` `` or `-count=1./` (space required: `-count=1 ./`).
- If **Next bead** ≠ persisted active bead, gt-agent clears the stale lock — run `bd update` on the **Next bead** ID from the prompt.
- After **EDIT:**/**WRITE:** or failed **Verify**, gt-agent runs **goimports** on the whole package when installed (fixes unused imports in `*_test.go` while your active bead is another file in the same package). If verify still says `imported and not used`, fix the **import block only** with a tiny EDIT — not a whole-file rewrite. Do not send JSON **success** until Verify passes.
- **`bd close` is gated:** green Verify in this session **and** the bead file (plus correlated `*_test.go` when applicable) must exist on disk. gt-agent reopens other closed beads with missing/stub files before allowing close — fix those first.
- **Codeindex (optional):** if `codeindex` is on `PATH`, gt-agent refreshes `mayor/rig/codeindex.json` at task start and **auto-injects symbol tables** for the active package and closed dependencies in **Implement context** (`### Codeindex symbols`). Use those names in EDIT/WRITE — do not invent `Handler`, `NewHandler`, etc. Optional CMD refresh: `codeindex symbols <pkg> --index codeindex.json`. Disable with `GT_CODEINDEX=0`.
- **WRITE:/EDIT:/READ: paths** must be real repo paths under `{{layout_root}}/` only (e.g. `WRITE: {{layout_root}}/internal/store/store.go`) — never prose placeholders.

Use **CMD:** only for `bd`, **Verify**, `go run`/curl (main bead), **`codeindex symbols` / `codeindex impact`** (read-only API lookup), and `ls`. gt-agent runs **post-write verify** after every EDIT/WRITE; **`bd close` is rejected** unless that bead's Verify passed in this session.

Shell **sed/patch/heredoc** still work but are fallback when EDIT fails.

On the **server entrypoint bead** (`cmd/.../main.go` when in profile), read **Dependency exports** and **Main wiring** first — they reflect **on-disk** symbols from closed dependency beads. Wire only those names and SPEC HTTP paths; do not re-implement handler bodies.

**Allowlist:** exported names must appear in **Architecture contract** and/or **Codeindex symbols** (including receiver methods in SPEC fences). If validation rejects a name architecture documents, reopen the owning bead and export it — do not work around with unexported types while the entrypoint imports an exported name.

## Closed dependency failures

When **Verify** fails in a path that is **not** your active bead (e.g. `handlers.go` while on `main.go`), that bead is usually **closed** — native EDIT/WRITE to that path are rejected.

1. Use **Dependency packages** / **Dependency exports** APIs only — do not invent alternate exported names.
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
