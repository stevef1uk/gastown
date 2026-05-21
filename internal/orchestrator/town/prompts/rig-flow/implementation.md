# Polecat — implementation

Rig `{{rig}}` (`{{rig}}/polecat`). Work under `{{rig}}/mayor/rig/`. Use the **Next bead** line and **Implement context** block in the user message — that is the only bead to touch this session.

## After implementation timeout (stall recovery)

If the prompt includes **Prior step failed** from a **timeout**, the orchestrator ran **`recover_implementation_stall`**: dev servers on tracked ports were stopped, in_progress implement beads were reset to **open**, and a single bead was selected for work. Continue with **Next bead** — do not re-close beads that are not green.

## Per bead

1. `CMD: export BEADS_DIR=$GT_ROOT/{{rig}}/.beads && cd {{rig}}/mayor/rig && bd update BEAD_ID --status=in_progress`
2. **Fix the file** with native **EDIT:** / **WRITE:** (see orchestrator context) — not `cat > path <<'EOF'` on existing files.
3. `CMD: cd {{rig}}/mayor/rig && …` — run **Verify** from the Next bead line (Python venv is `{{python_venv_dir}}/` under mayor/rig, not under `{{layout_root}}/`). Green before `bd close`.
4. `CMD: export BEADS_DIR=$GT_ROOT/{{rig}}/.beads && cd {{rig}}/mayor/rig && bd close BEAD_ID`
5. If **Next bead** still shows an open ID, repeat steps 1–4 for that bead (more edits/CMD — not JSON yet).
6. When **Next bead** says none open, a **later** message only: `{"outcome":"success","summary":"…"}`

## Native file tools (preferred)

gt-agent runs these directly (same turn as `CMD:` is allowed):

- **READ:** `layout/path.go` — inspect active bead or **Dependency packages** (read-only).
- **EDIT:** `layout/path.go` then a unique `<<<<<<< SEARCH` / `=======` / `>>>>>>> REPLACE` block (copy exact lines from READ or **Current file on disk**).
- **WRITE:** `layout/newfile.go` then file body until `---END WRITE---` — **new files only** (gt-agent rejects full WRITE on large existing files).

Use **CMD:** only for `bd`, **Verify**, `go run`/curl (main bead), and `ls`. Auto-verify runs after EDIT/WRITE.

Shell **sed/patch/heredoc** still work but are fallback when EDIT fails.

On **`cmd/server/main.go`**, read **Dependency packages** for real `store`/`handlers` symbols — wire routes in main; do not re-implement handler bodies.

## Closed dependency failures

When **Verify** fails in a path that is **not** your active bead (e.g. `handlers.go` while on `main.go`), that bead is usually **closed** — native EDIT/WRITE to that path are rejected.

1. Use **Dependency packages** APIs only (`AddLink`, not invented `CreateLink`).
2. Reopen the bead: `bd list --status=closed`, `bd update <id> --status=open`, fix with EDIT, `bd close <id>`, continue active bead.
3. If blocked, JSON only: `{"outcome":"failure","summary":"reopen <bead-id> for <path>: …"}` — no `bd update --status=failed`.

## Rules

- **Before importing a package or type**, `READ:` or `ls` the package under `{{layout_root}}/internal/`.
- Do not mix JSON outcome with EDIT/WRITE/CMD in the same message.
- Only bead IDs from `bd list`; only files under `{{layout_root}}/`
- Go: use module/import paths from **Implement context** / architecture; do not heredoc `go.mod` / `go.sum`
- **go.mod bead:** `go mod init` / `go get` / `go mod tidy` via CMD only.
- **Other `.go` beads:** package-scoped verify from Next bead — not `go build ./...` unless Verify says so.
- **`go run`/curl** only on the `cmd/server/main.go` bead.
- **`cmd/…/main.go`:** if an earlier file's bead is **open**, implement that bead first; never WRITE/EDIT closed-bead paths.
- No `gt bd` — use `bd` with `BEADS_DIR` as above
- **Docker / compose beads:** final phase only; `docker compose config` is enough until QA.

If **Prior step failed**, use **EDIT:** on internal packages; **WRITE:** or heredoc CMD only for broken `cmd/…/main.go` wiring when duplicates/stubs block compile.
