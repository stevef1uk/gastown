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
	v = autoCorrectSpecIndexLayout(v, rig, filepath.Join(townRoot, rig, "mayor", "rig"))
	return ClampProfileValidationForRig(townRoot, rig, v), true, nil
}

// layoutCmdTarget renders the "cd <target>" argument for a layout root; "." stays "."
// and an empty root maps to "." so verify commands never `cd ""`.
func layoutCmdTarget(target string) string {
	if target == "" {
		return "."
	}
	return target
}

// autoCorrectSpecIndexLayout fixes the common spec-index bug where layout_root
// equals the rig name on flat mayor/rig worktrees. When all required_files share
// a common prefix matching the rig name, the project is at rig root (layout_root = ".")
// It also rejects the literal placeholder "layout_root" (the JSON field name echoed
// as its own value) — a common judge hallucination — and remaps it onto the SPEC's
// declared layout root (e.g. pingapp) or the rig root when the SPEC is flat.
func autoCorrectSpecIndexLayout(v WorkflowValidation, rig, mayorRigDir string) WorkflowValidation {
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" || layout == "." {
		return v
	}
	// Judge hallucination: layout_root literally equals the JSON key. Remap the
	// bogus "layout_root/" prefix onto the SPEC's real layout root.
	if layout == "layout_root" {
		target := ""
		if mayorRigDir != "" {
			if specPaths, ok := extractSpecLayoutPaths(mayorRigDir); ok {
				target = inferLayoutRootFromPaths(specPaths)
			}
		}
		if target == "" || target == "." || target == "layout_root" {
			target = ""
		}
		v.LayoutRoot = target
		if target == "" {
			v.LayoutRoot = "."
		}
		v.RequiredFiles = remapLayoutRootPlaceholderPaths(v.RequiredFiles, target)
		for i := range v.DeliveryPhases {
			v.DeliveryPhases[i].RequiredFiles = remapLayoutRootPlaceholderPaths(v.DeliveryPhases[i].RequiredFiles, target)
			if q := strings.TrimSpace(v.DeliveryPhases[i].QAVerifyCommand); q != "" {
				lower := strings.ToLower(q)
				if strings.HasPrefix(lower, "cd layout_root") {
					v.DeliveryPhases[i].QAVerifyCommand = strings.Replace(q, "cd layout_root", layoutCmdTarget(target), 1)
				} else if !strings.Contains(lower, "cd ") && target != "" && target != "." {
					v.DeliveryPhases[i].QAVerifyCommand = "cd " + target + " && " + q
				}
			}
		}
		if q := strings.TrimSpace(v.QAVerifyCommand); q != "" {
			lower := strings.ToLower(q)
			if strings.HasPrefix(lower, "cd layout_root") {
				v.QAVerifyCommand = strings.Replace(q, "cd layout_root", layoutCmdTarget(target), 1)
			} else if !strings.Contains(lower, "cd ") && target != "" && target != "." {
				v.QAVerifyCommand = "cd " + target + " && " + q
			}
		}
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
