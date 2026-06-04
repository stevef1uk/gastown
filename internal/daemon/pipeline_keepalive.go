package daemon

import (
	"time"

	"github.com/steveyegge/gastown/internal/orchestrator"
	"github.com/steveyegge/gastown/internal/session"
)

// pipelineKeepaliveInterval is how often the daemon re-checks rig-flow pipeline agents
// while the orchestrator is active. Faster than the 3m recovery heartbeat so gt status
// stays ● between long LLM turns and brief gt-agent exits.
const pipelineKeepaliveInterval = 45 * time.Second

// ensureOrchestratedPipelineKeepalive starts or revives mayor/planner/setup and
// per-rig pipeline agents (architect, qa, polecat) for rigs with a running workflow.
// Witness/refinery/deacon/mechanic are skipped when pipeline-only mode is active.
func (d *Daemon) ensureOrchestratedPipelineKeepalive() {
	if d.isShutdownInProgress() {
		return
	}
	orchRunning, _, _ := orchestrator.IsRunning(d.config.TownRoot)
	if !orchRunning {
		return
	}

	d.ensureMayorRunning()

	if d.isPatrolActive("planner") {
		if p := d.checkPressure("planner"); p.OK {
			d.ensurePlannerRunning()
		}
	}

	if p := d.checkPressure("setup"); p.OK {
		d.ensureSetupRunning()
	}

	architectPatrol := d.isPatrolActive("architect")
	qaPatrol := d.isPatrolActive("qa")

	for _, rigName := range d.getKnownRigs() {
		if orchestrator.RigWorkflowActivityForRig(d.config.TownRoot, rigName) != orchestrator.RigWorkflowRunning {
			continue
		}
		operational, reason := d.isRigOperational(rigName)
		if !operational {
			d.logger.Printf("Pipeline keepalive: skip %s (%s)", rigName, reason)
			continue
		}
		if architectPatrol {
			if p := d.checkPressure("architect"); p.OK {
				d.ensureArchitectRunning(rigName)
			}
		}
		if qaPatrol {
			if p := d.checkPressure("qa"); p.OK {
				d.ensureQARunning(rigName)
			}
		}
		// Rig-flow polecat must stay up during implementation even when architect/qa
		// patrols are absent from mayor/daemon.json (common in orchestrator-only towns).
		d.ensureRigPolecatRunning(rigName)
	}

	for _, line := range orchestrator.RunWorkflowStuckMonitorTick(d.config.TownRoot, d.rigPolecatSessionRunning) {
		d.logger.Println(line)
	}
}

// rigPolecatSessionRunning reports whether the orchestrated rig polecat tmux session exists.
func (d *Daemon) rigPolecatSessionRunning(rigName string) bool {
	if d.sp == nil || rigName == "" {
		return false
	}
	name := session.RigPolecatSessionName(session.PrefixFor(rigName), rigName)
	running, err := d.sp.Exists(d.ctx, name)
	return err == nil && running
}
