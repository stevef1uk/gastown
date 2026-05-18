package session

import (
	"context"
	"fmt"
	"strings"
)

// RigPipelineSessionIDs returns orchestrated rig-flow session names for a rig.
func RigPipelineSessionIDs(rigName string) []string {
	prefix := PrefixFor(rigName)
	return []string{
		ArchitectSessionName(prefix, rigName),
		QASessionName(prefix, rigName),
		RigPolecatSessionName(prefix, rigName),
	}
}

// StopRigPipelineSessions stops architect, QA, and orchestrated polecat for rig.
// Returns names of sessions that were stopped. graceful=false matches --force shutdown.
func StopRigPipelineSessions(p Provider, rigName string, graceful bool) ([]string, error) {
	if p == nil || rigName == "" {
		return nil, nil
	}
	ctx := context.Background()
	var stopped []string
	var errs []string
	for _, sessionID := range RigPipelineSessionIDs(rigName) {
		exists, err := p.Exists(ctx, sessionID)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", sessionID, err))
			continue
		}
		if !exists {
			continue
		}
		if err := p.Stop(ctx, sessionID, graceful); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", sessionID, err))
			continue
		}
		stopped = append(stopped, sessionID)
	}
	if len(errs) > 0 {
		return stopped, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return stopped, nil
}
