package orchestrator

import (
	"context"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/session"
)

// StopPatrolLLMAgents stops town patrol agents that burn LLM quota but are not
// needed for rig-flow when pipeline-only mode is active (deacon, boot, mechanic,
// per-rig witness and refinery).
func StopPatrolLLMAgents(townRoot string) {
	sp := session.GetDefaultProvider(townRoot)
	ctx := context.Background()
	stopSessionIfExists(ctx, sp, session.DeaconSessionName())
	stopSessionIfExists(ctx, sp, session.BootSessionName())
	stopSessionIfExists(ctx, sp, session.MechanicSessionName())

	for _, rigName := range knownRigNames(townRoot) {
		prefix := session.PrefixFor(rigName)
		stopSessionIfExists(ctx, sp, session.WitnessSessionName(prefix, rigName))
		stopSessionIfExists(ctx, sp, session.RefinerySessionName(prefix, rigName))
		// Legacy per-rig mechanic sessions (town-level hq-mechanic is canonical).
		stopSessionIfExists(ctx, sp, session.MechanicSessionNameForRig(rigName))
	}
}

func stopSessionIfExists(ctx context.Context, sp session.Provider, sessionID string) {
	exists, err := sp.Exists(ctx, sessionID)
	if err != nil || !exists {
		return
	}
	_ = sp.Stop(ctx, sessionID, true)
}

func knownRigNames(townRoot string) []string {
	rigsConfig, err := config.LoadRigsConfig(constants.MayorRigsPath(townRoot))
	if err != nil || rigsConfig == nil {
		return nil
	}
	names := make([]string, 0, len(rigsConfig.Rigs))
	for name := range rigsConfig.Rigs {
		names = append(names, name)
	}
	return names
}
