package main

import (
	"fmt"
	"path/filepath"
	"os"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func main() {
	townRoot := "/home/stevef/gt"
	rig := "testgt3"
	mayorRigDir := filepath.Join(townRoot, rig, "mayor", "rig")

	v, _, err := orchestrator.LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	
	scoped := v.ForActivePhase()

	err = orchestrator.ValidateWorkNotStubbed(mayorRigDir, scoped)
	fmt.Println("ValidateWorkNotStubbed result:", err)
}
