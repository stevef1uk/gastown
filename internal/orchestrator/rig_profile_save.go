package orchestrator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// marshalRigProfileJSON writes workflow-profile.json without \\u0026 escapes for & and &&.
func marshalRigProfileJSON(env rigProfileEnvelope) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(env); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

// SaveRigWorkflowProfileEnvelope writes a clamped profile envelope to workflow-profile.json.
func SaveRigWorkflowProfileEnvelope(townRoot, rig string, env rigProfileEnvelope) error {
	if rig == "" || townRoot == "" {
		return fmt.Errorf("town root and rig name required")
	}
	env.Validation = ClampProfileValidationForRig(townRoot, rig, NormalizeLayoutProfile(env.Validation))
	path := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir, rigProfileFile)
	raw, err := marshalRigProfileJSON(env)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// NormalizeRigWorkflowProfile rewrites workflow-profile.json with ClampProfileValidation
// (e.g. Dockerfile/compose moved to the final delivery phase). Returns the normalized validation.
func NormalizeRigWorkflowProfile(townRoot, rig string) (WorkflowValidation, error) {
	if rig == "" || townRoot == "" {
		return WorkflowValidation{}, fmt.Errorf("town root and rig name required")
	}
	// Reconcile architecture.md paths into the profile before normalizing.
	ReconcileProfileWithArchitecture(townRoot, rig)
	path := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir, rigProfileFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return WorkflowValidation{}, fmt.Errorf("read rig profile %s: %w", path, err)
	}
	var env rigProfileEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return WorkflowValidation{}, fmt.Errorf("decode rig profile: %w", err)
	}
	if env.Validation.ActivePhaseID() == "" && len(env.Validation.DeliveryPhases) > 0 {
		env.Validation.ActivePhaseIDField = strings.TrimSpace(env.Validation.DeliveryPhases[0].ID)
	}
	if err := SaveRigWorkflowProfileEnvelope(townRoot, rig, env); err != nil {
		return WorkflowValidation{}, err
	}
	v, _, err := LoadRigWorkflowProfileFile(townRoot, rig)
	return v, err
}

// SetRigActivePhase updates active_phase_id in workflow-profile.json.
func SetRigActivePhase(townRoot, rig, phaseID string) error {
	if rig == "" || townRoot == "" {
		return fmt.Errorf("town root and rig name required")
	}
	phaseID = strings.TrimSpace(phaseID)
	path := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir, rigProfileFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read rig profile %s: %w", path, err)
	}
	var env rigProfileEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("decode rig profile: %w", err)
	}
	if !env.Validation.HasPhasedDelivery() {
		return fmt.Errorf("rig profile has no delivery_phases; run gt rig spec-index %s first", rig)
	}
	found := false
	for _, p := range env.Validation.DeliveryPhases {
		if strings.TrimSpace(p.ID) == phaseID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("unknown phase id %q (see delivery_phases in %s)", phaseID, path)
	}
	env.Validation.ActivePhaseIDField = phaseID
	return SaveRigWorkflowProfileEnvelope(townRoot, rig, env)
}

// WriteRigWorkflowProfile writes a full profile envelope (used by spec-index).
func WriteRigWorkflowProfile(townRoot, rig string, v WorkflowValidation, source, confidence string) error {
	outDir := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return err
	}
	env := rigProfileEnvelope{
		Version:     1,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Source:      source,
		Confidence:  confidence,
		Validation:  ClampProfileValidationForRig(townRoot, rig, NormalizeLayoutProfile(v)),
	}
	raw, err := marshalRigProfileJSON(env)
	if err != nil {
		return err
	}
	path := filepath.Join(outDir, rigProfileFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	_, _ = EnsureHTTPImplementationRigConfig(townRoot, rig, env.Validation)
	return nil
}
