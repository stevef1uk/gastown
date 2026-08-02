package specprofile

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// DeterministicIndexRig creates a workflow profile from SPEC.md WITHOUT LLM.
// Uses SPEC layout tree + phases section for a hallucination-free profile.
// Falls back to LLM only when SPEC lacks a parseable layout tree.
func DeterministicIndexRig(ctx context.Context, townRoot, rig string) (*ProfileFile, error) {
	specPath := SpecPath(townRoot, rig)
	data, err := os.ReadFile(specPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", specPath, err)
	}
	spec := string(data)

	// Parse SPEC layout tree for required_files
	mayorRig := filepath.Join(townRoot, rig, "mayor", "rig")
	paths, hasTree := orchestrator.ProbeExtractSpecLayoutPaths(mayorRig)
	if !hasTree || len(paths) == 0 {
		return nil, fmt.Errorf("no parseable layout tree in SPEC — falling back to LLM")
	}

	// Parse phases from SPEC
	phases := parseSpecPhases(spec)
	if len(phases) == 0 {
		// Default phases from tree structure
		phases = defaultPhasesFromPaths(paths)
	}

	// Parse verify commands from SPEC
	verifyCmd := inferVerifyCommand(spec, paths)

	v := orchestrator.WorkflowValidation{
		LayoutRoot:         inferLayoutRoot(paths),
		BeadTitleContains:  "Implement " + inferLayoutRoot(paths) + "/",
		RequiredFiles:      paths,
		QAVerifyCommand:    verifyCmd,
		TestRunner:         inferTestRunner(paths),
		DeliveryPhases:     phases,
		ActivePhaseIDField: phases[0].ID,
	}

	// Clamp/validate
	v = orchestrator.ClampProfileValidation(v)
	v = orchestrator.SanitizeRigFlowProfile(v)

	f := ProfileFile{
		Version:     1,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Source:      "deterministic",
		Confidence:  "high",
		Validation:  v,
	}

	if err := orchestrator.WriteRigWorkflowProfile(townRoot, rig, f.Validation, f.Source, f.Confidence); err != nil {
		return nil, err
	}

	log.Printf("[deterministic-index] wrote profile for %s: %d files, %d phases", rig, len(paths), len(phases))
	return &f, nil
}

func parseSpecPhases(spec string) []orchestrator.DeliveryPhase {
	// Extract "## Phases" or "## Delivery Phases" section
	lower := strings.ToLower(spec)
	for _, marker := range []string{"## phases", "## delivery phases"} {
		i := strings.Index(lower, marker)
		if i < 0 {
			continue
		}
		section := spec[i:]
		if j := strings.Index(section[1:], "\n## "); j >= 0 {
			section = section[:1+j]
		}
		return parsePhaseList(section)
	}
	return nil
}

func parsePhaseList(section string) []orchestrator.DeliveryPhase {
	lines := strings.Split(section, "\n")
	var phases []orchestrator.DeliveryPhase
	var current *orchestrator.DeliveryPhase
	phaseNum := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "```") {
			continue
		}
		// Numbered list: "1. **phase-name** — description" or "- **phase-name**"
		if strings.Contains(trimmed, "**") && (strings.HasPrefix(trimmed, "-") || (len(trimmed) > 1 && trimmed[0] >= '1' && trimmed[0] <= '9')) {
			// Extract phase name from **...**
			start := strings.Index(trimmed, "**")
			end := strings.Index(trimmed[start+2:], "**")
			if start >= 0 && end >= 0 {
				name := trimmed[start+2 : start+2+end]
				phaseNum++
				current = &orchestrator.DeliveryPhase{
					ID:              slugify(name),
					Title:           name,
					RequiredFiles:   []string{},
					QAVerifyCommand: "",
					SpecFocus:       "",
				}
				phases = append(phases, *current)
			}
		} else if current != nil && (strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "*")) {
			// File entry under phase - skip for now (we use tree paths)
		}
	}
	return phases
}

func defaultPhasesFromPaths(paths []string) []orchestrator.DeliveryPhase {
	// Group by top-level directory
	dirs := map[string][]string{}
	for _, p := range paths {
		parts := strings.Split(p, "/")
		if len(parts) > 1 {
			dirs[parts[1]] = append(dirs[parts[1]], p)
		} else {
			dirs["root"] = append(dirs["root"], p)
		}
	}

	var phases []orchestrator.DeliveryPhase
	for dir, files := range dirs {
		if dir == "root" {
			phases = append(phases, orchestrator.DeliveryPhase{
				ID:              "setup",
				Title:           "Setup and Root Files",
				RequiredFiles:   files,
				QAVerifyCommand: "",
			})
		} else {
			phases = append(phases, orchestrator.DeliveryPhase{
				ID:              dir,
				Title:           strings.Title(dir) + " Layer",
				RequiredFiles:   files,
				QAVerifyCommand: "",
			})
		}
	}
	return phases
}

func inferVerifyCommand(spec string, paths []string) string {
	// Look for test commands in SPEC
	lower := strings.ToLower(spec)
	if strings.Contains(lower, "go test") {
		return "cd " + inferLayoutRoot(paths) + " && go test ./..."
	}
	if strings.Contains(lower, "pytest") {
		return "cd " + inferLayoutRoot(paths) + " && pytest"
	}
	if strings.Contains(lower, "npm test") {
		return "cd " + inferLayoutRoot(paths) + " && npm test"
	}
	return ""
}

func inferLayoutRoot(paths []string) string {
	for _, p := range paths {
		parts := strings.Split(p, "/")
		if len(parts) > 1 {
			return parts[0]
		}
	}
	return "."
}

func inferTestRunner(paths []string) string {
	for _, p := range paths {
		ext := strings.ToLower(filepath.Ext(p))
		if ext == ".py" {
			return "pytest"
		}
		if ext == ".go" {
			return "go"
		}
		if ext == ".js" || ext == ".ts" {
			return "npm"
		}
	}
	return "go"
}

func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "(", "")
	s = strings.ReplaceAll(s, ")", "")
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, ".", "-")
	return s
}
