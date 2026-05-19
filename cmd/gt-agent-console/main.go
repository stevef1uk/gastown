package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/steveyegge/gastown/internal/agentconsole"
	"github.com/steveyegge/gastown/internal/workspace"
)

func main() {
	port := 8090 // Default changed from 8081 to avoid conflict with dev servers
	if p := os.Getenv("GT_AGENT_CONSOLE_PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}
	bind := "127.0.0.1"
	if b := os.Getenv("GT_AGENT_CONSOLE_BIND"); b != "" {
		bind = b
	}

	// Find town root
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: not in a Gas Town workspace: %v\n", err)
		os.Exit(1)
	}

	server, err := agentconsole.NewServer(townRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: creating server: %v\n", err)
		os.Exit(1)
	}
	defer server.Close()

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	listenAddr := fmt.Sprintf("%s:%d", bind, port)
	fmt.Printf("Agent Console starting at http://%s\n", listenAddr)
	fmt.Println("Press Ctrl+C to stop")

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}
