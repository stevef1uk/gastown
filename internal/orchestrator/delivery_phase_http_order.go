package orchestrator

import (
	"path/filepath"
	"strings"
)

type deliveryPhaseScored struct {
	phase DeliveryPhase
	score int
	web   bool
	http  bool
}

// reorderDeliveryPhasesWebBeforeHTTPHandlers moves delivery phases whose paths are
// all web assets ahead of phases that include HTTP handler production files.
// Spec-index LLMs often emit api-handlers before web-static; handler unit tests need web/ on disk.
func reorderDeliveryPhasesWebBeforeHTTPHandlers(v WorkflowValidation) WorkflowValidation {
	if len(v.DeliveryPhases) < 2 {
		return v
	}
	items := make([]deliveryPhaseScored, 0, len(v.DeliveryPhases))
	for _, p := range v.DeliveryPhases {
		if len(p.RequiredFiles) == 0 {
			items = append(items, deliveryPhaseScored{phase: p, score: 999})
			continue
		}
		minScore := 999
		webOnly := true
		hasHTTP := false
		for _, f := range p.RequiredFiles {
			f = filepath.ToSlash(strings.TrimSpace(f))
			if f == "" {
				continue
			}
			s := implementationPathScore(f)
			if s < minScore {
				minScore = s
			}
			if !strings.Contains(f, "/web/") {
				webOnly = false
			}
			if IsHTTPHandlerImplementPath(f) {
				hasHTTP = true
			}
		}
		items = append(items, deliveryPhaseScored{phase: p, score: minScore, web: webOnly, http: hasHTTP})
	}
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if deliveryPhaseOrderLess(items[j], items[i]) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
	out := make([]DeliveryPhase, len(items))
	for i := range items {
		out[i] = items[i].phase
	}
	v.DeliveryPhases = out
	return v
}

func deliveryPhaseOrderLess(a, b deliveryPhaseScored) bool {
	// Web-only phases before any phase that ships HTTP handler production code.
	if a.web && !a.http && b.http {
		return true
	}
	if b.web && !b.http && a.http {
		return false
	}
	if a.score != b.score {
		return a.score < b.score
	}
	return strings.TrimSpace(a.phase.ID) < strings.TrimSpace(b.phase.ID)
}
