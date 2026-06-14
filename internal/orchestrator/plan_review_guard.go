package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// PlanReviewFailureNeedsArchitect reports plan_review failures that require architecture.md edits
// (planner cannot write architecture.md — send workflow to design instead of planning loop).
// Generic checklist wording ("architecture.md and plan.md must agree on store API names") must not
// escalate; only explicit architecture revision signals do.
func PlanReviewFailureNeedsArchitect(summary string) bool {
	lower := strings.ToLower(strings.TrimSpace(summary))
	if lower == "" {
		return false
	}
	if planReviewFailureIsPlannerOwned(lower) {
		return false
	}
	if strings.Contains(lower, "revise architecture") ||
		strings.Contains(lower, "rewrite architecture") ||
		strings.Contains(lower, "architecture.md must") ||
		strings.Contains(lower, "architecture drift") ||
		strings.Contains(lower, "architecture_failure") {
		return true
	}
	if !strings.Contains(lower, "architecture.md") {
		return false
	}
	return strings.Contains(lower, "store api signature") ||
		strings.Contains(lower, "signatures drift") ||
		strings.Contains(lower, "context.context") ||
		strings.Contains(lower, "package-level") ||
		(strings.Contains(lower, "drift") && strings.Contains(lower, "spec"))
}

func planReviewFailureIsPlannerOwned(lower string) bool {
	plannerOwned := []string{
		"planner must",
		"missing bead",
		"duplicate",
		"bd delete",
		"bd create",
		"zero open implement",
		"no open implement",
		"no implement bead",
		"integration contract",
		"plan.md is missing",
		"plan.md missing",
		"plan.md does not",
		"plan.md incorrectly",
		"beads database not initialized",
		"bd list fails",
		"bd list command failed",
	}
	for _, needle := range plannerOwned {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// PlanReviewSpuriousFailureReason returns guidance when a plan_review failure summary cites
// issues that contradict mechanical checks (empty string when not spurious).
func PlanReviewSpuriousFailureReason(townRoot, rig, summary string, v WorkflowValidation) string {
	v = v.ForActivePhase()
	lower := strings.ToLower(summary)
	if !profileHasServerEntrypoint(v) &&
		strings.Contains(lower, "integration contract") {
		return "Integration contract is not required in the active phase (no cmd/.../main.go in required_files). Do not fail for a missing ## Integration contract."
	}
	if planReviewSummaryClaimsMissingImplementBead(lower) {
		covered, err := allRequiredPathsCoveredByActiveBeads(townRoot, rig, v)
		if err == nil && covered {
			return "Implement beads exist for every active-phase required_files path (open or in_progress). Re-run `bd list --status=open,in_progress` — do not fail for a missing bead."
		}
	}
	return ""
}

func planReviewSummaryClaimsMissingImplementBead(lower string) bool {
	return strings.Contains(lower, "missing bead") ||
		strings.Contains(lower, "zero open") ||
		strings.Contains(lower, "no open implement") ||
		strings.Contains(lower, "no implement bead")
}

func allRequiredPathsCoveredByActiveBeads(townRoot, rig string, v WorkflowValidation) (bool, error) {
	v = v.ForActivePhase()
	if len(v.RequiredFiles) == 0 {
		return true, nil
	}
	for _, want := range v.RequiredFiles {
		ok, err := openBeadCoversRequiredPath(townRoot, rig, want, v)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// PlanReviewGateSatisfied reports whether plan_review success preconditions hold on disk.
func PlanReviewGateSatisfied(townRoot, rig string, v WorkflowValidation) error {
	return ValidatePlanningPhaseGate(townRoot, rig, "plan_review", v.ForActivePhase())
}

// rejectSpuriousArchitectureRework validates QA architecture_failure claims against
// actual file content before allowing the workflow to reset to design. Returns a
// rejection reason when the claim is contradicted by files on disk.
func rejectSpuriousArchitectureRework(townRoot, rig, summary string) string {
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	lower := strings.ToLower(strings.TrimSpace(summary))
	if strings.Contains(lower, "static") || strings.Contains(lower, "prefix") ||
		strings.Contains(lower, ".js") || strings.Contains(lower, ".css") {
		findFiles := []string{"linkshelf/web/index.html", "web/index.html"}
		for _, rel := range findFiles {
			abs := filepath.Join(rigDir, filepath.FromSlash(rel))
			body, err := os.ReadFile(abs)
			if err != nil {
				continue
			}
			text := string(body)
			if strings.Contains(text, "/static/") {
				return fmt.Sprintf("QA claims missing /static/ prefix but %s already uses /static/ paths on disk", rel)
			}
		}
	}
	if strings.Contains(summary, `"links"`) || strings.Contains(summary, "DOM id") {
		findFiles := []string{"linkshelf/web/index.html", "web/index.html", "linkshelf/web/app.js", "web/app.js"}
		for _, rel := range findFiles {
			abs := filepath.Join(rigDir, filepath.FromSlash(rel))
			body, err := os.ReadFile(abs)
			if err != nil {
				continue
			}
			text := string(body)
			if htmlGetElementByIDRE.MatchString(text) {
				for _, m := range htmlGetElementByIDRE.FindAllStringSubmatch(text, -1) {
					if len(m) >= 2 {
						id := strings.TrimSpace(m[1])
						if strings.Contains(string(body), `id="`+id+`"`) || strings.Contains(string(body), `id='`+id+`'`) {
							return fmt.Sprintf("QA claims DOM id %q mismatch but it exists in on-disk files", id)
						}
					}
				}
			}
		}
	}
	return ""
}

var htmlGetElementByIDRE = regexp.MustCompile(`getElementById\s*\(\s*["']([^"']+)["']\s*\)`)

// rejectSpuriousQAFailure validates QA failure claims against on-disk files before
// allowing the workflow to return to implementation. Returns a rejection reason when
// the QA's claim is contradicted by files that already exist and are correct.
func rejectSpuriousQAFailure(townRoot, rig, summary string) string {
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	lower := strings.ToLower(strings.TrimSpace(summary))
	if strings.Contains(lower, "does not exist") || strings.Contains(lower, "missing") ||
		strings.Contains(lower, "not found") || strings.Contains(lower, "no such file") {
		return ""
	}
	if strings.Contains(lower, "static") || strings.Contains(lower, ".js") || strings.Contains(lower, ".css") ||
		strings.Contains(lower, ".html") || strings.Contains(lower, "frontend") || strings.Contains(lower, "web") {
		if hasValidFrontendArtifacts(rigDir) {
			return "QA claims frontend issues but web artifacts exist and are valid on disk"
		}
	}
	if strings.Contains(lower, "import") || strings.Contains(lower, "module") {
		if hasPassingPythonTests(rigDir) {
			return "QA claims import/module issues but tests pass on disk"
		}
	}
	return ""
}

func hasValidFrontendArtifacts(rigDir string) bool {
	for _, rel := range []string{"frontend/index.html", "frontend/game/main.js"} {
		data, err := os.ReadFile(filepath.Join(rigDir, rel))
		if err != nil || len(data) < 50 {
			return false
		}
	}
	return true
}

func hasPassingPythonTests(rigDir string) bool {
	cmd := exec.Command(filepath.Join(rigDir, ".venv", "bin", "python3"),
		"-m", "pytest", filepath.Join(rigDir, "defender", "backend", "tests"), "-q")
	cmd.Dir = rigDir
	cmd.Env = append(os.Environ(), "PYTHONPATH="+rigDir)
	return cmd.Run() == nil
}
