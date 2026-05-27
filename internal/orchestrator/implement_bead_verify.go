package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/steveyegge/gastown/internal/config"
)

// bdCloseImplementBeadHook is set by tests to avoid calling bd close.
var bdCloseImplementBeadHook func(townRoot, rig, beadID string) error

// implementBeadVerifyEvaluator memoizes Go Verify results for one reconcile pass (deterministic, no duplicate go test runs).
type implementBeadVerifyEvaluator struct {
	rigDir string
	v      WorkflowValidation
	memo   map[string]bool
}

func newImplementBeadVerifyEvaluator(rigDir string, v WorkflowValidation) *implementBeadVerifyEvaluator {
	return &implementBeadVerifyEvaluator{
		rigDir: rigDir,
		v:      v.ForActivePhase(),
		memo:   map[string]bool{},
	}
}

func (e *implementBeadVerifyEvaluator) GoSatisfied(beadPath string) bool {
	if e == nil || !WorkflowUsesGo(e.v) {
		return false
	}
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if beadPath == "" || IsProjectSetupArtifactPath(beadPath, e.v) {
		return false
	}
	if !strings.HasSuffix(beadPath, ".go") && !strings.HasSuffix(beadPath, "go.mod") {
		return false
	}
	if v, ok := e.memo[beadPath]; ok {
		return v
	}
	green := implementBeadGoVerifyGreenUncached(e.rigDir, beadPath, e.v)
	e.memo[beadPath] = green
	return green
}

// VerifySatisfied reports whether an implement bead's on-disk artifact is ready to close:
// Go paths use package verify; frontend paths (.html/.css/.js) use non-stub artifact checks.
func (e *implementBeadVerifyEvaluator) VerifySatisfied(beadPath string) bool {
	if e == nil {
		return false
	}
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if beadPath == "" || IsProjectSetupArtifactPath(beadPath, e.v) {
		return false
	}
	if e.GoSatisfied(beadPath) {
		return true
	}
	if !isFrontendImplementPath(beadPath) {
		return false
	}
	if v, ok := e.memo[beadPath]; ok {
		return v
	}
	green := !beadImplementationNeedsRework(e.rigDir, beadPath, e.v)
	e.memo[beadPath] = green
	return green
}

func bdCloseImplementBead(townRoot, rig, beadID string) error {
	if bdCloseImplementBeadHook != nil {
		return bdCloseImplementBeadHook(townRoot, rig, beadID)
	}
	beadID = strings.TrimSpace(beadID)
	if beadID == "" {
		return nil
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	workDir := filepath.Join(townRoot, rig, "mayor", "rig")
	cmd := exec.Command("bd", "close", beadID)
	cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("bd close %s: %w: %s", beadID, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RunGoCompileVerifyForBead runs the profile Verify command for a single implement bead path.
func RunGoCompileVerifyForBead(mayorRigDir, beadPath string, v WorkflowValidation) error {
	if !WorkflowUsesGo(v) {
		return fmt.Errorf("not a Go workflow")
	}
	verify := strings.TrimSpace(GoCompileVerifyCommandForBead(v, mayorRigDir, beadPath))
	if verify == "" {
		return fmt.Errorf("empty verify command for %s", beadPath)
	}
	cmd := exec.Command("/bin/bash", "-c", verify)
	cmd.Dir = mayorRigDir
	cmd.Env = os.Environ()
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		text := strings.TrimSpace(string(out))
		if text == "" {
			text = runErr.Error()
		}
		return fmt.Errorf("verify failed: %w\n%s", runErr, text)
	}
	return nil
}

func implementBeadGoVerifyGreenUncached(rigDir, beadPath string, v WorkflowValidation) bool {
	if err := ValidateBeadArtifactOnDisk(rigDir, beadPath, v); err != nil {
		return false
	}
	return RunGoCompileVerifyForBead(rigDir, beadPath, v) == nil
}

// ImplementBeadGoVerifyGreen reports whether a Go implement bead's file exists and its Verify passes.
func ImplementBeadGoVerifyGreen(rigDir, beadPath string, v WorkflowValidation) bool {
	return newImplementBeadVerifyEvaluator(rigDir, v).GoSatisfied(beadPath)
}

// implementBeadsIndexedByPath maps normalized implement paths to beads (first wins per path).
func implementBeadsIndexedByPath(townRoot, rig string, v WorkflowValidation, statuses ...string) (map[string]PlanBead, error) {
	out := map[string]PlanBead{}
	for _, status := range statuses {
		beads, err := listImplementBeadsForGuard(townRoot, rig, v, status)
		if err != nil {
			return nil, err
		}
		for _, b := range beads {
			p := normalizeImplementBeadPath(b.Title, v)
			if p == "" || b.ID == "" {
				continue
			}
			if _, ok := out[p]; !ok {
				out[p] = b
			}
		}
	}
	return out, nil
}

func normalizeImplementBeadPath(title string, v WorkflowValidation) string {
	return NormalizeBeadPathForLayout(
		ExtractPathFromBeadTitle(title, v.BeadTitleContains), v.LayoutRoot)
}

// orderedImplementBeadPaths returns required_files build order (stable sort).
func orderedImplementBeadPaths(v WorkflowValidation) []string {
	return OrderRequiredFilesForImplementation(v.RequiredFiles)
}

// CloseImplementBeadsWithGreenGoVerify closes open implement beads whose verify/artifact
// checks pass (Go: go test/build per bead; frontend: non-stub file on disk), in profile build order.
func CloseImplementBeadsWithGreenGoVerify(townRoot, rig string, v WorkflowValidation, eval *implementBeadVerifyEvaluator) ([]string, error) {
	if townRoot == "" || rig == "" || !WorkflowUsesGo(v) {
		return nil, nil
	}
	if bdCloseImplementBeadHook == nil && !BeadsDatabaseReady(townRoot, rig) {
		return nil, nil
	}
	if eval == nil {
		rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
		eval = newImplementBeadVerifyEvaluator(rigDir, v)
	}
	v = v.ForActivePhase()
	// Only auto-close open beads — never in_progress (polecat may be mid-edit; reconcile runs every fetch_task).
	active, err := implementBeadsIndexedByPath(townRoot, rig, v, "open")
	if err != nil {
		return nil, err
	}
	var closed []string
	for _, rel := range orderedImplementBeadPaths(v) {
		b, ok := active[filepath.ToSlash(rel)]
		if !ok {
			continue
		}
		if !eval.VerifySatisfied(rel) {
			continue
		}
		if err := bdCloseImplementBead(townRoot, rig, b.ID); err != nil {
			return closed, err
		}
		closed = append(closed, b.ID)
	}
	sort.Strings(closed)
	return closed, nil
}

// CloseImplementBeadsWithGreenFrontendVerify closes open and in_progress implement beads whose
// frontend artifacts pass VerifySatisfied. Used when phase verify is still red so Go/handler beads
// stay open until compile and smoke pass, but finished web assets can leave the queue.
func CloseImplementBeadsWithGreenFrontendVerify(townRoot, rig string, v WorkflowValidation, eval *implementBeadVerifyEvaluator) ([]string, error) {
	if townRoot == "" || rig == "" || !WorkflowUsesGo(v) {
		return nil, nil
	}
	if bdCloseImplementBeadHook == nil && !BeadsDatabaseReady(townRoot, rig) {
		return nil, nil
	}
	if eval == nil {
		rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
		eval = newImplementBeadVerifyEvaluator(rigDir, v)
	}
	v = v.ForActivePhase()
	active, err := implementBeadsIndexedByPath(townRoot, rig, v, "open", "in_progress")
	if err != nil {
		return nil, err
	}
	var closed []string
	for _, rel := range orderedImplementBeadPaths(v) {
		if !isFrontendImplementPath(rel) {
			continue
		}
		b, ok := active[filepath.ToSlash(rel)]
		if !ok {
			continue
		}
		if !eval.VerifySatisfied(rel) {
			continue
		}
		if err := bdCloseImplementBead(townRoot, rig, b.ID); err != nil {
			return closed, err
		}
		closed = append(closed, b.ID)
	}
	sort.Strings(closed)
	return closed, nil
}

// reopenClosedImplementBeadsOrdered reopens closed implement beads that still need work, in profile order.
// Go beads with green Verify are never reopened.
func reopenClosedImplementBeadsOrdered(townRoot, rig string, v WorkflowValidation, eval *implementBeadVerifyEvaluator) ([]string, error) {
	if eval == nil {
		rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
		eval = newImplementBeadVerifyEvaluator(rigDir, v)
	}
	v = v.ForActivePhase()
	closed, err := implementBeadsIndexedByPath(townRoot, rig, v, "closed")
	if err != nil {
		return nil, err
	}
	if len(closed) == 0 {
		return nil, nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var reopened []string
	for _, rel := range orderedImplementBeadPaths(v) {
		b, ok := closed[filepath.ToSlash(rel)]
		if !ok {
			continue
		}
		if IsProjectSetupArtifactPath(rel, v) {
			continue
		}
		if !beadImplementationNeedsRework(rigDir, rel, v) {
			continue
		}
		if eval.VerifySatisfied(rel) {
			continue
		}
		if err := bdUpdateImplementBeadStatus(townRoot, rig, b.ID, "open"); err != nil {
			return reopened, err
		}
		reopened = append(reopened, b.ID)
	}
	sort.Strings(reopened)
	return reopened, nil
}
