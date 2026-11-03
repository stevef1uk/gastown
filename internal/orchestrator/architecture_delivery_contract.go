package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// ValidateArchitectureStructure rejects ambiguous machine-consumed sections.
// Appending another "canonical" copy must not silently leave the first in force.
func ValidateArchitectureStructure(doc string) error {
	seen := map[string]bool{}
	fenced := false
	var previousHeading string
	for _, line := range strings.Split(doc, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fenced = !fenced
			previousHeading = ""
			continue
		}
		if fenced || line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if previousHeading != "" {
				// A subsection is valid content; a sibling/parent is not.
				if strings.Index(line, " ") <= strings.Index(previousHeading, " ") {
					return fmt.Errorf("architecture.md has empty heading %q; amend it in place", previousHeading)
				}
			}
			previousHeading = line
			key := strings.ToLower(line)
			switch key {
			case "## delivery phases", "## requirements", "## planned file layout", "## file layout":
				if seen[key] {
					return fmt.Errorf("architecture.md has duplicate %q sections; reconcile one canonical section instead of appending amendments", line)
				}
				seen[key] = true
			}
		} else {
			previousHeading = ""
		}
	}
	if previousHeading != "" {
		return fmt.Errorf("architecture.md has empty heading %q", previousHeading)
	}
	_, _, err := architectureDeliveryContract(doc)
	return err
}

// ValidateArchitectureDeliveryContract previews the explicit profile without
// writing it. Design completion must see the same defects as design review.
func ValidateArchitectureDeliveryContract(townRoot, rig string, v WorkflowValidation) error {
	dir := filepath.Join(townRoot, rig, "mayor", "rig")
	doc, err := os.ReadFile(filepath.Join(dir, "architecture.md"))
	if err != nil {
		return err
	}
	phases, explicit, err := architectureDeliveryContract(string(doc))
	if err != nil || !explicit {
		return err
	}
	v.DeliveryPhases = phases
	v.RequiredFiles, _, _ = extractSpecLayoutPaths(dir)
	for _, p := range phases {
		v.RequiredFiles = append(v.RequiredFiles, p.RequiredFiles...)
	}
	v.ExplicitDeliveryContract = true
	v.QAVerifyCommand = phases[len(phases)-1].QAVerifyCommand
	if defect := ValidateRigWorkflowProfileForQA(townRoot, rig, v); defect != "" {
		return fmt.Errorf("architecture delivery contract: %s", defect)
	}
	return nil
}

// ArchitectureRequirementPhaseIDs uses the new contract's IDs during rework,
// rather than forcing an architect to retain obsolete generated profile IDs.
func ArchitectureRequirementPhaseIDs(doc string, fallback WorkflowValidation) ([]string, error) {
	phases, explicit, err := architectureDeliveryContract(doc)
	if err != nil {
		return nil, err
	}
	if !explicit {
		phases = fallback.DeliveryPhases
	}
	ids := make([]string, 0, len(phases))
	for _, p := range phases {
		ids = append(ids, p.ID)
	}
	return ids, nil
}

// A fenced delivery_phases block uses the existing DeliveryPhase schema. Unlike
// prose exit criteria, it transfers file ownership, dependencies and commands
// together, without guessing shell commands from surrounding explanatory text.
func architectureDeliveryContract(doc string) ([]DeliveryPhase, bool, error) {
	lower := strings.ToLower(doc)
	start := strings.Index(lower, "## delivery phases")
	if start < 0 {
		return nil, false, nil
	}
	section := extractMarkdownSection(doc, start)
	parts := strings.Split(section, "```")
	for i := 1; i < len(parts); i += 2 {
		_, body, ok := strings.Cut(parts[i], "\n")
		if !ok || !strings.Contains(body, "delivery_phases:") {
			continue
		}
		var contract struct {
			DeliveryPhases []DeliveryPhase `yaml:"delivery_phases"`
		}
		decoder := yaml.NewDecoder(strings.NewReader(body))
		decoder.KnownFields(true)
		if err := decoder.Decode(&contract); err != nil {
			return nil, true, fmt.Errorf("architecture delivery contract: %w", err)
		}
		seen := map[string]bool{}
		for _, p := range contract.DeliveryPhases {
			if strings.TrimSpace(p.ID) == "" || seen[p.ID] || len(p.RequiredFiles) == 0 || strings.TrimSpace(p.QAVerifyCommand) == "" || isNoOpVerifyCommand(p.QAVerifyCommand) {
				return nil, true, fmt.Errorf("architecture delivery contract: phase %q needs a unique ID, concrete required_files and an executable qa_verify_command", p.ID)
			}
			seen[p.ID] = true
			for _, f := range p.RequiredFiles {
				if !isImplementableFilePath(f) || !IsValidImplementBeadPath(f) {
					return nil, true, fmt.Errorf("architecture delivery contract: invalid deliverable %q", f)
				}
			}
		}
		if len(contract.DeliveryPhases) == 0 {
			return nil, true, fmt.Errorf("architecture delivery contract has no delivery_phases")
		}
		for _, p := range contract.DeliveryPhases {
			for _, dep := range p.DependsOn {
				if !seen[dep] || dep == p.ID {
					return nil, true, fmt.Errorf("architecture delivery contract: phase %q has invalid dependency %q", p.ID, dep)
				}
			}
		}
		return contract.DeliveryPhases, true, nil
	}
	return nil, false, nil
}

func reconcileDeliveryContractProgress(v *WorkflowValidation, previous WorkflowValidation) {
	old := map[string]DeliveryPhase{}
	for _, p := range previous.DeliveryPhases {
		old[p.ID] = p
	}
	var completed []string
	for _, id := range previous.CompletedPhaseIDsField {
		for _, p := range v.DeliveryPhases {
			prior, ok := old[id]
			if p.ID == id && ok && reflect.DeepEqual(prior.RequiredFiles, p.RequiredFiles) && prior.QAVerifyCommand == p.QAVerifyCommand && reflect.DeepEqual(prior.DependsOn, p.DependsOn) {
				completed = append(completed, id)
			}
		}
	}
	v.CompletedPhaseIDsField = completed
	if len(completed) != len(previous.CompletedPhaseIDsField) {
		for _, p := range v.DeliveryPhases {
			if !containsString(completed, p.ID) {
				v.ActivePhaseIDField = p.ID
				break
			}
		}
	}
}
