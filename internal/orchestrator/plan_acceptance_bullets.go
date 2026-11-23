package orchestrator

import (
	"path/filepath"
	"strings"
)

func planAcceptanceBullets(beadPath string, v WorkflowValidation) []string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if IsSQLiteSchemaBeadPath(beadPath) {
		return []string{
			"File exists at `" + beadPath + "` under the rig worktree.",
			"Exports `InitSchema(*sql.DB) error` (or equivalent) with `CREATE TABLE IF NOT EXISTS` DDL from architecture.md.",
			"DDL matches architecture.md; production entrypoint and package tests call this helper — no duplicated `CREATE TABLE` strings in tests or main.",
			"Polecat runs **Verify** from the Next bead line before `bd close`.",
			"Bead is implemented before `" + StoreBeadPathFromProfile(v) + "` in build order.",
		}
	}
	common := []string{
		"File exists at `" + beadPath + "` under the rig worktree.",
		"Matches layout and naming in architecture.md (no stub/placeholder implementation).",
		"Polecat runs **Verify** from the Next bead line before `bd close`.",
	}
	if IsTestImplementPath(beadPath) {
		common = append([]string{
			"Unit tests assert **functional requirements** from SPEC.md and this plan section (happy path, errors, edge cases named in architecture).",
			"No trivial always-pass tests (`assert True`, empty `TestX` bodies, or tests that only import the module).",
			"Test names and cases map to SPEC/plan acceptance bullets (reference FR IDs or behavior in comments when helpful).",
		}, common...)
	}
	if WorkflowUsesGo(v) && strings.HasSuffix(beadPath, ".go") && !strings.HasSuffix(beadPath, "go.mod") {
		if testPath := CorrelatedTestPathForSource(beadPath, v); testPath != "" && TestPathListedInRequired(beadPath, v) {
			common = append(common,
				"Package has unit tests in `"+testPath+"` (same bead or dedicated test bead) covering SPEC behavior before close.",
				"`go test` for this package must pass (Verify runs `go test -count=1 ./<pkg>/...`).",
			)
		}
		if strings.Contains(beadPath, "internal/store/store.go") {
			common = append(common,
				"Store methods match SPEC (`List`/`Create`/`Delete` with `context.Context`); tests use `:memory:` + `InitSchema` — not a shared `./links.db`.",
			)
		}
	}
	if WorkflowUsesPython(v) && strings.HasSuffix(beadPath, ".py") && !IsTestImplementPath(beadPath) {
		if testPath := CorrelatedTestPathForSource(beadPath, v); testPath != "" && TestPathListedInRequired(beadPath, v) {
			common = append(common,
				"Add or update `"+testPath+"` with pytest cases tied to SPEC/plan acceptance before close.",
			)
		}
	}
	if isWebSourceBeadPath(beadPath) {
		common = append(common,
			"Application UI files must expose real interactive controls and client-side behavior (buttons/inputs/forms plus event handlers, fetch, or React state); a static content-only screen is not acceptance.",
			"QA must verify the rendered screen can perform at least one user action from this bead's acceptance criteria before reporting all_passed.",
		)
	}
	return append(planBehaviorAcceptanceBullets(beadPath, v), common...)
}

func isWebSourceBeadPath(beadPath string) bool {
	switch strings.ToLower(filepath.Ext(filepath.ToSlash(strings.TrimSpace(beadPath)))) {
	case ".html", ".htm", ".js", ".jsx", ".ts", ".tsx":
		return true
	default:
		return false
	}
}

func planBehaviorAcceptanceBullets(beadPath string, v WorkflowValidation) []string {
	path := filepath.ToSlash(strings.TrimSpace(beadPath))
	switch filepath.Base(path) {
	case "index.html":
		return []string{
			"Render the real application entry page with the canonical heading from SPEC.md, not a placeholder.",
			"Include project creation input/button, project selector/list, task form with required title, optional description, and priority select, plus every Kanban column required by SPEC.md.",
			"Include visible empty-state and error-state regions populated from API results.",
			"Reference the canonical static assets under `" + strings.Trim(v.LayoutRoot, "/") + "/static/`; do not embed implementation-only scripts or styles.",
		}
	case "app.js":
		return []string{
			"On page load call `GET /api/projects` and populate the project selector; selecting a project loads its tasks from `GET /api/projects/{project_id}/tasks`.",
			"Implement task create, edit, delete, and move-forward through the exact API routes in SPEC.md using status values `todo`, `doing`, and `done`.",
			"Insert user-provided task titles and descriptions with `textContent`/`replaceChildren`; never build markup from user data with raw `innerHTML`.",
			"Render visible empty/error states and disable or hide forward movement for done tasks as SPEC.md requires.",
		}
	case "style.css":
		return []string{
			"Style every Kanban column and task card from the rendered page, including narrow-window layout behavior.",
			"Style project/task forms, project selector, empty-state region, and API error region without external UI frameworks or icon libraries.",
		}
	case "test_api.py":
		return []string{
			"Assert `GET /api/health` returns exactly `{\"status\":\"ok\"}` with HTTP 200.",
			"Cover project create/list/delete, invalid-name 400, missing-project 404, empty `[]` lists, and 204 deletes with no body.",
			"Cover task create/list/patch/delete with defaults, invalid-field 400, unknown status/priority 400, empty-PATCH 400, missing-task 404, and ascending ID order.",
			"Assert UTC timestamps and exact JSON shapes from SPEC.md without asserting wall-clock time; defer status/priority filter scenarios to the phase that owns them.",
		}
	case "test_repository.py":
		return []string{
			"Cover empty schema, idempotent schema initialization, ordered project create/list, and task defaults with valid UTC timestamps.",
			"Preserve omitted fields on PATCH and verify project deletion cascades to its tasks.",
			"Assert SPEC negative cases explicitly (invalid status/priority, overlong title/description/project name) with concrete boundary values; defer filter scenarios to the phase that owns them.",
		}
	case "test_ui_content.py":
		return []string{
			"Assert `GET /` returns real HTML with the canonical `TaskBoard` heading, project form, task form, and all three Kanban column headings from SPEC.md.",
			"Assert `GET /static/app.js` and `GET /static/style.css` return HTTP 200 with nonempty real content.",
			"Assert the safe DOM contract from SPEC.md: user-provided task text is inserted with `textContent`/`replaceChildren`, not raw `innerHTML`.",
		}
	}
	return nil
}
