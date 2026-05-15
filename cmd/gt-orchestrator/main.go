package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/nats-io/nats.go"
	"github.com/steveyegge/gastown/internal/orchestrator"
)

func main() {
	townRoot := os.Getenv("GT_TOWN_ROOT")
	if townRoot == "" {
		townRoot = "."
	}

	mgr := orchestrator.NewManager(townRoot)
	tmplDir := filepath.Join(townRoot, "orchestrator", "templates")
	if err := mgr.LoadTemplatesFromDir(tmplDir); err != nil {
		log.Printf("Warning: failed to load templates: %v", err)
	}

	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = nats.DefaultURL
	}

	server := orchestrator.NewServer(mgr)
	fmt.Printf("Orchestrator listening on NATS: %s\n", natsURL)
	if err := server.ListenNATS(natsURL); err != nil {
		log.Fatalf("starting NATS listener: %v", err)
	}

	select {}
}
