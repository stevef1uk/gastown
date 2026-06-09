package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/steveyegge/gastown/internal/agentconsole"
	"github.com/steveyegge/gastown/internal/workspace"
)

func main() {
	var portFlag int
	var bindFlag string
	flag.IntVar(&portFlag, "port", 0, "HTTP listen port (overrides GT_AGENT_CONSOLE_PORT)")
	flag.StringVar(&bindFlag, "bind", "", "HTTP bind address (overrides GT_AGENT_CONSOLE_BIND)")
	flag.Parse()

	listen, err := agentconsole.ResolveListenConfig(portFlag, bindFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

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

	fmt.Printf("Agent Console starting at %s\n", listen.URL())
	fmt.Println("Press Ctrl+C to stop")

	srv := &http.Server{
		Addr:              listen.Addr(),
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
