package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// RequiredOwnedTests resolves the approved test plan into outputs of an existing
// source bead. Explicit required_files test deliverables retain their own beads.
// It does not add files to required_files or manufacture new implementation work.
func RequiredOwnedTests(rigDir, source string, v WorkflowValidation) ([]string, error) {
	if IsTestImplementPath(source) || source == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(rigDir, ".gastown", "workflow-profile.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var envelope rigProfileEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("read approved test contract: %w", err)
	}
	if !envelope.TestPlanReviewed && !envelope.TestPlanFrozen {
		return nil, nil
	}
	data, err = os.ReadFile(TestPlanPath(rigDir))
	if err != nil {
		return nil, fmt.Errorf("approved TEST_PLAN.md is unavailable: %w", err)
	}
	scoped := v.ForActivePhase()
	source = filepath.ToSlash(filepath.Clean(source))
	var tests []string
	blocks := ParseTestPlanBlocks(string(data))
	if len(blocks) == 0 {
		return nil, fmt.Errorf("approved TEST_PLAN.md has no requirement/test mappings")
	}
	var activeBlocks []TestPlanBlock
	for _, block := range blocks {
		phase := strings.TrimSpace(block.Phase)
		if phase == "" {
			for _, declared := range scoped.DeliveryPhases {
				if strings.EqualFold(block.ReqID, declared.ID) {
					phase = declared.ID
					break
				}
			}
		}
		if scoped.HasPhasedDelivery() && phase != "" && !strings.EqualFold(phase, scoped.ActivePhaseID()) {
			continue
		}
		activeBlocks = append(activeBlocks, block)
		test := cleanPlannedTestFilePath(block.TestFile)
		if SourcePathForOwnedTest(test, scoped) != source {
			continue
		}
		independent := false
		for _, required := range scoped.RequiredFiles {
			independent = independent || filepath.ToSlash(filepath.Clean(required)) == test
		}
		if !independent {
			tests = appendUniquePath(tests, test)
		}
	}
	if err := ValidateTestPlanPaths(v, activeBlocks); err != nil {
		return nil, fmt.Errorf("approved test contract is invalid: %w", err)
	}
	return tests, nil
}

// assessOwnedTests uses Tester's assertion/stub rules, not the production file
// size threshold. A focused test must not be rejected just for being short.
func assessOwnedTests(rigDir, source string, v WorkflowValidation) ArtifactAssessment {
	tests, err := RequiredOwnedTests(rigDir, source, v)
	if err != nil {
		return ArtifactAssessment{Status: ArtifactUnavailable, Reason: err.Error()}
	}
	opts := StubCheckOptionsFromValidation(v)
	if opts.MinFileBytes > 160 {
		opts.MinFileBytes = 160
	}
	if opts.MinSubstantiveLines < 1 {
		opts.MinSubstantiveLines = 1
	}
	for _, test := range tests {
		data, err := os.ReadFile(filepath.Join(rigDir, filepath.FromSlash(test)))
		if os.IsNotExist(err) {
			return ArtifactAssessment{Status: ArtifactMissing, Reason: fmt.Sprintf("required test output %s for %s is missing (approved TEST_PLAN.md)", test, source)}
		}
		if err != nil {
			return ArtifactAssessment{Status: ArtifactUnavailable, Reason: err.Error()}
		}
		if err := CheckContentNotStub(data, test, optsForPath(test, opts)); err != nil {
			return ArtifactAssessment{Status: ArtifactInvalid, Reason: fmt.Sprintf("required test output %s: %v", test, err)}
		}
	}
	return ArtifactAssessment{Status: ArtifactReady}
}

// RequiredOwnedTestVerifyCommand never falls back to compile-only verification
// when approved tests are absent. Python commands target every owned test file;
// Go commands exercise the owning package, including its test companions.
func RequiredOwnedTestVerifyCommand(rigDir, source string, v WorkflowValidation) (string, error) {
	tests, err := RequiredOwnedTests(rigDir, source, v)
	if err != nil || len(tests) == 0 {
		return "", err
	}
	if strings.HasSuffix(source, ".py") {
		args := []string{shellQuoteComponentPath(v.PythonVenvRelDir() + "/bin/python3"), "-m pytest -v"}
		for _, test := range tests {
			args = append(args, shellQuoteComponentPath(test))
		}
		return strings.Join(args, " "), nil
	}
	if strings.HasSuffix(source, ".go") {
		return GoTestVerifyCommandForPackage(v, rigDir, source), nil
	}
	return "", fmt.Errorf("no test runner for required test outputs of %s", source)
}

// RunRequiredOwnedTests is also used by the final close guard, so a successful
// compileall/build or an unrelated green command cannot stand in for these tests.
func RunRequiredOwnedTests(rigDir, source string, v WorkflowValidation) error {
	if result := assessOwnedTests(rigDir, source, v); result.Status != ArtifactReady {
		return result.Err()
	}
	verify, err := RequiredOwnedTestVerifyCommand(rigDir, source, v)
	if err != nil || verify == "" {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", verify)
	cmd.Dir = rigDir
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("required tests for %s timed out: %s", source, verify)
		}
		return fmt.Errorf("required tests for %s failed (%s): %w\n%s", source, verify, err, out)
	}
	return nil
}

func ownedTestContractFailureCommand(err error) string {
	return "printf '%s\\n' " + shellQuoteComponentPath(err.Error()) + " >&2; exit 1"
}
