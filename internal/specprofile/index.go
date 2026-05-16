package specprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// ProfileFile is the on-disk JSON format (includes provenance).
type ProfileFile struct {
	Version     int                            `json:"version"`
	GeneratedAt string                         `json:"generated_at"`
	Source      string                         `json:"source"`
	Confidence  string                         `json:"confidence"`
	Validation  orchestrator.WorkflowValidation `json:"validation"`
}

// IndexRig reads SPEC.md and writes workflow-profile.json using the LLM.
// On success it returns the profile that was written (for operator messaging).
func IndexRig(ctx context.Context, townRoot, rig string) (*ProfileFile, error) {
	specPath := SpecPath(townRoot, rig)
	data, err := os.ReadFile(specPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", specPath, err)
	}

	v, confidence, err := LLMExtractProfile(ctx, string(data))
	if err != nil {
		return nil, err
	}

	outDir := filepath.Join(townRoot, rig, "mayor", "rig", GastownMetaDir)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return nil, err
	}

	f := ProfileFile{
		Version:     1,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Source:      "llm",
		Confidence:  confidence,
		Validation:  v,
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}

	path := ProfilePath(townRoot, rig)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	return &f, nil
}
