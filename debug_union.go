package main

import (
	"fmt"
	"os"
	"path/filepath"

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
	
	fmt.Println("LayoutRoot:", v.LayoutRoot)
	
	for i, f := range v.UnionRequiredFiles() {
		fmt.Printf("Union[%d]: %s\n", i, f)
		path := orchestrator.ResolveRequiredFileOnDisk(mayorRigDir, f, v.LayoutRoot)
		fmt.Printf("  -> Path: %s\n", path)
	}
}
