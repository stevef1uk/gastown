package orchestrator

import (
	"path/filepath"
	"strings"
)

// reorderDeliveryPhasesWebAfterBackend moves web-only delivery phases that appear
// before the first backend phase (HTTP handler or server main) to just after the last
// backend phase. Web frontend depends on backend API handlers and server, so backend
// must be implemented first. Other phase order (store before handlers, docker last) is preserved.
func reorderDeliveryPhasesWebAfterBackend(v WorkflowValidation) WorkflowValidation {
	phases := v.DeliveryPhases
	if len(phases) < 2 {
		return v
	}
	firstBackend := firstBackendPhaseIndex(phases)
	if firstBackend < 0 {
		return v
	}
	lastBackend := lastBackendPhaseIndex(phases)
	if lastBackend < 0 {
		return v
	}
	var webEarly []DeliveryPhase
	rest := make([]DeliveryPhase, 0, len(phases))
	for i, p := range phases {
		if isWebOnlyDeliveryPhase(p) && i < firstBackend {
			webEarly = append(webEarly, p)
			continue
		}
		rest = append(rest, p)
	}
	if len(webEarly) == 0 {
		return v
	}
	firstBackend = firstBackendPhaseIndex(rest)
	lastBackend = lastBackendPhaseIndex(rest)
	if firstBackend < 0 || lastBackend < 0 {
		return v
	}
	out := append(append([]DeliveryPhase{}, rest[:lastBackend+1]...), webEarly...)
	out = append(out, rest[lastBackend+1:]...)
	v.DeliveryPhases = out
	return v
}

// firstBackendPhaseIndex returns the index of the first phase with Go backend code (handler or server).
func firstBackendPhaseIndex(phases []DeliveryPhase) int {
	for i, p := range phases {
		if deliveryPhaseHasGoBackendCode(p) {
			return i
		}
	}
	return -1
}

// lastBackendPhaseIndex returns the index of the last phase with Go backend code.
func lastBackendPhaseIndex(phases []DeliveryPhase) int {
	idx := -1
	for i, p := range phases {
		if deliveryPhaseHasGoBackendCode(p) {
			idx = i
		}
	}
	return idx
}

// deliveryPhaseHasGoBackendCode reports whether a phase contains Go source files
// that are not go.mod and not under /web/ (i.e., backend code like handlers, server, store).
func deliveryPhaseHasGoBackendCode(p DeliveryPhase) bool {
	for _, f := range p.RequiredFiles {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" {
			continue
		}
		if strings.HasSuffix(f, "go.mod") {
			continue
		}
		if strings.Contains(f, "/web/") {
			continue
		}
		if strings.HasSuffix(f, ".go") {
			return true
		}
	}
	return false
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
