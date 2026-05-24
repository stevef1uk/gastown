package main

import (
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func (r *stateRunner) implementBeadPathForCodeindex() string {
	if p := r.activeImplementBeadPath(); p != "" {
		return p
	}
	next, err := orchestrator.NextOpenImplementBead(r.townRoot, r.rig, r.v)
	if err != nil || next == nil {
		return ""
	}
	return orchestrator.NormalizeBeadPathForLayout(
		orchestrator.ExtractPathFromBeadTitle(next.Title, r.v.BeadTitleContains),
		r.v.LayoutRoot,
	)
}

func (r *stateRunner) logCodeindexInjectionForActiveBead() {
	if !strings.EqualFold(strings.TrimSpace(r.hooks.Track), "implementation") {
		return
	}
	beadPath := r.implementBeadPathForCodeindex()
	if beadPath == "" {
		return
	}
	mayorRig := filepath.Join(r.townRoot, r.rig, "mayor", "rig")
	block := orchestrator.FormatCodeindexContextForBead(mayorRig, beadPath, r.v)
	if block == "" {
		orchestratedPrintf("[gt-agent] codeindex context for %s: none (index missing or disabled)\n", beadPath)
		return
	}
	orchestratedPrintf("[gt-agent] codeindex context for %s: %s\n", beadPath, orchestrator.CodeindexContextSummary(block))
}

func (r *stateRunner) appendImplementationCodeindexReminder(b *strings.Builder) {
	if b == nil || !strings.EqualFold(strings.TrimSpace(r.hooks.Track), "implementation") {
		return
	}
	beadPath := r.implementBeadPathForCodeindex()
	if beadPath == "" {
		return
	}
	mayorRig := filepath.Join(r.townRoot, r.rig, "mayor", "rig")
	reminder := orchestrator.FormatCodeindexSymbolsReminderForBead(mayorRig, beadPath, r.v)
	if reminder == "" {
		return
	}
	b.WriteString("\n\n**Codeindex symbols (use these names in EDIT/WRITE):**\n")
	b.WriteString(reminder)
}

func (r *stateRunner) refreshCodeindexAfterGoWrite(relPath string) {
	if !orchestrator.CodeindexEnabled() {
		return
	}
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if !strings.HasSuffix(strings.ToLower(relPath), ".go") {
		return
	}
	mayorRig := filepath.Join(r.townRoot, r.rig, "mayor", "rig")
	if log, err := orchestrator.RefreshCodeindexIndex(mayorRig, r.v); err != nil {
		orchestratedFprintfStderr("[gt-agent] codeindex refresh after write: %v\n", err)
	} else if log != "" {
		orchestratedPrintf("[gt-agent] %s\n", log)
	}
}
