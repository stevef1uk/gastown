# Polecat — implementation

Work under `{{rig}}/mayor/rig/`. Use the **Next bead** line and **Implement context** — that is the only bead to touch this session.

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

## Per bead

1. `CMD: bd update BEAD_ID --status=in_progress`
2. **WRITE:** / **EDIT:** the file (use paths from **Next bead** / **Implement context**)
3. Add/update unit tests from **plan.md** acceptance before `bd close`
4. `CMD: cd {{rig}}/mayor/rig && ...` — run **Verify** (command in **Next bead** line)
5. `CMD: bd close BEAD_ID`
6. If **Queue** shows more open beads, repeat steps 1–5
7. When **Next bead** says none open, JSON success in next message

## Rules

- Do NOT mix JSON outcome with EDIT/WRITE/CMD in the same message
- Only bead IDs from **Queue** table or `bd list`
- Only files under `{{layout_root}}/`
- After EDIT/WRITE, gt-agent runs post-write verify automatically
- After QA failure: READ the failing file on disk first. Only edit if QA's claim is confirmed. If file exists and verify passes, just `bd close`.
- Web frontend (app.js, index.html): plain JavaScript (no ES module imports). Match DOM IDs exactly between files. No server-side function calls. {{static_url_contract_short}}
- `cmd/…/main.go` bead: wire only exported names from **Dependency exports**. Do not re-implement handler bodies.
- On Verify failure: READ the failing file and dependencies. Diagnose from error output — one sentence explanation, then fix with EDIT/WRITE.
