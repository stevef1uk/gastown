package orchestrator

import (
	"fmt"
	"io/fs"
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

	// Check for static path complaints — only reject when QA is actively
	// faulting the static prefix, not just mentioning it in passing.
	if isStaticPrefixComplaint(lower) {
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

	// Check for DOM id / field name complaints — only reject when QA mentions
	// a DOM element mismatch that actually exists on disk.
	if isDOMIdComplaint(summary) {
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

// isStaticPrefixComplaint reports whether summary actively faults the /static/ URL prefix.
func isStaticPrefixComplaint(lower string) bool {
	if !strings.Contains(lower, "static") {
		return false
	}
	// Positive/acknowledging mentions of static paths are not complaints.
	negators := []string{
		"correct static",
		"static path",
		"references correct",
		"already uses",
	}
	for _, n := range negators {
		if strings.Contains(lower, n) {
			return false
		}
	}
	return strings.Contains(lower, "missing") ||
		strings.Contains(lower, "should use") ||
		strings.Contains(lower, "needs /static") ||
		strings.Contains(lower, "require /static") ||
		strings.Contains(lower, "expects /static") ||
		strings.Contains(lower, "not at /static") ||
		strings.Contains(lower, "prefix") ||
		strings.Contains(lower, ".js") ||
		strings.Contains(lower, ".css")
}

// isDOMIdComplaint reports whether summary actively faults a DOM element ID mismatch.
func isDOMIdComplaint(summary string) bool {
	lower := strings.ToLower(strings.TrimSpace(summary))
	if !strings.Contains(lower, "dom") && !strings.Contains(lower, "getelementbyid") &&
		!strings.Contains(lower, "id mismatch") && !strings.Contains(lower, "id not found") {
		return false
	}
	return true
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
	if strings.Contains(lower, "collected 0 items") || strings.Contains(lower, "no tests ran") ||
		strings.Contains(lower, "no tests found") {
		if !hasAnyTestFiles(rigDir) {
			return "QA claims missing/empty tests but no test file exists on disk — test bead not yet implemented"
		}
	}
	if (strings.Contains(lower, "test failed") || strings.Contains(lower, "tests failed") ||
		strings.Contains(lower, "status 0")) && strings.Contains(lower, "no output") {
		if hasPassingPythonTests(rigDir) {
			return "QA claims test failure but pytest passes on disk — hallucinated failure"
		}
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
	// If QA reports ANY failure but phase verify passes on disk, the QA claim is wrong.
	// Prevents wasteful polecat re-implementation of already-correct code.
	if isFailureKeyWord(summary) && phaseVerifyPasses(townRoot, rig) {
		return "QA reports failure but phase verify passes on disk — QA claim is spurious"
	}
	return ""
}

func isFailureKeyWord(summary string) bool {
	lower := strings.ToLower(summary)
	for _, w := range []string{"missing", "not found", "no such", "does not exist",
		"broken", "incomplete", "truncated", "missing file", "doesn't exist"} {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

func phaseVerifyPasses(townRoot, rig string) bool {
	v, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		return false
	}
	v = v.ForActivePhase()
	return ImplementationPhaseVerifyOK(townRoot, rig, v) == nil
}

func hasPassingPythonTests(rigDir string) bool {
	venvBin := filepath.Join(rigDir, ".venv", "bin", "python3")
	if _, err := os.Stat(venvBin); err != nil {
		return false
	}
	cmd := exec.Command(venvBin, "-m", "pytest", "--collect-only", "-q")
	cmd.Dir = rigDir
	cmd.Env = append(os.Environ(), "PYTHONPATH="+rigDir)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "test") && !strings.Contains(string(out), "no tests ran")
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

func hasAnyTestFiles(rigDir string) bool {
	found := false
	_ = filepath.WalkDir(rigDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			base := strings.ToLower(d.Name())
			if base == ".venv" || base == "node_modules" || base == ".git" || base == ".gastown" {
				return filepath.SkipDir
			}
			return nil
		}
		name := strings.ToLower(d.Name())
		if strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test.py") ||
			strings.HasSuffix(name, "_test.go") {
			found = true
		}
		return nil
	})
	return found
}
