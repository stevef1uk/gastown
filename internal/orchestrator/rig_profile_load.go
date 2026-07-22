package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const rigProfileDir = ".gastown"
const rigProfileFile = "workflow-profile.json"

type rigProfileEnvelope struct {
	Version     int                 `json:"version"`
	GeneratedAt string              `json:"generated_at"`
	Source      string              `json:"source"`
	Confidence  string              `json:"confidence"`
	Validation  WorkflowValidation  `json:"validation"`
}

// LoadRigWorkflowProfileFile reads {rig}/mayor/rig/.gastown/workflow-profile.json if present.
func LoadRigWorkflowProfileFile(townRoot, rig string) (WorkflowValidation, bool, error) {
	if rig == "" || townRoot == "" {
		return WorkflowValidation{}, false, nil
	}
	path := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir, rigProfileFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return WorkflowValidation{}, false, nil
		}
		return WorkflowValidation{}, false, fmt.Errorf("read rig profile %s: %w", path, err)
	}
	var env rigProfileEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return WorkflowValidation{}, false, fmt.Errorf("decode rig profile %s: %w", path, err)
	}
	v := NormalizeLayoutProfile(env.Validation)
	v = autoCorrectSpecIndexLayout(v, rig)
	return ClampProfileValidationForRig(townRoot, rig, v), true, nil
}

// autoCorrectSpecIndexLayout fixes the common spec-index bug where layout_root
// equals the rig name on flat mayor/rig worktrees. When all required_files share
// a common prefix matching the rig name, the project is at rig root (layout_root = ".")
func autoCorrectSpecIndexLayout(v WorkflowValidation, rig string) WorkflowValidation {
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" || layout == "." {
		return v
	}
	// Check if all required files share a common prefix equal to the rig name
	rigPrefix := commonPathPrefix(v.UnionRequiredFiles())
	if rigPrefix != "" && rigPrefix != "." && layout == rigPrefix && rigPrefix == rig {
		// Strip the rig prefix from all file paths
		v.RequiredFiles = stripLayoutPrefixFromPaths(v.RequiredFiles, rigPrefix)
		for i := range v.DeliveryPhases {
			v.DeliveryPhases[i].RequiredFiles = stripLayoutPrefixFromPaths(v.DeliveryPhases[i].RequiredFiles, rigPrefix)
		}
		// Fix qa_verify_command: replace "cd <rig>" with "cd .", or add "cd . &&" if missing
		if q := strings.TrimSpace(v.QAVerifyCommand); q != "" {
			lower := strings.ToLower(q)
			if strings.HasPrefix(lower, "cd "+rigPrefix) {
				v.QAVerifyCommand = strings.Replace(q, "cd "+rigPrefix, "cd .", 1)
			} else if !strings.Contains(lower, "cd .") && !strings.Contains(lower, "cd ") {
				v.QAVerifyCommand = "cd . && " + q
			}
		}
		for i := range v.DeliveryPhases {
			if q := strings.TrimSpace(v.DeliveryPhases[i].QAVerifyCommand); q != "" {
				lower := strings.ToLower(q)
				if strings.HasPrefix(lower, "cd "+rigPrefix) {
					v.DeliveryPhases[i].QAVerifyCommand = strings.Replace(q, "cd "+rigPrefix, "cd .", 1)
				} else if !strings.Contains(lower, "cd .") && !strings.Contains(lower, "cd ") {
					v.DeliveryPhases[i].QAVerifyCommand = "cd . && " + q
				}
			}
		}
		v.LayoutRoot = "."
	}
	return v
}

// commonPathPrefix returns the common first path component across all files,
// or "" if they don't share a single prefix.
func commonPathPrefix(files []string) string {
	var prefix string
	for _, f := range files {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" {
			continue
		}
		idx := strings.Index(f, "/")
		if idx <= 0 {
			return ""
		}
		first := f[:idx]
		if prefix == "" {
			prefix = first
		} else if prefix != first {
			return ""
		}
	}
	return prefix
}
