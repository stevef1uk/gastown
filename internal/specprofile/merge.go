package specprofile

import (
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// mergeIndexedProfiles combines chunk-level extractions into one workflow profile.
func mergeIndexedProfiles(parts []orchestrator.WorkflowValidation) orchestrator.WorkflowValidation {
	if len(parts) == 0 {
		return orchestrator.WorkflowValidation{}
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		p := parts[i]
		if strings.TrimSpace(p.LayoutRoot) != "" && strings.TrimSpace(out.LayoutRoot) == "" {
			out.LayoutRoot = p.LayoutRoot
		}
		if strings.TrimSpace(p.BeadTitleContains) != "" && strings.TrimSpace(out.BeadTitleContains) == "" {
			out.BeadTitleContains = p.BeadTitleContains
		}
		if strings.TrimSpace(p.QAVerifyCommand) != "" && strings.TrimSpace(out.QAVerifyCommand) == "" {
			out.QAVerifyCommand = p.QAVerifyCommand
		}
		if strings.TrimSpace(p.TestRunner) != "" {
			out.TestRunner = p.TestRunner
		}
		if strings.TrimSpace(p.UnittestModule) != "" {
			out.UnittestModule = p.UnittestModule
		}
		if strings.TrimSpace(p.SpecSummary) != "" {
			if out.SpecSummary == "" {
				out.SpecSummary = p.SpecSummary
			} else if !strings.Contains(out.SpecSummary, p.SpecSummary) {
				out.SpecSummary += "\n\n" + p.SpecSummary
			}
		}
		if p.MinPlanBytes > out.MinPlanBytes {
			out.MinPlanBytes = p.MinPlanBytes
		}
		if p.MinArchitectureBytes > out.MinArchitectureBytes {
			out.MinArchitectureBytes = p.MinArchitectureBytes
		}
		if p.DevServerPort > 0 && out.DevServerPort == 0 {
			out.DevServerPort = p.DevServerPort
		}
		out.DeliveryPhases = appendPhaseList(out.DeliveryPhases, p.DeliveryPhases)
		out.RequiredFiles = unionPaths(out.RequiredFiles, p.RequiredFiles)
	}
	return orchestrator.ClampProfileValidation(orchestrator.NormalizeLayoutProfile(orchestrator.FinalizeDeliveryPhases(out)))
}

func appendPhaseList(base, add []orchestrator.DeliveryPhase) []orchestrator.DeliveryPhase {
	seen := make(map[string]bool)
	for _, p := range base {
		seen[strings.TrimSpace(p.ID)] = true
	}
	for _, p := range add {
		id := strings.TrimSpace(p.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		base = append(base, p)
	}
	return base
}

func unionPaths(a, b []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, list := range [][]string{a, b} {
		for _, f := range list {
			f = strings.TrimSpace(f)
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}
