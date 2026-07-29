package specprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

	endpoint, model := ResolveLLMForSpecIndex(townRoot)
	v, confidence, err := indexSpecContent(ctx, endpoint, model, string(data))
	if err != nil {
		return nil, err
	}

	// One-time JUDGE pass: review phase verify commands via LLM.
	// Non-fatal — if the LLM fails, the deterministic commands still apply.
	v = JudgePhaseVerifyCommands(ctx, endpoint, model, v)

	f := ProfileFile{
		Version:     1,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Source:      "llm",
		Confidence:  confidence,
		Validation:  v,
	}
	if err := orchestrator.WriteRigWorkflowProfile(townRoot, rig, f.Validation, f.Source, f.Confidence); err != nil {
		return nil, err
	}

	path := ProfilePath(townRoot, rig)
	// Re-read so returned ProfileFile matches sanitized on-disk validation.
	raw, err := os.ReadFile(path)
	if err != nil {
		return &f, nil
	}
	_ = json.Unmarshal(raw, &f)
	return &f, nil
}

func indexSpecContent(ctx context.Context, endpoint, model, spec string) (orchestrator.WorkflowValidation, string, error) {
	chunks := splitSpecIntoChunks(spec)
	if len(chunks) == 1 {
		return LLMExtractProfile(ctx, endpoint, model, chunks[0])
	}
	var parts []orchestrator.WorkflowValidation
	confidence := "medium"
	for i, chunk := range chunks {
		v, conf, err := LLMExtractProfile(ctx, endpoint, model, chunk)
		if err != nil {
			return orchestrator.WorkflowValidation{}, "", fmt.Errorf("chunk %d/%d: %w", i+1, len(chunks), err)
		}
		parts = append(parts, v)
		if conf == "low" {
			confidence = "low"
		} else if conf == "high" && confidence != "low" {
			confidence = "high"
		}
	}
	return mergeIndexedProfiles(parts), confidence, nil
}
