// gt-proxy-server is the mTLS proxy server for sandboxed polecat execution.
// It runs on the host and allows containers to call gt/bd and access git repos
// via authenticated, authorized HTTP endpoints.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/steveyegge/gastown/internal/proxy"
)

func main() {
	var (
		configFile     = flag.String("config", "", "path to config file (default: ~/gt/.runtime/proxy/config.json)")
		listen         = flag.String("listen", "0.0.0.0:9876", "address to listen on")
		adminListen    = flag.String("admin-listen", "127.0.0.1:9877", "address for local admin HTTP server (use empty string to disable)")
		caDir          = flag.String("ca-dir", "", "directory for CA cert/key (default: ~/gt/.runtime/ca)")
		allowedCmds    = flag.String("allowed-cmds", "gt,bd", "comma-separated list of allowed commands")
		allowedSubcmds = flag.String("allowed-subcmds", discoverAllowedSubcmds(), `semicolon-separated list of "cmd:sub1,sub2,..." subcommand allowlists`)
		townRoot       = flag.String("town-root", "", "Gas Town root directory (default: $GT_TOWN or ~/gt)")
	)
	flag.Parse()

	cfg, caDirPath, err := resolveServerConfig(flag.CommandLine, *configFile, listen, adminListen, caDir, allowedCmds, allowedSubcmds, townRoot)
	if err != nil {
		slog.Error("failed to resolve server config", "err", err)
		os.Exit(1)
	}

	ca, err := proxy.LoadOrGenerateCA(caDirPath)
	if err != nil {
		slog.Error("CA setup failed", "err", err)
		os.Exit(1)
	}
	slog.Info("CA loaded", "dir", caDirPath)

	srv, err := proxy.New(cfg, ca)
	if err != nil {
		slog.Error("invalid server config", "err", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}
