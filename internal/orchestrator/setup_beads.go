package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/config"
)

// IsProjectSetupArtifactPath reports paths owned by project_setup (dependency manifests / module root),
// not polecat implementation.
func IsProjectSetupArtifactPath(path string, v WorkflowValidation) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return false
	}
	if req := v.RequirementsFilePath(); req != "" && pathMatchesRequired(path, []string{req}) {
		return true
	}
	base := strings.ToLower(filepath.Base(path))
	if base == "pyproject.toml" && WorkflowUsesPython(v) {
		return true
	}
	if WorkflowUsesGo(v) {
		if strings.HasSuffix(path, "/go.mod") || path == "go.mod" ||
			strings.HasSuffix(path, "/go.sum") || path == "go.sum" {
			return true
		}
	}
	return false
}

// CloseProjectSetupBeads closes open/in_progress implement beads for setup-owned paths when artifacts are ready.
func CloseProjectSetupBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	v = v.ForActivePhase()
	active, err := ListImplementBeadsOpenOrInProgress(townRoot, rig, v)
	if err != nil {
		return nil, err
	}
	if len(active) == 0 {
		return nil, nil
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	workDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var closed []string
	for _, b := range active {
		p := resolveImplementBeadPath(b.Title, v)
		if !IsProjectSetupArtifactPath(p, v) {
			continue
		}
		// Active delivery phase still owns this path — polecat must verify and bd close it.
		if pathMatchesRequired(p, v.RequiredFiles) {
			continue
		}
		if !projectSetupArtifactReady(workDir, p, v) {
			continue
		}
		cmd := exec.Command("bd", "close", b.ID, "--reason=project_setup")
		cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return closed, fmt.Errorf("bd close %s: %w: %s", b.ID, err, strings.TrimSpace(string(out)))
		}
		closed = append(closed, b.ID)
	}
	return closed, nil
}

// resolveImplementBeadPath maps a bead title to the canonical required_files path when possible.
func resolveImplementBeadPath(title string, v WorkflowValidation) string {
	p := NormalizeBeadPathForLayout(ExtractPathFromBeadTitle(title, v.BeadTitleContains), v.LayoutRoot)
	for _, want := range v.RequiredFiles {
		if pathMatchesRequired(p, []string{want}) {
			return want
		}
	}
	return p
}

func projectSetupArtifactReady(workDir, path string, v WorkflowValidation) bool {
	full := filepath.Join(workDir, filepath.FromSlash(path))
	if !rigFileNonEmpty(full) {
		return false
	}
	if WorkflowUsesPython(v) {
		if req := v.RequirementsFilePath(); req != "" && pathMatchesRequired(path, []string{req}) {
			venvPy := filepath.Join(workDir, v.PythonVenvRelDir(), "bin", "python3")
			if !rigFileNonEmpty(venvPy) {
				return false
			}
			cmd := exec.Command(venvPy, "-c", "import pytest")
			cmd.Dir = workDir
			return cmd.Run() == nil
		}
	}
	return true
}

func rigFileNonEmpty(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var b [1]byte
	_, err = f.Read(b[:])
	return err == nil
}
