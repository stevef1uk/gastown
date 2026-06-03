package daemon

import (
	"time"

	"github.com/steveyegge/gastown/internal/orchestrator"
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

	if !d.isPatrolActive("architect") && !d.isPatrolActive("qa") {
		return
	}

	for _, rigName := range d.getKnownRigs() {
		if orchestrator.RigWorkflowActivityForRig(d.config.TownRoot, rigName) != orchestrator.RigWorkflowRunning {
			continue
		}
		if operational, reason := d.isRigOperational(rigName); !operational {
			d.logger.Printf("Pipeline keepalive: skip %s (%s)", rigName, reason)
			continue
		}
		if d.isPatrolActive("architect") {
			if p := d.checkPressure("architect"); p.OK {
				d.ensureArchitectRunning(rigName)
			}
		}
		if d.isPatrolActive("qa") {
			if p := d.checkPressure("qa"); p.OK {
				d.ensureQARunning(rigName)
			}
		}
		d.ensureRigPolecatRunning(rigName)
	}
}
