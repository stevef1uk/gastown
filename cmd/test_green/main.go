package main

import (
	"fmt"
	"os"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func main() {
	townRoot := "/home/stevef/gt"
	rig := "testgt3"
	
	v, _, err := orchestrator.LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil {
		fmt.Printf("Load err: %v\n", err)
		os.Exit(1)
	}
	
	fmt.Printf("Active Phase: %s\n", v.ActivePhaseID())
	green := orchestrator.ImplementationQueueGreen(townRoot, rig, v)
	fmt.Printf("ImplementationQueueGreen: %v\n", green)
}
