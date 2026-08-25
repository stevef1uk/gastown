package orchestrator

import (
	"testing"
)

// Regression: an LLM-authored profile listed "linkshelf/dockerfile" (lowercase)
// — Polecat wrote that file, but `build: .` needs "Dockerfile", deadlocking the
// bead on verify-before-close. SanitizeRigFlowProfile must normalize casing for
// known filenames in both the union and per-phase required_files.
func TestSanitizeRigFlowProfile_normalizesLLMFilenameCasing(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/go.mod", "linkshelf/dockerfile"},
		DeliveryPhases: []DeliveryPhase{{
			ID:              "integration-and-deployment",
			RequiredFiles:   []string{"linkshelf/go.mod", "linkshelf/dockerfile"},
			QAVerifyCommand: "cd linkshelf && docker-compose build && docker-compose up --exit-code-from playwright",
		}},
	}

	got := SanitizeRigFlowProfile(v)

	for _, files := range [][]string{got.RequiredFiles, got.DeliveryPhases[0].RequiredFiles} {
		found := false
		for _, f := range files {
			if f == "linkshelf/Dockerfile" {
				found = true
			}
			if f == "linkshelf/dockerfile" {
				t.Fatalf("lowercase dockerfile survived sanitize: %v", files)
			}
		}
		if !found {
			t.Fatalf("missing linkshelf/Dockerfile after normalization: %v", files)
		}
	}
}
