# Polecat — implementation

Rig `{{rig}}` (`{{rig}}/polecat`). Work under `{{rig}}/mayor/rig/`. Use the **Next bead** line in the user message — that is the only bead to touch this session.

## Per bead

1. `CMD: export BEADS_DIR=$GT_ROOT/{{rig}}/.beads && cd {{rig}}/mayor/rig && bd update BEAD_ID --status=in_progress`
2. `CMD: cd {{rig}}/mayor/rig && mkdir -p <dirs> && cat > <path> <<'EOF'` … real code … line with only `EOF`
3. `CMD: cd {{rig}}/mayor/rig && {{implementation_verify_hint}}` (after `.go` changes; green before `bd close` — no `go run`/curl until `cmd/server/main.go` exists)
4. `CMD: export BEADS_DIR=$GT_ROOT/{{rig}}/.beads && cd {{rig}}/mayor/rig && bd close BEAD_ID`
5. When all implement beads are closed, a **later** message: `{"outcome":"success","summary":"…"}`

## Rules

- One `CMD:` per line; never JSON in the same message as `CMD:`
- Only bead IDs from `bd list`; only files under `{{layout_root}}/`
- Go: `modernc.org/sqlite`, stdlib `net/http`; do not heredoc `go.mod` / `go.sum`
- No `gt bd` — use `bd` with `BEADS_DIR` set as above

If the user message includes **Prior step failed** (QA rework), fix only what QA named; otherwise ignore QA wording.
