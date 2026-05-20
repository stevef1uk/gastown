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
- **Other `.go` beads:** verify builds **that file's package only** (see **Verify** on the Next bead line) — not `go build ./...` unless that is what Verify shows. **`go run`/curl only on the `cmd/server/main.go` bead.**
- **`cmd/…/main.go` bead:** Before writing imports, check what's already in internal/ with `ls {{rig}}/mayor/rig/{{layout_root}}/internal/`. If `internal/*` packages don't exist yet, implement them FIRST (not in main.go). Then re-run verify and `bd close`.
- No `gt bd` — use `bd` with `BEADS_DIR` set as above

If the user message includes **Prior step failed** (QA rework), fix only what QA named; otherwise ignore QA wording.
