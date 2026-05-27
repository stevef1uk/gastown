# Python static/web cwd auto-fix (future work)

**Status:** Planned — implement when validating a Python rig with real pytest + static/template failures  
**Related:** [verify-and-smoke-gaps.md](./verify-and-smoke-gaps.md) (GT-VERIFY-001), Go implementation in `internal/orchestrator/http_handler_web_cwd_autofix.go`  
**Go reference shipped:** May 2026 (`TryAutoFixHandlerWebCwd404`, `cmd/gt-agent/implement_handler_web_cwd_recover.go`)

---

## Why this doc exists

The **handler web cwd auto-fix** added for **Go** (`linkshelf` / `net/http`) is **not** active for Python rigs. Post-write verify only runs the Go path when `WorkflowUsesGo` and the edited file ends in `.go`.

When you run rig-flow on a **Python document / rig**, capture a failing polecat session and use this doc to implement parity.

---

## Problem statement (Python analogue)

Same class of failure as testgt3 Go:

| Layer | Symptom | Common mistake |
|-------|---------|----------------|
| Production | `GET /` or static URL returns **404** in pytest with `TestClient` | `open("static/...")` or `Path("templates/...")` resolved from **pytest cwd** (package dir or `tests/`) instead of project root |
| Tests | pytest **passes** or fails opaquely | `os.chdir("..")`, `tmpdir` layouts, or `monkeypatch` that do not match architecture’s real `static/` / `templates/` tree |
| Polecat | Loops suggesting “create missing file” | Asset exists under layout root; model adds files in wrong place or patches tests only |

**Goal:** Gastown should **detect** “static/template 404 but assets exist on disk” and **apply a deterministic patch**, then **retry verify** (same UX as goimports / Go cwd auto-fix).

---

## What Go does today (template for Python)

1. **Detect** after failed verify:
   - `web/` (or profile `web_disk_dir`) assets present under `mayor/rig`
   - pytest/go test output suggests handler/static **404** (not compile/import errors)
2. **Patch** without LLM:
   - **Production:** stop using bare `os.getcwd()` for asset paths; use project-root discovery (`Path(__file__).resolve().parents[N]`, walk to `pyproject.toml` / `go.mod`-equivalent, or shared helper)
   - **Tests:** replace fragile `os.chdir("..")` / fixed `../..` with walk-up to root containing `static/` or `templates/`
3. **Retry** the same verify command once; log `[gt-agent] auto-fixed … web cwd`.

**Files (Go):**

- `internal/orchestrator/http_handler_web_cwd_autofix.go`
- `cmd/gt-agent/implement_handler_web_cwd_recover.go`
- `cmd/gt-agent/implement_post_write.go` (retry after fix)
- `internal/orchestrator/httpprofiles/defaults/go-stdlib-servemux.json` (matchers + hints)

---

## Prerequisites before coding

Collect from the **first Python rig** that hits this in the wild:

1. **Layout** — `layout_root`, where `static/`, `templates/`, `web/` live vs `app/`, `src/`, `tests/`
2. **Framework** — Flask (`static_folder`, `template_folder`), FastAPI (`StaticFiles`, `Jinja2Templates`), Starlette, Django (different — may defer)
3. **Verify command** — exact `pytest` line from profile / per-bead verify
4. **Failure transcript** — full pytest output for one failing test (file:line, assertion message)
5. **Production + test source** — the `.py` files on disk when verify fails (redacted if needed)
6. **Architecture excerpt** — how SPEC/architecture names static URLs and disk paths

Without (4)–(5), matchers will be guesswork.

---

## Proposed design (GT-VERIFY-012)

### ID: GT-VERIFY-012 — Python static/template cwd auto-fix

**Priority:** P1 (after first Python rig repro)  
**Stack scope:** Flask / FastAPI / Starlette first; Django optional later

### Detection

| Signal | Notes |
|--------|--------|
| `WorkflowUsesPython(v)` | Not `WorkflowUsesGo` |
| Edited or verified path `*.py` | Hook post-write verify for Python (today skipped) |
| Assets on disk | Reuse or extend `ProfileRequiresWebAssets` / `MissingWebAssetPaths` for `static/`, `templates/`, `web/` per profile |
| pytest output | New matchers, e.g. `test_*.py` + `404` + (`Not Found` \| `assert 404` \| `FileNotFoundError` \| `No such file`) |
| Negative guards | Do not run when `ModuleNotFoundError`, `SyntaxError`, `IndentationError`, or missing requirements |

**New file (suggested):** `internal/orchestrator/python_static_cwd_autofix.go`

**Profile (suggested):** extend HTTP profile JSON or add `python-static-serve.json` under `httpprofiles/defaults/` with:

- `static_disk_dirs`: `["static", "web", "templates"]`
- `test_output_matchers`: framework-specific substrings
- `hints` for polecat (if auto-fix cannot patch)

### Auto-fix strategies (ordered)

1. **`conftest.py` / test module** — inject or replace:
   - `find_project_root()` walking parents until `pyproject.toml` or `requirements.txt` + `static/` (or profile dir)
   - `os.chdir(root)` in session-scoped fixture (`@pytest.fixture(scope="session", autouse=True)`) **only** when architecture says real tree on disk (not `tmp_path` fiction)
2. **App factory / routes module** — replace patterns like:
   - `Path("static")` / `open("templates/index.html")` → `PROJECT_ROOT / "static" / ...`
   - Flask: ensure `static_folder` / `template_folder` are absolute paths from `__file__`
   - FastAPI: `StaticFiles(directory=str(project_root / "static"))`
3. **Do not** auto-create missing HTML/JS — same rule as Go: if asset missing, fail prerequisites, not cwd patch

### gt-agent integration

Mirror Go:

```text
runPostNativeWriteVerify (Python branch)
  → run pytest/compile verify
  → on failure: tryPythonStaticCwdAutoFix(mayorDir, out)
  → retry verify once
```

**Files to touch:**

- `cmd/gt-agent/implement_post_write.go` — branch for `WorkflowUsesPython` + `.py`
- `cmd/gt-agent/implement_python_static_cwd_recover.go` (new)
- `internal/orchestrator/python_workflow.go` — per-bead verify command helper if missing
- `internal/orchestrator/town/prompts/rig-flow/implementation.md` — Python row for static/tests (align with Go GT-VERIFY-001)

### Write-time guards (optional, phase 2)

- Reject `os.chdir` into `tmp_path` / `TemporaryDirectory` for implement beads that own `test_*routes*.py` when profile requires real `static/`
- Reject shell lines pasted into `.py` (already partially covered by `CheckPythonSourceValid`)

### Acceptance criteria

- [ ] Fixture rig under `internal/orchestrator/…_test.go` with minimal Flask/FastAPI app: pytest 404 → auto-fix → green on retry
- [ ] Real rig session log shows `[gt-agent] auto-fixed python static cwd: …` and verify retry
- [ ] No auto-fix when `static/index.html` (or profile path) is actually missing
- [ ] No auto-fix on import/syntax failures
- [ ] Hints still emitted when patch patterns do not match (unknown framework)
- [ ] Documented in [verify-and-smoke-gaps.md](./verify-and-smoke-gaps.md) issue index

---

## Testing checklist (when you have a Python document)

1. Run implementation on a profile with `web/` or `static/` in `required_files` and pytest verify.
2. Induce failure: handler uses relative paths; test runs from `mayor/rig` or package subdir.
3. Confirm polecat log shows failed verify → (after implementation) auto-fix line → retry.
4. Record in this doc:
   - Framework name
   - Exact pytest failure lines used for matchers
   - Before/after snippets that auto-fix should generate
5. Add regression test to `python_static_cwd_autofix_test.go` using those snippets.

---

## Out of scope (initial Python pass)

- Django `STATIC_ROOT` / collectstatic workflows
- SPA build artifacts (`dist/`) unless architecture explicitly lists them
- Changing QA smoke curl behavior (separate from unit-test cwd)
- Auto-fixing corrupted `<<<<<<< SEARCH` in `.py` (handled by corruption cleanup today)

---

## Cross-reference: existing Python support

| Feature | Location | Same problem? |
|---------|----------|----------------|
| Corruption cleanup | `implementation_corruption_cleanup.go`, `scripts/detect_corrupted_open_beads.py` | Markers / invalid syntax — **yes**, different |
| `CheckPythonSourceValid` | `python_sanity.go` | Shell pasted as Python — **no** |
| pytest `cd` normalization | `orchestrated_exec.go`, `python_workflow.go` | Command cwd — **related**, not test-body cwd |
| HTTP contract / web prerequisites | `http_handler_prerequisites.go` | Go handler paths — **reuse asset detection**, not patches |

---

## Issue index entry (for verify-and-smoke-gaps.md)

When implementing, add:

| ID | Priority | Title |
|----|----------|--------|
| GT-VERIFY-012 | P1 | Python static/template cwd auto-fix (pytest + real `static/` tree) |

Link to this file.
