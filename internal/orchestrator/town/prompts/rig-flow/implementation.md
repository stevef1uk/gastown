# Polecat — implementation

Rig `{{rig}}` (`{{rig}}/polecat`). Work under `{{rig}}/mayor/rig/`. Use the **Next bead** line and **Implement context** block in the user message — that is the only bead to touch this session.

## Per bead

1. `CMD: export BEADS_DIR=$GT_ROOT/{{rig}}/.beads && cd {{rig}}/mayor/rig && bd update BEAD_ID --status=in_progress`
2. `CMD: cd {{rig}}/mayor/rig && mkdir -p <dirs> && cat > <path> <<'EOF'` … real code … line with only `EOF`
3. `CMD: cd {{rig}}/mayor/rig && …` — run **Verify** from the Next bead line (Python venv is `{{python_venv_dir}}/` under mayor/rig, not under `{{layout_root}}/`). Green before `bd close`.
4. `CMD: export BEADS_DIR=$GT_ROOT/{{rig}}/.beads && cd {{rig}}/mayor/rig && bd close BEAD_ID`
5. If **Next bead** still shows an open ID, repeat steps 1–4 for that bead (more `CMD:` lines — not JSON yet).
6. When **Next bead** says none open, a **later** message only: `{"outcome":"success","summary":"…"}`

## Rules

- **Before importing a package or type**, list files in that package first: `ls {{rig}}/mayor/rig/{{layout_root}}/internal/*/` to see what's already implemented. If the package or type doesn't exist, implement it FIRST before using it.
- One `CMD:` per line; never JSON in the same message as `CMD:`
- Only bead IDs from `bd list`; only files under `{{layout_root}}/`
- Go: use module/import paths from **Implement context** / architecture; do not heredoc `go.mod` / `go.sum`
- **go.mod bead:** use `go mod init` / `go get` (deps from architecture) / `go mod tidy` only — no heredoc for `go.mod`. If tidy fails, fix bad `import` lines in existing `.go` files shown in **Source context** (heredoc those files only). Verify is **tidy only** (no `go build`/`go run`/curl on this bead).
- **Other `.go` beads:** verify builds **that file's package only** (see **Verify** on the Next bead line) — not `go build ./...` unless that is what Verify shows. **`go run`/curl only on the `cmd/server/main.go` bead.** Run **only** the **Verify** line from **Next bead** (do not add `go build ./...` or `pkill`). gt-agent **frees stale listeners before each `go run`**, stops the server when the step finishes, and strips broken `pkill` tails from smoke commands.
- **Port already in use:** gt-agent should have cleared it; belt-and-braces: `CMD: bash "${GASTOWN:-$HOME/dev/freeride/gastown}/scripts/stop-rig-dev-servers.sh" 8080` (use the port from architecture.md), then re-run verify.
- **`cmd/…/main.go` bead:** Before writing imports, check what's already in internal/ with `ls {{rig}}/mayor/rig/{{layout_root}}/internal/`. If an earlier file's bead is still **open**, implement that bead first — **never heredoc over a path whose implement bead is already closed** (gt-agent rejects it; QA must reopen for rework).
- No `gt bd` — use `bd` with `BEADS_DIR` set as above
- **Docker / compose beads:** implement only when they appear on **Next bead** (final phase). `docker-compose.yml` must reference a real `Dockerfile` and app layout from earlier beads — `docker compose config` (or `docker-compose config`) is enough to verify; no full stack run required until QA.

If the user message includes **Prior step failed** (QA rework), fix only what QA named; otherwise ignore QA wording.
