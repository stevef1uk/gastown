package main

import (
	"fmt"
	"path/filepath"
	"github.com/steveyegge/gastown/internal/beads"
)

func main() {
	townRoot := "/home/stevef/gt"
	townBeadsDir := filepath.Join(townRoot, ".beads")
	beadID := "te-syu"
	
	resolved := beads.ResolveBeadsDirForID(townBeadsDir, beadID)
	fmt.Printf("Bead: %s\n", beadID)
	fmt.Printf("Town Beads Dir: %s\n", townBeadsDir)
	fmt.Printf("Resolved: %s\n", resolved)
	fmt.Printf("Final Dir: %s\n", filepath.Dir(resolved))
}
