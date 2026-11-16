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

const supersededImplementReason = "superseded by canonical implement bead "
const supersededImplementLabel = "gt:implement-superseded:"

func isSupersededImplementBead(b PlanBead) bool {
	if strings.HasPrefix(b.CloseReason, supersededImplementReason) {
		return true
	}
	for _, label := range b.Labels {
		if strings.HasPrefix(label, supersededImplementLabel) {
			return true
		}
	}
	return false
}

var supersedeImplementBeadHook func(townRoot, rig, duplicate, canonical string) error

// CanonicalizeImplementBeads resolves ownership across statuses, not just the
// runnable queue. Superseded tickets retain their history and canonical link,
// but must never count as completion or be reopened by recovery.
func CanonicalizeImplementBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	v = v.ForActivePhase()
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	type owner struct {
		PlanBead
		priority int
	}
	owners := map[string][]owner{}
	for rank, status := range []string{"closed", "in_progress", "open", "blocked", "deferred"} {
		rows, err := listImplementBeadsForGuard(townRoot, rig, v, status)
		if err != nil {
			return nil, err
		}
		for _, b := range rows {
			path := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot, rig)
			if !pathMatchesRequiredForProfile(path, v.RequiredFiles, v) {
				continue
			}
			priority := rank
			if status == "closed" && AssessImplementationArtifact(rigDir, path, v).Status != ArtifactReady {
				priority = 5
			}
			owners[path] = append(owners[path], owner{b, priority})
		}
	}
	paths := make([]string, 0, len(owners))
	for path := range owners {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var superseded []string
	for _, path := range paths {
		rows := owners[path]
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].priority != rows[j].priority {
				return rows[i].priority < rows[j].priority
			}
			return rows[i].ID < rows[j].ID
		})
		for _, duplicate := range rows[1:] {
			if duplicate.ID == rows[0].ID {
				continue
			}
			if err := supersedeImplementBead(townRoot, rig, duplicate.ID, rows[0].ID); err != nil {
				return superseded, err
			}
			superseded = append(superseded, duplicate.ID+" -> "+rows[0].ID)
		}
	}
	return superseded, nil
}

func supersedeImplementBead(townRoot, rig, duplicate, canonical string) error {
	if supersedeImplementBeadHook != nil {
		return supersedeImplementBeadHook(townRoot, rig, duplicate, canonical)
	}
	// Use beads' native duplicate relation, retaining the canonical reference
	// and audit trail. A label also excludes already-closed duplicates (bd close
	// alone does not update their original close reason).
	for _, args := range [][]string{
		{"duplicate", duplicate, "--of", canonical},
		{"update", duplicate, "--add-label=" + supersededImplementLabel + canonical},
	} {
		cmd := exec.Command("bd", args...)
		cmd.Dir = filepath.Join(townRoot, rig, "mayor", "rig")
		cmd.Env = append(os.Environ(), "BEADS_DIR="+config.ResolveBeadsDirForRig(townRoot, rig))
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("supersede %s with %s: %w: %s", duplicate, canonical, err, out)
		}
	}
	return nil
}
