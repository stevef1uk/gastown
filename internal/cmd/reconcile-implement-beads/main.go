// reconcile-implement-beads audits required_files on disk and reopens closed implement
// beads that should not be closed (missing/stub/empty artifacts).
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func main() {
	townRoot := flag.String("town", os.Getenv("GT_ROOT"), "Gas Town root (default GT_ROOT or ~/gt)")
	rig := flag.String("rig", os.Getenv("RIG"), "Rig name (default RIG or testgt3)")
	dryRun := flag.Bool("dry-run", false, "Audit only; do not bd update")
	flag.Parse()
	if strings.TrimSpace(*townRoot) == "" {
		*townRoot = strings.TrimSpace(os.Getenv("HOME")) + "/gt"
	}
	if strings.TrimSpace(*rig) == "" {
		*rig = "testgt3"
	}

	v, ok, err := orchestrator.LoadRigWorkflowProfileFile(*townRoot, *rig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load profile: %v\n", err)
		os.Exit(1)
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "no workflow-profile.json for rig %q — run gt rig spec-index\n", *rig)
		os.Exit(1)
	}

	rigDir := strings.TrimSpace(*townRoot) + "/" + strings.TrimSpace(*rig) + "/mayor/rig"
	fmt.Println("=== Audit required_files ===")
	for _, issue := range orchestrator.AuditRequiredImplementFiles(rigDir, v) {
		fmt.Println(" ", issue)
	}
	mismatches, err := orchestrator.AuditClosedImplementBeadMismatches(*townRoot, *rig, v, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "audit closed beads: %v\n", err)
		os.Exit(1)
	}
	if len(mismatches) > 0 {
		fmt.Println("=== Closed beads with bad artifacts ===")
		for _, m := range mismatches {
			fmt.Println(" ", m)
		}
	}
	if *dryRun {
		fmt.Println("(dry-run: no bd update)")
		if len(mismatches) == 0 && len(orchestrator.AuditRequiredImplementFiles(rigDir, v)) == 0 {
			os.Exit(0)
		}
		os.Exit(2)
	}

	log, err := orchestrator.ReconcileImplementBeads(*townRoot, *rig, v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reconcile: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("=== Reconcile ===")
	fmt.Println(log)
}
