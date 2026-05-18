package cmd

import (
	"github.com/spf13/cobra"
)

// shutdownRigAgents stops all rig agents (witness, refinery, polecats, architect, qa, pipeline polecat).
func shutdownRigAgents(townRoot, rigName string, force bool) error {
	if rigName == "" {
		return nil
	}
	prev := rigShutdownForce
	rigShutdownForce = force
	defer func() { rigShutdownForce = prev }()
	return runRigShutdown(&cobra.Command{}, []string{rigName})
}
