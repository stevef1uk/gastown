package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/crew"
	"github.com/steveyegge/gastown/internal/daemon"
	"github.com/steveyegge/gastown/internal/deacon"
	"github.com/steveyegge/gastown/internal/doltserver"
	"github.com/steveyegge/gastown/internal/events"
	"github.com/steveyegge/gastown/internal/mail"
	"github.com/steveyegge/gastown/internal/mayor"
	"github.com/steveyegge/gastown/internal/natsserver"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/refinery"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/util"
	"github.com/steveyegge/gastown/internal/witness"
	"github.com/steveyegge/gastown/internal/workspace"
)

// agentStartResult holds the result of starting an agent.
type agentStartResult struct {
	name   string // Display name like "Witness (gastown)"
	ok     bool   // Whether start succeeded
	detail string // Status detail (session name or error)
}

// UpOutput represents the JSON output of the up command.
type UpOutput struct {
	Success  bool            `json:"success"`
	Services []ServiceStatus `json:"services"`
	Summary  UpSummary       `json:"summary"`
}

// ServiceStatus represents the status of a single service.
type ServiceStatus struct {
	Name   string `json:"name"`
	Type   string `json:"type"` // daemon, deacon, mayor, witness, refinery, crew, polecat
	Rig    string `json:"rig,omitempty"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// UpSummary provides counts for the up command output.
type UpSummary struct {
	Total   int `json:"total"`
	Started int `json:"started"`
	Failed  int `json:"failed"`
}

func buildUpSummary(services []ServiceStatus) UpSummary {
	started := 0
	failed := 0
	for _, svc := range services {
		if svc.OK {
			started++
		} else {
			failed++
		}
	}
	return UpSummary{
		Total:   len(services),
		Started: started,
		Failed:  failed,
	}
}

func emitUpJSON(w io.Writer, services []ServiceStatus) error {
	summary := buildUpSummary(services)
	output := UpOutput{
		Success:  summary.Failed == 0,
		Services: services,
		Summary:  summary,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(output); err != nil {
		return err
	}
	if summary.Failed > 0 {
		return NewSilentExit(1)
	}
	return nil
}

// maxConcurrentAgentStarts limits parallel agent startups to avoid resource
// exhaustion. Each agent start spawns a tmux session and runs gt prime, so
// more than ~10 concurrent starts can saturate CPU and cause timeouts.
const maxConcurrentAgentStarts = 10

// daemonStartupGrace is how long to wait after spawning the daemon process
// before verifying it started. The daemon needs time to write its PID file.
// On Windows, DETACHED_PROCESS startup is slower so we allow extra time.
var daemonStartupGrace = func() time.Duration {
	if runtime.GOOS == "windows" {
		return 2 * time.Second
	}
	return 300 * time.Millisecond
}()

var upCmd = &cobra.Command{
	Use:     "up",
	GroupID: GroupServices,
	Short:   "Bring up all Gas Town services",
	Long: `Start all Gas Town long-lived services.

This is the idempotent "boot" command for Gas Town. It ensures all
infrastructure agents are running:

  • Dolt       - Shared SQL database server for beads
  • Daemon     - Go background process that pokes agents
  • Deacon     - Health orchestrator (monitors Mayor/Witnesses)
  • Mayor      - Global work coordinator
  • Witnesses  - Per-rig polecat managers
  • Refineries - Per-rig merge queue processors

Polecats are NOT started by this command - they are transient workers
spawned on demand by the Mayor or Witnesses.

Use --restore to also start:
  • Crew       - Per rig settings (settings/config.json crew.startup)
  • Polecats   - Those with pinned beads (work attached)

Running 'gt up' multiple times is safe - it only starts services that
aren't already running.`,
	RunE: runUp,
}

var (
	upQuiet   bool
	upRestore bool
	upJSON    bool
)

func init() {
	upCmd.Flags().BoolVarP(&upQuiet, "quiet", "q", false, "Only show errors (ignored with --json)")
	upCmd.Flags().BoolVar(&upRestore, "restore", false, "Also restore crew (from settings) and polecats (from hooks)")
	upCmd.Flags().BoolVar(&upJSON, "json", false, "Output as JSON")
	rootCmd.AddCommand(upCmd)
}

func runUp(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	// Ensure lifecycle defaults are configured. On first run this creates
	// mayor/daemon.json with sensible defaults for the six-stage Dolt lifecycle.
	// On subsequent runs it fills in any newly added patrols without touching
	// existing config. Errors are non-fatal — the town can run without lifecycle
	// automation, it just won't have automated maintenance.
	if err := daemon.EnsureLifecycleConfigFile(townRoot); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not configure lifecycle defaults: %v\n", err)
	}

	// Load daemon.json env vars so services (Dolt, etc.) use the right config.
	// The daemon does this too, but gt up starts services before the daemon.
	if patrolCfg := daemon.LoadPatrolConfig(townRoot); patrolCfg != nil {
		for k, v := range patrolCfg.Env {
			os.Setenv(k, v)
		}
	}

	allOK := true
	var services []ServiceStatus

	// Discover rigs early so we can prefetch while daemon/deacon/mayor start
	rigs := discoverRigs(townRoot)

	// Safety: bring current agent out of DND on startup so orchestration nudges
	// are not silently muted after a previous incident/debug session.
	if changed, err := disableCurrentAgentDND(townRoot); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not reset DND state: %v\n", err)
	} else if changed && !upQuiet {
		fmt.Printf("%s DND was enabled; reset to normal for current agent\n", style.SuccessPrefix)
	}

	// Start infrastructure services first (sequentially where dependencies exist)
	var daemonErr error
	var daemonPID int
	var deaconResult, mayorResult agentStartResult
	var prefetchedRigs map[string]*rig.Rig
	var rigErrors map[string]error
	var doltOK, natsOK bool
	var doltDetail, natsDetail string
	var doltSkipped bool

	// 0. NATS server (Docker) — start FIRST and wait for readiness.
	// The daemon and agents need NATS to be available when they initialize
	// their session provider. Starting NATS sequentially avoids race conditions.
	if err := natsserver.Start(natsserver.Config{}); err != nil {
		// Start may fail if container already exists (Docker exit 125), but
		// NATS could still be running — check IsRunning before giving up.
		if natsserver.IsRunning() {
			natsOK = true
			natsDetail = "already running"
		} else {
			natsDetail = err.Error()
		}
	} else {
		natsOK = true
		if natsserver.IsRunning() {
			natsDetail = "started (port 4222)"
		} else {
			natsDetail = "already running"
		}
	}
	if !natsOK {
		// Continue anyway — agents will fall back to tmux if NATS is unavailable
		fmt.Fprintf(os.Stderr, "Warning: NATS not available, agents may use tmux fallback\n")
	}

	// Start daemon, deacon, mayor, planner, mechanic, dolt, and rig prefetch in parallel
	var startupWg sync.WaitGroup
	startupWg.Add(7)

	// 1. Dolt server (if configured)
	go func() {
		defer startupWg.Done()
		cfg := doltserver.DefaultConfig(townRoot)

		// If data dir is missing (fresh install or after cleanup), create it and
		// initialize the town database so Dolt can start successfully.
		if _, err := os.Stat(cfg.DataDir); os.IsNotExist(err) {
			if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
				doltDetail = fmt.Sprintf("creating data dir: %v", err)
				return
			}
		}
		// Ensure town database exists — without at least one DB Dolt exits immediately.
		townDB := filepath.Join(cfg.DataDir, "hq")
		if _, err := os.Stat(filepath.Join(townDB, ".dolt")); os.IsNotExist(err) {
			if err := os.MkdirAll(townDB, 0755); err != nil {
				doltDetail = fmt.Sprintf("creating town db dir: %v", err)
				return
			}
			cmd := exec.Command("dolt", "init")
			cmd.Dir = townDB
			if output, err := cmd.CombinedOutput(); err != nil {
				doltDetail = fmt.Sprintf("initializing town db: %v\n%s", err, output)
				return
			}
		}

		running, _, _ := doltserver.IsRunning(townRoot)
		if running {
			doltOK = true
			doltDetail = "already running"
			return
		}
		if err := doltserver.Start(townRoot); err != nil {
			doltDetail = err.Error()
			return
		}
		// Wait for Dolt to actually accept connections before declaring it ready.
		// Agents (deacon, mayor, witness, refinery) run bd commands on startup
		// via gt prime → patrol_helpers. Without this gate, they race the server.
		waitForDoltReady(townRoot)
		doltOK = true
		doltDetail = fmt.Sprintf("started (port %d)", cfg.Port)
	}()

	// 2. Daemon (Go process)
	go func() {
		defer startupWg.Done()
		if err := ensureDaemon(townRoot); err != nil {
			daemonErr = err
		} else {
			running, pid, _ := daemon.IsRunning(townRoot)
			if running {
				daemonPID = pid
			}
		}
	}()

	// 3. Deacon
	go func() {
		defer startupWg.Done()
		deaconMgr := deacon.NewManager(townRoot)
		if err := deaconMgr.Start(""); err != nil {
			if err == deacon.ErrAlreadyRunning {
				deaconResult = agentStartResult{name: "Deacon", ok: true, detail: deaconMgr.SessionName()}
			} else {
				deaconResult = agentStartResult{name: "Deacon", ok: false, detail: err.Error()}
			}
		} else {
			deaconResult = agentStartResult{name: "Deacon", ok: true, detail: deaconMgr.SessionName()}
		}
	}()

	// 4. Mayor
	go func() {
		defer startupWg.Done()
		mayorMgr := mayor.NewManager(townRoot)
		if err := mayorMgr.Start(""); err != nil {
			if errors.Is(err, mayor.ErrAlreadyRunning) {
				mayorResult = agentStartResult{name: "Mayor", ok: true, detail: mayorMgr.SessionName()}
			} else if errors.Is(err, mayor.ErrACPActive) {
				mayorResult = agentStartResult{name: "Mayor", ok: true, detail: "ACP active"}
			} else {
				mayorResult = agentStartResult{name: "Mayor", ok: false, detail: err.Error()}
			}
		} else {
			mayorResult = agentStartResult{name: "Mayor", ok: true, detail: mayorMgr.SessionName()}
		}
	}()

	// 4.5. Planner
	var plannerResult agentStartResult
	go func() {
		defer startupWg.Done()
		plannerResult = upStartPlanner(townRoot)
	}()

	// 4.6. Mechanic
	var mechanicResult agentStartResult
	go func() {
		defer startupWg.Done()
		mechanicResult = upStartMechanic(townRoot)
	}()

	// 5. Prefetch rig configs (overlaps with daemon/deacon/mayor startup)
	go func() {
		defer startupWg.Done()
		prefetchedRigs, rigErrors = prefetchRigs(rigs)
	}()

	startupWg.Wait()

	// Ensure beads metadata points to the Dolt server
	if !doltSkipped && doltOK {
		_, _ = doltserver.EnsureAllMetadata(townRoot)
	}

	// Collect Dolt and NATS status
	services = append(services, ServiceStatus{Name: "NATS", Type: "nats", OK: natsOK, Detail: natsDetail})
	if !natsOK {
		allOK = false
	}

	if !doltSkipped {
		services = append(services, ServiceStatus{Name: "Dolt", Type: "dolt", OK: doltOK, Detail: doltDetail})
		if !doltOK {
			allOK = false
		}
	}

	// Collect daemon/deacon/mayor results (always append daemon status)
	if daemonErr != nil {
		services = append(services, ServiceStatus{Name: "Daemon", Type: "daemon", OK: false, Detail: daemonErr.Error()})
		allOK = false
	} else if daemonPID > 0 {
		services = append(services, ServiceStatus{Name: "Daemon", Type: "daemon", OK: true, Detail: fmt.Sprintf("PID %d", daemonPID)})
	} else {
		services = append(services, ServiceStatus{Name: "Daemon", Type: "daemon", OK: true, Detail: "running (PID unknown)"})
	}
	services = append(services, ServiceStatus{Name: deaconResult.name, Type: constants.RoleDeacon, OK: deaconResult.ok, Detail: deaconResult.detail})
	if !deaconResult.ok {
		allOK = false
	}
	services = append(services, ServiceStatus{Name: mayorResult.name, Type: constants.RoleMayor, OK: mayorResult.ok, Detail: mayorResult.detail})
	if !mayorResult.ok {
		allOK = false
	}
	services = append(services, ServiceStatus{Name: plannerResult.name, Type: constants.RolePlanner, OK: plannerResult.ok, Detail: plannerResult.detail})
	if !plannerResult.ok {
		allOK = false
	}
	services = append(services, ServiceStatus{Name: mechanicResult.name, Type: constants.RoleMechanic, OK: mechanicResult.ok, Detail: mechanicResult.detail})
	if !mechanicResult.ok {
		allOK = false
	}

	// Ensure Dolt server is fully ready before starting agents that depend on it.
	// Witnesses and refineries run bd commands on startup (via gt prime → patrol_helpers)
	// that connect to the Dolt SQL server. Without this gate, they race the server
	// and get "connection refused" errors. (gt-zou1n)
	// Only wait if Dolt was actually started (or detected running). If it failed or
	// was skipped, polling the port would just burn the full timeout. (review finding #1)
	if !doltSkipped && doltOK {
		waitForDoltReady(townRoot)
		// Propagate Dolt connection info to process env so all subsequently spawned
		// agents (witnesses, refineries, crew) inherit it. Without this,
		// bd auto-starts rogue Dolt instances in agent tmux sessions. (GH#2412)
		// Host propagation prevents bd from falling back to 127.0.0.1 when the
		// Dolt server runs on a remote machine (e.g., mini2 over Tailscale).
		doltCfg := doltserver.DefaultConfig(townRoot)
		portStr := fmt.Sprintf("%d", doltCfg.Port)
		os.Setenv("GT_DOLT_PORT", portStr)
		os.Setenv("BEADS_DOLT_PORT", portStr)
		if doltCfg.Host != "" {
			os.Setenv("BEADS_DOLT_SERVER_HOST", doltCfg.Host)
		}

		// Propagate session transport to subsequently spawned agents.
		// Without this, agents use the default (tmux) even when town is
		// configured for NATS.
		if settings, err := config.LoadOrCreateTownSettings(config.TownSettingsPath(townRoot)); err == nil && settings != nil {
			if settings.SessionTransport != "" {
				os.Setenv("GT_SESSION_TRANSPORT", settings.SessionTransport)
			}
			if settings.NatsURL != "" {
				os.Setenv("GT_NATS_URL", settings.NatsURL)
			}
		}
	}

	// Orphaned bead recovery: detect beads stuck in hooked/in_progress status
	// assigned to polecats that no longer exist (session dead + directory gone).
	// After a crash, these beads sit orphaned until someone manually resets them.
	// Running this before witnesses start avoids duplicate recovery. (gas-udp)
	if !doltSkipped && doltOK {
		orphanServices := recoverOrphanedBeads(townRoot, rigs, prefetchedRigs)
		services = append(services, orphanServices...)
	}

	// 5 & 6. Witnesses, Refineries, Architects, and QAs (using prefetched rigs)
	witnessResults, refineryResults, architectResults, qaResults := startRigAgentsWithPrefetch(rigs, prefetchedRigs, rigErrors)

	// Collect results in order: all witnesses first, then all refineries, then architects, then qa
	for _, rigName := range rigs {
		if result, ok := witnessResults[rigName]; ok {
			services = append(services, ServiceStatus{Name: result.name, Type: constants.RoleWitness, Rig: rigName, OK: result.ok, Detail: result.detail})
			if !result.ok {
				allOK = false
			}
		}
	}
	for _, rigName := range rigs {
		if result, ok := refineryResults[rigName]; ok {
			services = append(services, ServiceStatus{Name: result.name, Type: constants.RoleRefinery, Rig: rigName, OK: result.ok, Detail: result.detail})
			if !result.ok {
				allOK = false
			}
		}
	}
	for _, rigName := range rigs {
		if result, ok := architectResults[rigName]; ok {
			services = append(services, ServiceStatus{Name: result.name, Type: constants.RoleArchitect, Rig: rigName, OK: result.ok, Detail: result.detail})
			if !result.ok {
				allOK = false
			}
		}
	}
	for _, rigName := range rigs {
		if result, ok := qaResults[rigName]; ok {
			services = append(services, ServiceStatus{Name: result.name, Type: constants.RoleQA, Rig: rigName, OK: result.ok, Detail: result.detail})
			if !result.ok {
				allOK = false
			}
		}
	}

	// 7. Crew (if --restore)
	if upRestore {
		for _, rigName := range rigs {
			crewStarted, crewErrors := startCrewFromSettings(townRoot, rigName)
			for _, name := range crewStarted {
				services = append(services, ServiceStatus{
					Name:   fmt.Sprintf("Crew (%s/%s)", rigName, name),
					Type:   constants.RoleCrew,
					Rig:    rigName,
					OK:     true,
					Detail: session.CrewSessionName(session.PrefixFor(rigName), name),
				})
			}
			for name, err := range crewErrors {
				services = append(services, ServiceStatus{
					Name:   fmt.Sprintf("Crew (%s/%s)", rigName, name),
					Type:   constants.RoleCrew,
					Rig:    rigName,
					OK:     false,
					Detail: err.Error(),
				})
				allOK = false
			}
		}

		// 7. Polecats with pinned work (if --restore)
		for _, rigName := range rigs {
			polecatsStarted, polecatErrors := startPolecatsWithWork(townRoot, rigName)
			for _, name := range polecatsStarted {
				services = append(services, ServiceStatus{
					Name:   fmt.Sprintf("Polecat (%s/%s)", rigName, name),
					Type:   constants.RolePolecat,
					Rig:    rigName,
					OK:     true,
					Detail: session.PolecatSessionName(session.PrefixFor(rigName), name),
				})
			}
			for name, err := range polecatErrors {
				services = append(services, ServiceStatus{
					Name:   fmt.Sprintf("Polecat (%s/%s)", rigName, name),
					Type:   constants.RolePolecat,
					Rig:    rigName,
					OK:     false,
					Detail: err.Error(),
				})
				allOK = false
			}
		}
	}

	// Log boot event for both JSON and text paths
	if allOK {
		startedServices := []string{"dolt", "daemon", "deacon", "mayor", "planner", "mechanic"}
		for _, rigName := range rigs {
			startedServices = append(startedServices, fmt.Sprintf("%s/witness", rigName))
			startedServices = append(startedServices, fmt.Sprintf("%s/refinery", rigName))
			startedServices = append(startedServices, fmt.Sprintf("%s/architect", rigName))
			startedServices = append(startedServices, fmt.Sprintf("%s/qa", rigName))
		}
		_ = events.LogFeed(events.TypeBoot, "gt", events.BootPayload("town", startedServices))
	}

	// Output JSON or text
	if upJSON {
		return emitUpJSON(os.Stdout, services)
	}

	// Text output
	for _, svc := range services {
		printStatus(svc.Name, svc.OK, svc.Detail)
	}

	fmt.Println()
	if allOK {
		fmt.Printf("%s All services running\n", style.Bold.Render("✓"))
	} else {
		fmt.Printf("%s Some services failed to start\n", style.Bold.Render("✗"))
		return fmt.Errorf("not all services started")
	}

	return nil
}

func printStatus(name string, ok bool, detail string) {
	if upQuiet && ok {
		return
	}
	if ok {
		fmt.Printf("%s %s: %s\n", style.SuccessPrefix, name, style.Dim.Render(detail))
	} else {
		fmt.Printf("%s %s: %s\n", style.ErrorPrefix, name, detail)
	}
}

// disableCurrentAgentDND resets DND for the current role context (if muted).
// Returns true when a change was applied.
func disableCurrentAgentDND(townRoot string) (bool, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return false, fmt.Errorf("getting current directory: %w", err)
	}

	roleInfo, err := GetRoleWithContext(cwd, townRoot)
	if err != nil {
		// No role context (or not in role workspace): nothing to change.
		return false, nil
	}

	ctx := RoleContext{
		Role:     roleInfo.Role,
		Rig:      roleInfo.Rig,
		Polecat:  roleInfo.Polecat,
		TownRoot: townRoot,
		WorkDir:  cwd,
	}
	agentBeadID := getAgentBeadID(ctx)
	if agentBeadID == "" {
		return false, nil
	}

	bd := beads.New(townRoot)
	level, err := bd.GetAgentNotificationLevel(agentBeadID)
	if err != nil {
		// Missing bead/field should not block startup.
		return false, nil
	}
	if level != beads.NotifyMuted {
		return false, nil
	}

	if err := bd.UpdateAgentNotificationLevel(agentBeadID, beads.NotifyNormal); err != nil {
		return false, fmt.Errorf("updating notification level for %s: %w", agentBeadID, err)
	}
	return true, nil
}

// expandGoBinPath returns an updated environment slice with PATH expanded
// to include the user's go/bin directory so that dolt, bd, and other
// Go-installed tools are available to the daemon and its spawned agents.
func expandGoBinPath() []string {
	home, _ := os.UserHomeDir()
	goBin := "/usr/local/go/bin"
	if home != "" {
		goBin = filepath.Join(home, "go/bin") + ":" + goBin
	}

	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		pathEnv = "/usr/local/bin:/usr/bin:/bin"
	}

	newPath := goBin + ":/usr/local/bin:/usr/bin:/bin:/sbin"
	if !strings.Contains(pathEnv, goBin) {
		newPath = newPath + ":" + pathEnv
	}

	// Replace existing PATH in os.Environ() to avoid duplicates
	env := os.Environ()
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			env[i] = "PATH=" + newPath
			return env
		}
	}
	return append(env, "PATH="+newPath)
}

// ensureDaemon starts the daemon if not running.
func ensureDaemon(townRoot string) error {
	// GH#2656: Don't restart the daemon while gt down is running.
	// GH#2907: If the sentinel's PID is dead, remove stale sentinel.
	sentinelPath := filepath.Join(townRoot, ShutdownSentinel)
	if data, err := os.ReadFile(sentinelPath); err == nil {
		stale := false
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			if process, err := os.FindProcess(pid); err != nil {
				stale = true
			} else if err := process.Signal(syscall.Signal(0)); err != nil {
				stale = true
			}
		} else {
			// Sentinel exists but has no valid PID — treat as stale.
			stale = true
		}
		if stale {
			os.Remove(sentinelPath)
		} else {
			return fmt.Errorf("shutdown in progress (sentinel exists: %s)", sentinelPath)
		}
	}

	running, _, err := daemon.IsRunning(townRoot)
	if err != nil {
		return err
	}
	if running {
		return nil
	}

	// Start daemon
	gtPath, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command(gtPath, "daemon", "run")
	cmd.Dir = townRoot
	// Detach from parent I/O for background daemon (uses its own logging)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	util.SetDetachedProcessGroup(cmd)

	// Ensure daemon inherits a complete PATH including go/bin for dolt, bd, etc.
	// The daemon spawns agents which inherit its environment.
	cmd.Env = expandGoBinPath()

	if err := cmd.Start(); err != nil {
		return err
	}

	// Wait for daemon to initialize
	time.Sleep(daemonStartupGrace)

	// Verify it started
	running, _, err = daemon.IsRunning(townRoot)
	if err != nil {
		return err
	}
	if !running {
		if msg := readDaemonStartupFailure(townRoot, cmd.Process.Pid); msg != "" {
			return fmt.Errorf("daemon failed to start: %s", msg)
		}
		return fmt.Errorf("daemon failed to start (check logs with 'gt daemon logs')")
	}

	return nil
}

// rigPrefetchResult holds the result of loading a single rig config.
type rigPrefetchResult struct {
	index int
	rig   *rig.Rig
	err   error
}

// prefetchRigs loads all rig configs in parallel for faster agent startup.
// Returns a map of rig name to loaded Rig, and any errors encountered.
func prefetchRigs(rigNames []string) (map[string]*rig.Rig, map[string]error) {
	n := len(rigNames)
	if n == 0 {
		return make(map[string]*rig.Rig), make(map[string]error)
	}

	// Use channel to collect results without locking
	results := make(chan rigPrefetchResult, n)

	for i, name := range rigNames {
		go func(idx int, rigName string) {
			_, r, err := getRig(rigName)
			results <- rigPrefetchResult{index: idx, rig: r, err: err}
		}(i, name)
	}

	// Collect results - pre-allocate maps with capacity
	rigs := make(map[string]*rig.Rig, n)
	errors := make(map[string]error)

	for i := 0; i < n; i++ {
		res := <-results
		name := rigNames[res.index]
		if res.err != nil {
			errors[name] = res.err
		} else {
			rigs[name] = res.rig
		}
	}

	return rigs, errors
}

// agentTask represents a unit of work for the agent worker pool.
type agentTask struct {
	rigName string
	rigObj  *rig.Rig
	role    string // "witness", "refinery", "architect", "qa"
}

// agentResultMsg carries result back from worker to collector.
type agentResultMsg struct {
	rigName string
	role    string
	result  agentStartResult
}

// startRigAgentsWithPrefetch starts all Witnesses, Refineries, Architects, and QAs using pre-loaded rig configs.
// Uses a worker pool with fixed goroutine count to limit concurrency and reduce overhead.
func startRigAgentsWithPrefetch(rigNames []string, prefetchedRigs map[string]*rig.Rig, rigErrors map[string]error) (witnessResults, refineryResults, architectResults, qaResults map[string]agentStartResult) {
	n := len(rigNames)
	witnessResults = make(map[string]agentStartResult, n)
	refineryResults = make(map[string]agentStartResult, n)
	architectResults = make(map[string]agentStartResult, n)
	qaResults = make(map[string]agentStartResult, n)

	if n == 0 {
		return
	}

	// Record errors for rigs that failed to load
	for rigName, err := range rigErrors {
		errDetail := err.Error()
		witnessResults[rigName] = agentStartResult{name: "Witness (" + rigName + ")", ok: false, detail: errDetail}
		refineryResults[rigName] = agentStartResult{name: "Refinery (" + rigName + ")", ok: false, detail: errDetail}
		architectResults[rigName] = agentStartResult{name: "Architect (" + rigName + ")", ok: false, detail: errDetail}
		qaResults[rigName] = agentStartResult{name: "QA (" + rigName + ")", ok: false, detail: errDetail}
	}

	if len(prefetchedRigs) == 0 {
		return
	}

	witnessResults = startRigAgentPhase(rigNames, prefetchedRigs, constants.RoleWitness)
	refineryResults = startRigAgentPhase(rigNames, prefetchedRigs, constants.RoleRefinery)
	architectResults = startRigAgentPhase(rigNames, prefetchedRigs, constants.RoleArchitect)

	for rigName, r := range prefetchedRigs {
		if archResult, ok := architectResults[rigName]; ok && !archResult.ok {
			qaResults[rigName] = agentStartResult{name: "QA (" + rigName + ")", ok: false, detail: "skipped (architect startup failed)"}
			continue
		}
		qaResults[rigName] = upStartQA(rigName, r)
	}

	return
}

func startRigAgentPhase(rigNames []string, prefetchedRigs map[string]*rig.Rig, role string) map[string]agentStartResult {
	resultsMap := make(map[string]agentStartResult, len(rigNames))
	numTasks := len(prefetchedRigs)
	if numTasks == 0 {
		return resultsMap
	}

	tasks := make(chan agentTask, numTasks)
	results := make(chan agentResultMsg, numTasks)

	numWorkers := maxConcurrentAgentStarts
	if numTasks < numWorkers {
		numWorkers = numTasks
	}

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range tasks {
				var result agentStartResult
				switch task.role {
				case constants.RoleWitness:
					result = upStartWitness(task.rigName, task.rigObj)
				case constants.RoleRefinery:
					result = upStartRefinery(task.rigName, task.rigObj)
				case constants.RoleArchitect:
					result = upStartArchitect(task.rigName, task.rigObj)
				case constants.RoleQA:
					result = upStartQA(task.rigName, task.rigObj)
				}
				results <- agentResultMsg{rigName: task.rigName, role: task.role, result: result}
			}
		}()
	}

	for rigName, r := range prefetchedRigs {
		tasks <- agentTask{rigName: rigName, rigObj: r, role: role}
	}
	close(tasks)

	go func() {
		wg.Wait()
		close(results)
	}()

	for msg := range results {
		resultsMap[msg.rigName] = msg.result
	}

	return resultsMap
}

// upStartWitness starts a witness for the given rig and returns a result struct.
// Respects parked/docked status - skips starting if rig is not operational.
func upStartWitness(rigName string, r *rig.Rig) agentStartResult {
	name := "Witness (" + rigName + ")"
	rigPrefix := session.PrefixFor(rigName)
	sessionID := session.WitnessSessionName(rigPrefix, rigName)

	// Check if rig is parked or docked (wisp + bead labels).
	if !r.GetBoolConfig("auto_start_on_up") && !r.GetBoolConfig("auto_start_on_boot") {
		townRoot := filepath.Dir(r.Path)
		if blocked, reason := IsRigParkedOrDocked(townRoot, rigName); blocked {
			return agentStartResult{name: name, ok: true, detail: fmt.Sprintf("skipped (rig %s)", reason)}
		}
	}

	mgr := witness.NewManager(r)
	if err := mgr.Start(false, "", nil); err != nil {
		if err == witness.ErrAlreadyRunning {
			return agentStartResult{name: name, ok: true, detail: sessionID}
		}
		return agentStartResult{name: name, ok: false, detail: err.Error()}
	}
	return agentStartResult{name: name, ok: true, detail: sessionID}
}

// upStartRefinery starts a refinery for the given rig and returns a result struct.
// Respects parked/docked status - skips starting if rig is not operational.
func upStartRefinery(rigName string, r *rig.Rig) agentStartResult {
	name := "Refinery (" + rigName + ")"
	rigPrefix := session.PrefixFor(rigName)
	sessionID := session.RefinerySessionName(rigPrefix, rigName)

	// Check if rig is parked or docked (wisp + bead labels).
	if !r.GetBoolConfig("auto_start_on_up") && !r.GetBoolConfig("auto_start_on_boot") {
		townRoot := filepath.Dir(r.Path)
		if blocked, reason := IsRigParkedOrDocked(townRoot, rigName); blocked {
			return agentStartResult{name: name, ok: true, detail: fmt.Sprintf("skipped (rig %s)", reason)}
		}
	}

	mgr := refinery.NewManager(r)
	if err := mgr.Start(false, ""); err != nil {
		if err == refinery.ErrAlreadyRunning {
			return agentStartResult{name: name, ok: true, detail: sessionID}
		}
		return agentStartResult{name: name, ok: false, detail: err.Error()}
	}
	return agentStartResult{name: name, ok: true, detail: sessionID}
}

// upStartArchitect starts an architect for the given rig and returns a result struct.
// Respects parked/docked status - skips starting if rig is not operational.
func upStartArchitect(rigName string, r *rig.Rig) agentStartResult {
	name := "Architect (" + rigName + ")"

	if !r.GetBoolConfig("auto_start_on_up") && !r.GetBoolConfig("auto_start_on_boot") {
		townRoot := filepath.Dir(r.Path)
		if blocked, reason := IsRigParkedOrDocked(townRoot, rigName); blocked {
			return agentStartResult{name: name, ok: true, detail: fmt.Sprintf("skipped (rig %s)", reason)}
		}
	}

	sessionID := session.ArchitectSessionName(session.PrefixFor(rigName), rigName)
	architectDir := filepath.Join(r.Path, constants.DirArchitect)
	if err := os.MkdirAll(architectDir, 0755); err != nil {
		return agentStartResult{name: name, ok: false, detail: err.Error()}
	}

	townRoot := filepath.Dir(r.Path)
	sp := session.GetDefaultProvider(townRoot)
	ctx := context.Background()

	if running, _ := sp.Exists(ctx, sessionID); running {
		return agentStartResult{name: name, ok: true, detail: sessionID}
	}

	_, err := session.StartSession(ctx, sp, session.SessionConfig{
		SessionID:    sessionID,
		WorkDir:      architectDir,
		Role:         constants.RoleArchitect,
		TownRoot:     townRoot,
		RigPath:      r.Path,
		RigName:      rigName,
		Beacon:       session.BeaconConfig{Recipient: "architect", Sender: "daemon", Topic: "startup"},
		WaitForAgent: true,
		WaitFatal:    true,
		ReadyDelay:   true,
		AutoRespawn:  true,
	})
	if err != nil {
		return agentStartResult{name: name, ok: false, detail: err.Error()}
	}
	return agentStartResult{name: name, ok: true, detail: sessionID}
}

// upStartQA starts a qa agent for the given rig and returns a result struct.
// Respects parked/docked status - skips starting if rig is not operational.
func upStartQA(rigName string, r *rig.Rig) agentStartResult {
	name := "QA (" + rigName + ")"

	if !r.GetBoolConfig("auto_start_on_up") && !r.GetBoolConfig("auto_start_on_boot") {
		townRoot := filepath.Dir(r.Path)
		if blocked, reason := IsRigParkedOrDocked(townRoot, rigName); blocked {
			return agentStartResult{name: name, ok: true, detail: fmt.Sprintf("skipped (rig %s)", reason)}
		}
	}

	sessionID := session.QASessionName(session.PrefixFor(rigName), rigName)
	qaDir := filepath.Join(r.Path, constants.DirQA)
	if err := os.MkdirAll(qaDir, 0755); err != nil {
		return agentStartResult{name: name, ok: false, detail: err.Error()}
	}

	townRoot := filepath.Dir(r.Path)
	sp := session.GetDefaultProvider(townRoot)
	ctx := context.Background()

	if running, _ := sp.Exists(ctx, sessionID); running {
		return agentStartResult{name: name, ok: true, detail: sessionID}
	}

	_, err := session.StartSession(ctx, sp, session.SessionConfig{
		SessionID:    sessionID,
		WorkDir:      qaDir,
		Role:         constants.RoleQA,
		TownRoot:     townRoot,
		RigPath:      r.Path,
		RigName:      rigName,
		Beacon:       session.BeaconConfig{Recipient: "qa", Sender: "daemon", Topic: "startup"},
		WaitForAgent: true,
		WaitFatal:    true,
		ReadyDelay:   true,
		AutoRespawn:  true,
	})
	if err != nil {
		return agentStartResult{name: name, ok: false, detail: err.Error()}
	}
	return agentStartResult{name: name, ok: true, detail: sessionID}
}

// upStartPlanner starts a planner and returns a result struct.
func upStartPlanner(townRoot string) agentStartResult {
	name := "Planner"
	sessionID := session.PlannerSessionName()
	plannerDir := filepath.Join(townRoot, constants.DirPlanner)
	if err := os.MkdirAll(plannerDir, 0755); err != nil {
		return agentStartResult{name: name, ok: false, detail: err.Error()}
	}

	sp := session.GetDefaultProvider(townRoot)
	ctx := context.Background()

	if running, _ := sp.Exists(ctx, sessionID); running {
		return agentStartResult{name: name, ok: true, detail: sessionID}
	}

	_, err := session.StartSession(ctx, sp, session.SessionConfig{
		SessionID:    sessionID,
		WorkDir:      plannerDir,
		Role:         constants.RolePlanner,
		TownRoot:     townRoot,
		Beacon:       session.BeaconConfig{Recipient: "planner", Sender: "daemon", Topic: "startup"},
		WaitForAgent: true,
		WaitFatal:    true,
		ReadyDelay:   true,
		AutoRespawn:  true,
	})
	if err != nil {
		return agentStartResult{name: name, ok: false, detail: err.Error()}
	}
	return agentStartResult{name: name, ok: true, detail: sessionID}
}

// discoverRigs finds all rigs in the town.
func discoverRigs(townRoot string) []string {
	var rigs []string

	// Try rigs.json first
	rigsConfigPath := filepath.Join(townRoot, "mayor", "rigs.json")
	if rigsConfig, err := config.LoadRigsConfig(rigsConfigPath); err == nil {
		for name := range rigsConfig.Rigs {
			rigs = append(rigs, name)
		}
		return rigs
	}

	// Fallback: scan directory for rig-like directories
	entries, err := os.ReadDir(townRoot)
	if err != nil {
		return rigs
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		// Skip known non-rig directories
		if name == "mayor" || name == "daemon" || name == "deacon" ||
			name == ".git" || name == "docs" || name[0] == '.' {
			continue
		}

		dirPath := filepath.Join(townRoot, name)

		// Check for .beads directory (indicates a rig)
		beadsPath := filepath.Join(dirPath, ".beads")
		if _, err := os.Stat(beadsPath); err == nil {
			rigs = append(rigs, name)
			continue
		}

		// Check for polecats directory (indicates a rig)
		polecatsPath := filepath.Join(dirPath, "polecats")
		if _, err := os.Stat(polecatsPath); err == nil {
			rigs = append(rigs, name)
		}
	}

	return rigs
}

// startCrewFromSettings starts crew members based on rig settings.
// Returns list of started crew names and map of errors.
func startCrewFromSettings(townRoot, rigName string) ([]string, map[string]error) {
	started := []string{}
	errors := map[string]error{}

	rigPath := filepath.Join(townRoot, rigName)

	// Load rig settings
	settingsPath := filepath.Join(rigPath, "settings", "config.json")
	settings, err := config.LoadRigSettings(settingsPath)
	if err != nil {
		// No settings file or error - skip crew startup
		return started, errors
	}

	if settings.Crew == nil || settings.Crew.Startup == "" {
		// No crew startup preference
		return started, errors
	}

	// Get available crew members using helper
	crewMgr, _, err := getCrewManager(rigName)
	if err != nil {
		return started, errors
	}

	crewWorkers, err := crewMgr.List()
	if err != nil {
		return started, errors
	}

	if len(crewWorkers) == 0 {
		return started, errors
	}

	// Extract crew names
	crewNames := make([]string, len(crewWorkers))
	for i, w := range crewWorkers {
		crewNames[i] = w.Name
	}

	// Parse startup preference and determine which crew to start
	toStart := parseCrewStartupPreference(settings.Crew.Startup, crewNames)

	// Start each crew member using Manager
	for _, crewName := range toStart {
		if err := crewMgr.Start(crewName, crew.StartOptions{}); err != nil {
			if err == crew.ErrSessionRunning {
				started = append(started, crewName)
			} else {
				errors[crewName] = err
			}
		} else {
			started = append(started, crewName)
		}
	}

	return started, errors
}

// parseCrewStartupPreference parses the natural language crew startup preference.
// Examples: "max", "joe and max", "all", "none", "pick one"
func parseCrewStartupPreference(pref string, available []string) []string {
	pref = strings.ToLower(strings.TrimSpace(pref))

	// Special keywords
	switch pref {
	case "none", "":
		return []string{}
	case "all":
		return available
	case "pick one", "any", "any one":
		if len(available) > 0 {
			return []string{available[0]}
		}
		return []string{}
	}

	// Parse comma/and-separated list
	// "joe and max" -> ["joe", "max"]
	// "joe, max" -> ["joe", "max"]
	// "max" -> ["max"]
	pref = strings.ReplaceAll(pref, " and ", ",")
	pref = strings.ReplaceAll(pref, ", but not ", ",-")
	pref = strings.ReplaceAll(pref, " but not ", ",-")

	parts := strings.Split(pref, ",")

	include := []string{}
	exclude := map[string]bool{}

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if strings.HasPrefix(part, "-") {
			// Exclusion
			exclude[strings.TrimPrefix(part, "-")] = true
		} else {
			include = append(include, part)
		}
	}

	// Filter to only available crew members
	result := []string{}
	for _, name := range include {
		if exclude[name] {
			continue
		}
		// Check if this crew exists
		for _, avail := range available {
			if avail == name {
				result = append(result, name)
				break
			}
		}
	}

	return result
}

// startPolecatsWithWork starts polecats that have pinned beads (work attached).
// Returns list of started polecat names and map of errors.
func startPolecatsWithWork(townRoot, rigName string) ([]string, map[string]error) {
	started := []string{}
	errors := map[string]error{}

	rigPath := filepath.Join(townRoot, rigName)
	polecatsDir := filepath.Join(rigPath, "polecats")

	// List polecat directories
	entries, err := os.ReadDir(polecatsDir)
	if err != nil {
		// No polecats directory
		return started, errors
	}

	// Get polecat session manager
	_, r, err := getRig(rigName)
	if err != nil {
		return started, errors
	}
	sp := session.GetDefaultProvider(townRoot)
	polecatMgr := polecat.NewSessionManager(sp, r)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		polecatName := entry.Name()
		polecatPath := filepath.Join(polecatsDir, polecatName)

		// Check if this polecat has a pinned bead (work attached)
		agentID := fmt.Sprintf("%s/polecats/%s", rigName, polecatName)
		b := beads.New(polecatPath)
		pinnedBeads, err := b.List(beads.ListOptions{
			Status:   beads.StatusPinned,
			Assignee: agentID,
			Priority: -1,
		})
		if err != nil || len(pinnedBeads) == 0 {
			// No pinned beads - skip
			continue
		}

		// This polecat has work - start it using SessionManager
		if err := polecatMgr.Start(polecatName, polecat.SessionStartOptions{}); err != nil {
			if err == polecat.ErrSessionRunning {
				started = append(started, polecatName)
			} else {
				errors[polecatName] = err
			}
		} else {
			started = append(started, polecatName)
		}
	}

	return started, errors
}

// doltReadyTimeout is how long gt up waits for the Dolt SQL server to accept
// connections before proceeding with witness/refinery startup. 10 seconds is
// generous: doltserver.Start() already retries for 5s, so this covers the case
// where the daemon (not gt up) started Dolt and it's still initializing.
const doltReadyTimeout = 10 * time.Second

// waitForDoltReady waits for the Dolt SQL server to be reachable before
// starting agents that depend on beads database access. If the server is not
// configured (no server-mode metadata), this is a no-op. If the timeout
// expires, logs a warning and continues (graceful degradation). (gt-zou1n)
func waitForDoltReady(townRoot string) {
	if err := doltserver.WaitForReady(townRoot, doltReadyTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v (agents may see connection errors)\n", err)
	}
}

// recoverOrphanedBeads scans each rig for beads stuck in hooked/in_progress
// status assigned to polecats that no longer exist (tmux session dead AND
// worktree directory removed). For each orphan, the bead is reset to open
// and the deacon is notified for re-dispatch.
//
// This runs during gt up after Dolt is ready, before witnesses start their
// own patrol. It catches the crash-recovery case where polecats die and
// their beads are never re-slung. (gas-udp)
func recoverOrphanedBeads(townRoot string, rigs []string, prefetchedRigs map[string]*rig.Rig) []ServiceStatus {
	var services []ServiceStatus

	bd := witness.DefaultBdCli()
	router := mail.NewRouterWithTownRoot(townRoot, townRoot)

	for _, rigName := range rigs {
		if _, ok := prefetchedRigs[rigName]; !ok {
			fmt.Fprintf(os.Stderr, "[orphan-recovery] skipping rig %s (failed to load)\n", rigName)
			continue // Rig failed to load — skip
		}

		rigPath := filepath.Join(townRoot, rigName)
		result := witness.DetectOrphanedBeads(bd, rigPath, rigName, router)

		if len(result.Orphans) == 0 {
			continue // No orphans in this rig
		}

		recovered := 0
		for _, orphan := range result.Orphans {
			if orphan.BeadRecovered {
				recovered++
			}
		}

		detail := fmt.Sprintf("found %d orphaned, recovered %d", len(result.Orphans), recovered)
		services = append(services, ServiceStatus{
			Name:   fmt.Sprintf("Orphan recovery (%s)", rigName),
			Type:   "recovery",
			Rig:    rigName,
			OK:     true,
			Detail: detail,
		})
	}

	// Flush any pending mail notifications before proceeding.
	router.WaitPendingNotifications()

	return services
}

// upStartMechanic starts a mechanic and returns a result struct.
func upStartMechanic(townRoot string) agentStartResult {
	name := "Mechanic"
	sessionID := session.MechanicSessionName()
	mechanicDir := filepath.Join(townRoot, constants.DirMechanic)
	if err := os.MkdirAll(mechanicDir, 0755); err != nil {
		return agentStartResult{name: name, ok: false, detail: err.Error()}
	}

	sp := session.GetDefaultProvider(townRoot)
	ctx := context.Background()

	if running, _ := sp.Exists(ctx, sessionID); running {
		return agentStartResult{name: name, ok: true, detail: sessionID}
	}

	_, err := session.StartSession(ctx, sp, session.SessionConfig{
		SessionID:    sessionID,
		WorkDir:      mechanicDir,
		Role:         constants.RoleMechanic,
		TownRoot:     townRoot,
		Beacon:       session.BeaconConfig{Recipient: "mechanic", Sender: "daemon", Topic: "startup"},
		WaitForAgent: true,
		WaitFatal:    true,
		ReadyDelay:   true,
		AutoRespawn:  true,
	})
	if err != nil {
		return agentStartResult{name: name, ok: false, detail: err.Error()}
	}

	return agentStartResult{name: name, ok: true, detail: sessionID}
}
