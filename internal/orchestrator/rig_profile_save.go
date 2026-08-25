package orchestrator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
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

// ResetRigPhaseForNewWorkflow resets delivery-phase progress in workflow-profile.json so a
// newly started workflow begins at the first phase needing work instead of inheriting the
// previous workflow's active/completed phase state (which previously fast-forwarded new
// workflows straight to the final phase). No-op when the profile has no delivery phases.
func ResetRigPhaseForNewWorkflow(townRoot, rig string) error {
	if townRoot == "" || rig == "" {
		return nil
	}
	path := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir, rigProfileFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no profile yet — nothing to reset
		}
		return fmt.Errorf("read rig profile %s: %w", path, err)
	}
	var env rigProfileEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("decode rig profile %s: %w", path, err)
	}
	if len(env.Validation.DeliveryPhases) == 0 {
		return nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	env.Validation.ActivePhaseIDField = ResolveActivePhaseFromDisk(rigDir, env.Validation)
	env.Validation.CompletedPhaseIDsField = nil
	env.Validation.RewoundFromPhaseIDField = ""
	env.TestPlanReviewed = false
	env.TestPlanFrozen = false
	return SaveRigWorkflowProfileEnvelope(townRoot, rig, env)
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
	if len(env.Validation.DeliveryPhases) > 0 {
		if env.Validation.ActivePhaseID() == "" {
			env.Validation.ActivePhaseIDField = strings.TrimSpace(env.Validation.DeliveryPhases[0].ID)
		} else if len(env.Validation.CompletedPhaseIDsField) == 0 {
			// No completed_phase_ids tracking available (e.g. profile from
			// before the tracking feature). Resolve active phase from disk
			// files: the first phase with missing files needs work.
			rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
			if diskPhase := ResolveActivePhaseFromDisk(rigDir, env.Validation); diskPhase != "" {
				env.Validation.ActivePhaseIDField = diskPhase
			}
		}
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

// SetRigRewoundFromPhase sets rewound_from_phase_id in workflow-profile.json.
// This marks the phase the workflow was rewound FROM so advancement can jump
// back to it directly instead of progressing sequentially through intermediates.
func SetRigRewoundFromPhase(townRoot, rig, phaseID string) error {
	if rig == "" || townRoot == "" {
		return fmt.Errorf("town root and rig name required")
	}
	phaseID = strings.TrimSpace(phaseID)
	if phaseID == "" {
		return nil
	}
	path := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir, rigProfileFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read rig profile %s: %w", path, err)
	}
	var env rigProfileEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("decode rig profile: %w", err)
	}
	env.Validation.RewoundFromPhaseIDField = phaseID
	return SaveRigWorkflowProfileEnvelope(townRoot, rig, env)
}

// AddRigCompletedPhase appends a phase id to the completed_phase_ids list in workflow-profile.json.
func AddRigCompletedPhase(townRoot, rig, phaseID string) error {
	if rig == "" || townRoot == "" {
		return fmt.Errorf("town root and rig name required")
	}
	phaseID = strings.TrimSpace(phaseID)
	if phaseID == "" {
		return nil
	}
	path := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir, rigProfileFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read rig profile %s: %w", path, err)
	}
	var env rigProfileEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("decode rig profile: %w", err)
	}
	for _, c := range env.Validation.CompletedPhaseIDsField {
		if c == phaseID {
			return nil // already recorded
		}
	}
	env.Validation.CompletedPhaseIDsField = append(env.Validation.CompletedPhaseIDsField, phaseID)
	return SaveRigWorkflowProfileEnvelope(townRoot, rig, env)
}

// ClearRigRewoundFromPhase clears rewound_from_phase_id so normal sequential
// advancement resumes on future phase completions.
func ClearRigRewoundFromPhase(townRoot, rig string) error {
	if rig == "" || townRoot == "" {
		return fmt.Errorf("town root and rig name required")
	}
	path := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir, rigProfileFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil // file missing — nothing to clear
	}
	var env rigProfileEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil // invalid JSON — nothing to clear
	}
	if env.Validation.RewoundFromPhaseIDField == "" {
		return nil
	}
	env.Validation.RewoundFromPhaseIDField = ""
	return SaveRigWorkflowProfileEnvelope(townRoot, rig, env)
}

// SetTestPlanReviewed marks TEST_PLAN.md as reviewed in workflow-profile.json.
// Once set, the tester agent skips re-validating TEST_PLAN.md on subsequent
// workflow entries to test_plan state.
func SetTestPlanReviewed(townRoot, rig string, reviewed bool) error {
	if townRoot == "" || rig == "" {
		return nil
	}
	path := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir, rigProfileFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read rig profile %s: %w", path, err)
	}
	var env rigProfileEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("decode rig profile %s: %w", path, err)
	}
	if env.TestPlanReviewed == reviewed {
		return nil // no change needed
	}
	env.TestPlanReviewed = reviewed
	// Freeze TEST_PLAN.md when it's first validated to prevent rewrites.
	if reviewed {
		env.TestPlanFrozen = true
	}
	return SaveRigWorkflowProfileEnvelope(townRoot, rig, env)
}

// SetTestPlanFrozen marks TEST_PLAN.md as frozen in workflow-profile.json.
// Once frozen, the tester cannot rewrite TEST_PLAN.md (plan_gap rework is blocked).
func SetTestPlanFrozen(townRoot, rig string, frozen bool) error {
	if townRoot == "" || rig == "" {
		return nil
	}
	path := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir, rigProfileFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read rig profile %s: %w", path, err)
	}
	var env rigProfileEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("decode rig profile %s: %w", path, err)
	}
	if env.TestPlanFrozen == frozen {
		return nil // no change needed
	}
	env.TestPlanFrozen = frozen
	return SaveRigWorkflowProfileEnvelope(townRoot, rig, env)
}

// IsTestPlanFrozen returns true if TEST_PLAN.md is frozen and cannot be rewritten.
func IsTestPlanFrozen(townRoot, rig string) bool {
	if townRoot == "" || rig == "" {
		return false
	}
	path := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir, rigProfileFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var env rigProfileEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return false
	}
	return env.TestPlanFrozen
}

// IsTestPlanReviewed returns true if TEST_PLAN.md has already been reviewed
// for this rig and the result is persisted in workflow-profile.json.
func IsTestPlanReviewed(townRoot, rig string) bool {
	if townRoot == "" || rig == "" {
		return false
	}
	path := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir, rigProfileFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var env rigProfileEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return false
	}
	return env.TestPlanReviewed
}

// WriteRigWorkflowProfile writes a full profile envelope (used by spec-index),
// clamping the validation first.
func WriteRigWorkflowProfile(townRoot, rig string, v WorkflowValidation, source, confidence string) error {
	return WriteRigWorkflowProfileClamped(townRoot, rig, v, source, confidence, true)
}

// WriteRigWorkflowProfileClamped writes a full profile envelope. When clamp is
// true, the validation is passed through ClampProfileValidationForRig first;
// when false, the validation is written as-is (used to preserve JUDGE-enhanced
// verify commands that clamping would otherwise reset).
func WriteRigWorkflowProfileClamped(townRoot, rig string, v WorkflowValidation, source, confidence string, clamp bool) error {
	outDir := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return err
	}
	if clamp {
		v = ClampProfileValidationForRig(townRoot, rig, NormalizeLayoutProfile(v))
	}

	// Preserve phase progress and test_plan_reviewed flag from existing profile
	// so spec-index --force doesn't reset workflow state.
	var existingTestPlanReviewed bool
	existingV, _, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err == nil {
		if existingV.ActivePhaseIDField != "" {
			v.ActivePhaseIDField = existingV.ActivePhaseIDField
		}
		if len(existingV.CompletedPhaseIDsField) > 0 {
			v.CompletedPhaseIDsField = existingV.CompletedPhaseIDsField
		}
	}
	// Read the raw envelope to preserve test_plan_reviewed and test_plan_frozen (not in WorkflowValidation).
	existingPath := filepath.Join(outDir, rigProfileFile)
	var existingTestPlanFrozen bool
	if existingData, rerr := os.ReadFile(existingPath); rerr == nil {
		var existingEnv rigProfileEnvelope
		if json.Unmarshal(existingData, &existingEnv) == nil {
			existingTestPlanReviewed = existingEnv.TestPlanReviewed
			existingTestPlanFrozen = existingEnv.TestPlanFrozen
		}
	}
	// Guard: when regenerated phase IDs invalidate an existing TEST_PLAN.md,
	// AUTOMATICALLY realign its section headings to the new phases (bodies
	// preserved; target inferred from where each section's test files live).
	// Only sections that cannot be confidently mapped leave the plan stale —
	// those unfreeze so the Tester rewrites them against the listed IDs.
	if existingTestPlanReviewed || existingTestPlanFrozen {
		planDir := filepath.Join(townRoot, rig, "mayor", "rig")
		planPath := filepath.Join(planDir, "TEST_PLAN.md")
		if data, err := os.ReadFile(planPath); err == nil &&
			len(strings.TrimSpace(string(data))) > 0 {
			stale := func() bool {
				valid := map[string]bool{}
				for _, p := range v.DeliveryPhases {
					valid[strings.ToLower(p.ID)] = true
				}
				for _, line := range strings.Split(string(data), "\n") {
					line = strings.TrimSpace(line)
					if !strings.HasPrefix(line, "### ") {
						continue
					}
					id := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "### ")))
					if id != "" && !valid[id] {
						return true
					}
				}
				return false
			}
			if stale() {
				res := alignTestPlanSectionIDs(townRoot, rig, v)
				freshData, _ := os.ReadFile(planPath)
				stillStale := func() bool {
					valid := map[string]bool{}
					for _, p := range v.DeliveryPhases {
						valid[strings.ToLower(p.ID)] = true
					}
					for _, line := range strings.Split(string(freshData), "\n") {
						line = strings.TrimSpace(line)
						if !strings.HasPrefix(line, "### ") {
							continue
						}
						id := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "### ")))
						if id != "" && !valid[id] {
							return true
						}
					}
					return false
				}
				if stillStale() {
					log.Printf("[spec-index] %s: TEST_PLAN partially aligned (%d relabeled); remaining sections unfrozen for Tester rewrite", rig, res.renamed)
					existingTestPlanReviewed = false
					existingTestPlanFrozen = false
				} else {
					log.Printf("[spec-index] TEST_PLAN.md auto-aligned to regenerated phases for %s", rig)
					existingTestPlanFrozen = false // fresh alignment ⇒ cheap re-review
				}
			}
		}
	}

	env := rigProfileEnvelope{
		Version:          1,
		GeneratedAt:      time.Now().UTC().Format(time.RFC3339),
		Source:           source,
		Confidence:       confidence,
		Validation:       v,
		TestPlanReviewed: existingTestPlanReviewed,
		TestPlanFrozen:   existingTestPlanFrozen,
	}
	// Resolve active phase from disk only if not already set (e.g. first write).
	// This overrides whatever the LLM or ClampProfileValidation set, ensuring
	// the active phase always points to the phase that actually needs work.
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if v.ActivePhaseIDField == "" {
		if diskPhase := ResolveActivePhaseFromDisk(rigDir, env.Validation); diskPhase != "" {
			env.Validation.ActivePhaseIDField = diskPhase
		}
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
