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
			"Bead is implemented before `internal/store/store.go` in build order.",
		}
	}
	common := []string{
		"File exists at `" + beadPath + "` under the rig worktree.",
		"Matches layout and naming in architecture.md (no stub/placeholder implementation).",
		"Polecat runs **Verify** from the Next bead line before `bd close`.",
	}
	if IsTestImplementPath(beadPath) {
		return append([]string{
			"Unit tests assert **functional requirements** from SPEC.md and this plan section (happy path, errors, edge cases named in architecture).",
			"No trivial always-pass tests (`assert True`, empty `TestX` bodies, or tests that only import the module).",
			"Test names and cases map to SPEC/plan acceptance bullets (reference FR IDs or behavior in comments when helpful).",
		}, common...)
	}
	if WorkflowUsesGo(v) && strings.HasSuffix(beadPath, ".go") && !strings.HasSuffix(beadPath, "go.mod") {
		testPath := CorrelatedTestPathForSource(beadPath, v.LayoutRoot)
		if testPath != "" {
			common = append(common,
				"Package has unit tests in `"+testPath+"` (same bead or dedicated test bead) covering SPEC behavior before close.",
				"`go test` for this package must pass (Verify runs `go test -count=1 ./<pkg>/...`).",
			)
			if strings.Contains(beadPath, "internal/store/store.go") {
				common = append(common,
					"Store methods match SPEC (`List`/`Create`/`Delete` with `context.Context`); tests use `:memory:` + `InitSchema` — not a shared `./links.db`.",
				)
			}
		}
	}
	if WorkflowUsesPython(v) && strings.HasSuffix(beadPath, ".py") && !IsTestImplementPath(beadPath) {
		if testPath := CorrelatedTestPathForSource(beadPath, v.LayoutRoot); testPath != "" {
			common = append(common,
				"Add or update `"+testPath+"` with pytest cases tied to SPEC/plan acceptance before close.",
			)
		}
	}
	return common
}
