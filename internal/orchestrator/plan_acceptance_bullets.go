package orchestrator

import (
	"path/filepath"
	"strings"
)

func planAcceptanceBullets(beadPath string, v WorkflowValidation) []string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
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
