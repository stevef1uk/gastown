package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ArtifactAssessment is deliberately independent of phase acceptance. A failed
// build, unavailable tool, or missing prerequisite is not evidence that a
// previously completed file needs rewriting.
type ArtifactAssessment struct {
	Status ArtifactStatus
	Reason string
}

type ArtifactStatus string

const (
	ArtifactReady       ArtifactStatus = "ready"
	ArtifactMissing     ArtifactStatus = "missing"
	ArtifactInvalid     ArtifactStatus = "invalid"
	ArtifactUnavailable ArtifactStatus = "unavailable"
)

func (a ArtifactAssessment) NeedsRepair() bool {
	return a.Status == ArtifactMissing || a.Status == ArtifactInvalid
}

func (a ArtifactAssessment) Err() error {
	if a.Status == ArtifactReady {
		return nil
	}
	return fmt.Errorf("%s", a.Reason)
}

// AssessImplementationArtifact is the shared disk contract for close guards,
// planning recovery, and reconciliation. It never executes a phase command.
func AssessImplementationArtifact(rigDir, beadPath string, v WorkflowValidation) ArtifactAssessment {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	result := func(status ArtifactStatus, reason string) ArtifactAssessment {
		return ArtifactAssessment{Status: status, Reason: "artifact " + beadPath + ": " + reason}
	}
	if beadPath == "" {
		return result(ArtifactInvalid, "empty bead path")
	}
	path := filepath.Join(rigDir, filepath.FromSlash(beadPath))
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return result(ArtifactMissing, "missing")
	}
	if err != nil {
		return result(ArtifactUnavailable, err.Error())
	}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return result(ArtifactUnavailable, err.Error())
		}
		if len(entries) == 0 {
			return result(ArtifactInvalid, "empty directory")
		}
		return result(ArtifactReady, "directory present")
	}
	if IsPlaceholderFile(beadPath) {
		return result(ArtifactReady, "placeholder present")
	}
	if info.Size() == 0 {
		return result(ArtifactInvalid, "empty file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return result(ArtifactUnavailable, err.Error())
	}
	if err := CheckContentNotStub(data, beadPath, optsForPath(beadPath, StubCheckOptionsFromValidation(v.ForActivePhase()))); err != nil {
		return result(ArtifactInvalid, err.Error())
	}
	if err := CheckPythonSourceValid(data, beadPath); err != nil {
		return result(ArtifactInvalid, err.Error())
	}
	if strings.HasSuffix(strings.ToLower(beadPath), ".js") {
		if err := CheckJavaScriptFileHealthy(data); err != nil {
			return result(ArtifactInvalid, err.Error())
		}
	}
	if owned := assessOwnedTests(rigDir, beadPath, v); owned.Status != ArtifactReady {
		return owned
	}
	return result(ArtifactReady, "disk checks passed")
}
