package orchestrator

import (
	"fmt"
	"path/filepath"
	"strings"
)

const maxDeliveryPhaseFiles = 10

// deliveryGraphProblems runs without LLM calls and preserves explicit milestones.
func deliveryGraphProblems(v WorkflowValidation) []string {
	var problems []string
	phases := map[string]DeliveryPhase{}
	for _, p := range v.DeliveryPhases {
		files := make(map[string]struct{}, len(p.RequiredFiles))
		for _, file := range p.RequiredFiles {
			file = filepath.ToSlash(filepath.Clean(strings.TrimSpace(file)))
			if file == "." || file == "" {
				continue
			}
			file = normalizeDeliveryOwnershipPath(file, v.LayoutRoot)
			files[file] = struct{}{}
		}
		if len(files) > maxDeliveryPhaseFiles {
			problems = append(problems, fmt.Sprintf("phase %q declares %d files; maximum is %d", p.ID, len(files), maxDeliveryPhaseFiles))
		}
		if p.ID == "" {
			problems = append(problems, "delivery phase has an empty ID")
			continue
		}
		if _, exists := phases[p.ID]; exists {
			problems = append(problems, fmt.Sprintf("duplicate delivery phase %q", p.ID))
		}
		phases[p.ID] = p
	}
	visited := map[string]int{}
	var visit func(string)
	visit = func(id string) {
		if visited[id] == 1 {
			problems = append(problems, fmt.Sprintf("delivery dependency cycle at %q", id))
			return
		}
		if visited[id] == 2 {
			return
		}
		visited[id] = 1
		for _, dep := range phases[id].DependsOn {
			if _, exists := phases[dep]; !exists {
				problems = append(problems, fmt.Sprintf("phase %q depends on unknown phase %q", id, dep))
				continue
			}
			visit(dep)
		}
		visited[id] = 2
	}
	for _, p := range v.DeliveryPhases {
		visit(p.ID)
	}
	return problems
}
