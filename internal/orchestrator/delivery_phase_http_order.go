package orchestrator

import (
	"path/filepath"
	"strings"
)

// reorderDeliveryPhasesWebBeforeHTTPHandlers moves web-only delivery phases that appear
// after the first HTTP handler phase to just before that handler phase. Other phase order
// (store before main, docker in final phase, etc.) is preserved.
func reorderDeliveryPhasesWebBeforeHTTPHandlers(v WorkflowValidation) WorkflowValidation {
	phases := v.DeliveryPhases
	if len(phases) < 2 {
		return v
	}
	handlerIdx := firstHTTPHandlerPhaseIndex(phases)
	if handlerIdx < 0 {
		return v
	}
	var webLate []DeliveryPhase
	rest := make([]DeliveryPhase, 0, len(phases))
	for i, p := range phases {
		if isWebOnlyDeliveryPhase(p) && i > handlerIdx {
			webLate = append(webLate, p)
			continue
		}
		rest = append(rest, p)
	}
	if len(webLate) == 0 {
		return v
	}
	insertAt := firstHTTPHandlerPhaseIndex(rest)
	if insertAt < 0 {
		return v
	}
	out := append(append([]DeliveryPhase{}, rest[:insertAt]...), webLate...)
	out = append(out, rest[insertAt:]...)
	v.DeliveryPhases = out
	return v
}

func firstHTTPHandlerPhaseIndex(phases []DeliveryPhase) int {
	for i, p := range phases {
		if deliveryPhaseHasHTTPHandler(p) {
			return i
		}
	}
	return -1
}

func deliveryPhaseHasHTTPHandler(p DeliveryPhase) bool {
	for _, f := range p.RequiredFiles {
		if IsHTTPHandlerImplementPath(filepath.ToSlash(strings.TrimSpace(f))) {
			return true
		}
	}
	return false
}

func isWebOnlyDeliveryPhase(p DeliveryPhase) bool {
	if len(p.RequiredFiles) == 0 {
		return false
	}
	for _, f := range p.RequiredFiles {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" || !strings.Contains(f, "/web/") {
			return false
		}
	}
	return true
}
