// dolt_zap_orphans.go — detect and kill orphan dolt sql-server processes.
//
// Background: every `bd` invocation can spawn a short-lived dolt sql-server
// on a random localhost port. Normally these exit cleanly when bd exits,
// but a crash / SIGKILL / parent-shell-exit leaks them. Over a long session
// dozens accumulate; they hold file locks and rack up open FDs and memory.
//
// This file adds `gt dolt zap-orphans` — a deterministic, side-effect-only
// command that walks /proc, classifies every `dolt sql-server` process,
// and SIGTERMs (then SIGKILLs) the orphans.
//
// SAFETY (in priority order):
//   1. Canonical preserve. If state.json records a pid and that pid is
//      still alive and is the dolt server, never touch it.
//   2. Canonical-port preserve. Belt-and-braces guard for the case where
//      state.json is stale: any dolt server listening on the configured
//      port (default 3307) is ALSO preserved.
//   3. Active-parent preserve. A dolt whose parent process is alive and
//      whose parent's comm is `bd` (or whitelisted equivalents) is an
//      in-flight ephemeral server — keep it.
//   4. Everything else with comm=="dolt" and `dolt sql-server` in argv
//      is an orphan and gets reaped.
//
// We NEVER `rm -rf` any data dir. Killing a SIGTERM'd dolt is safe per
// dolt's own shutdown path; the database files are flushed and the
// `.dolt/` directory is left untouched on disk for whatever creator
// process to recover on its next run.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/doltserver"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	doltZapOrphansDry  bool
	doltZapOrphansJSON bool
)

var doltZapOrphansCmd = &cobra.Command{
	Use:   "zap-orphans",
	Short: "Kill leaked dolt sql-server processes (preserves canonical server)",
	Long: `Detect and reap orphan dolt sql-server processes.

An "orphan" is any dolt sql-server that is NOT:
  - the canonical server recorded in state.json,
  - listening on the workspace's configured port,
  - currently a child of a live bd / gt process.

This is the long-tail cleanup for ephemeral bd-spawned servers that
crashed without taking their dolt child with them. Over a long session
they accumulate (~1 per crashed bd invocation) and starve the system
of file descriptors and memory.

This command is safe to run at any time. It NEVER touches data files
in .dolt-data/ — only sends SIGTERM (then SIGKILL after 3s) to the
identified PIDs.

The Mechanic agent runs this every patrol cycle.

Examples:
  gt dolt zap-orphans            # Reap orphans
  gt dolt zap-orphans --dry-run  # List what would be killed
  gt dolt zap-orphans --json     # Machine-readable output`,
	RunE: runDoltZapOrphans,
}

// doltProcInfo captures the snapshot of one /proc/<pid> entry we care
// about when classifying. Pure data — no live FDs.
type doltProcInfo struct {
	PID       int
	PPID      int
	Comm      string // /proc/<pid>/comm — typically "dolt"
	Cmdline   string // /proc/<pid>/cmdline (NUL-separated, joined with spaces)
	Port      int    // parsed from `-P <num>` in cmdline; 0 if absent
	ParentCmd string // /proc/<ppid>/comm (or empty if parent gone)
}

// doltProcClassification is the decision the orphan-reaper makes for
// each candidate.
type doltProcClassification int

const (
	classOrphan doltProcClassification = iota
	classPreserveCanonicalPID
	classPreserveCanonicalPort
	classPreserveActiveParent
)

func (c doltProcClassification) String() string {
	switch c {
	case classOrphan:
		return "orphan"
	case classPreserveCanonicalPID:
		return "preserve-canonical-pid"
	case classPreserveCanonicalPort:
		return "preserve-canonical-port"
	case classPreserveActiveParent:
		return "preserve-active-parent"
	default:
		return "unknown"
	}
}

// activeParentAllowlist names process commands that legitimately spawn
// short-lived dolt sql-servers as children. A dolt whose parent is on
// this list and is still alive is in-flight, not orphaned.
//
// Keep this list narrow — anything else (bash, ssh, init, an exited
// shell, etc.) means we can't prove the dolt is still in use, so we
// reap it. This is conservative: a real false positive would just
// restart the next time bd reruns.
var activeParentAllowlist = map[string]bool{
	"bd":       true,
	"gt":       true,
	"gt-agent": true,
}

// classifyDoltProc is the pure-decision function. Given a snapshot of
// /proc data and the canonical pid/port, return what to do. Exported
// in lower-case for the test file in the same package.
//
// canonicalPID may be 0 when the server is not running (we still apply
// canonicalPort as a belt-and-braces guard).
// canonicalPort is always >0 for a local dolt config.
func classifyDoltProc(p doltProcInfo, canonicalPID, canonicalPort int) doltProcClassification {
	if canonicalPID > 0 && p.PID == canonicalPID {
		return classPreserveCanonicalPID
	}
	if canonicalPort > 0 && p.Port == canonicalPort {
		return classPreserveCanonicalPort
	}
	if p.ParentCmd != "" && activeParentAllowlist[p.ParentCmd] {
		return classPreserveActiveParent
	}
	return classOrphan
}

// findDoltProcs walks /proc and returns one doltProcInfo per running
// `dolt sql-server` process. Non-Linux systems return an empty slice
// with a nil error (the command is then a no-op on macOS / Windows).
//
// Errors reading individual /proc entries are swallowed — a stale or
// races-out-from-under-us pid is exactly what we expect when scanning.
func findDoltProcs() ([]doltProcInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		// /proc not available (macOS, Windows, sandboxed Linux) — no
		// orphans to reap by definition.
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading /proc: %w", err)
	}

	var procs []doltProcInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		comm := readProcFile(pid, "comm")
		if strings.TrimSpace(comm) != "dolt" {
			continue
		}

		cmdline := readProcCmdline(pid)
		if !strings.Contains(cmdline, "sql-server") {
			continue
		}

		info := doltProcInfo{
			PID:     pid,
			Comm:    strings.TrimSpace(comm),
			Cmdline: cmdline,
			Port:    parsePortFromCmdline(cmdline),
		}

		// PPid lives in /proc/<pid>/status as "PPid:\t<n>".
		info.PPID = readPPid(pid)
		if info.PPID > 0 {
			info.ParentCmd = strings.TrimSpace(readProcFile(info.PPID, "comm"))
		}

		procs = append(procs, info)
	}
	return procs, nil
}

func readProcFile(pid int, name string) string {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), name))
	if err != nil {
		return ""
	}
	return string(data)
}

// readProcCmdline reads /proc/<pid>/cmdline (NUL-separated argv) and
// joins it with single spaces for easy substring matching.
func readProcCmdline(pid int) string {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return ""
	}
	// Replace NUL with space; trim trailing NUL.
	s := strings.TrimRight(string(data), "\x00")
	return strings.ReplaceAll(s, "\x00", " ")
}

// readPPid extracts the parent PID from /proc/<pid>/status. Returns 0
// on any error; 0 also means "unknown" to the caller.
func readPPid(pid int) int {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PPid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if n, err := strconv.Atoi(fields[1]); err == nil {
					return n
				}
			}
			return 0
		}
	}
	return 0
}

// parsePortFromCmdline pulls the port number out of a dolt-server
// argv string that contains `-P <num>` (with the space mandatory).
// Returns 0 when no port flag is present.
func parsePortFromCmdline(cmdline string) int {
	fields := strings.Fields(cmdline)
	for i, f := range fields {
		if (f == "-P" || f == "--port") && i+1 < len(fields) {
			if n, err := strconv.Atoi(fields[i+1]); err == nil {
				return n
			}
		}
	}
	return 0
}

// killSafely sends SIGTERM to pid, waits a bit, then SIGKILLs if the
// process is still alive. Returns true if the process is gone at exit.
//
// Errors from kill(2) on already-dead processes are swallowed — that's
// the success path.
func killSafely(pid int, gracePeriod time.Duration) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return true // FindProcess never errors on Linux; defensive
	}
	_ = proc.Signal(syscall.SIGTERM)
	deadline := time.Now().Add(gracePeriod)
	for time.Now().Before(deadline) {
		if !processIsAlive(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !processIsAlive(pid) {
		return true
	}
	_ = proc.Signal(syscall.SIGKILL)
	// Give the kernel a moment to reap.
	for i := 0; i < 10; i++ {
		if !processIsAlive(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !processIsAlive(pid)
}

// processIsAlive is reimplemented here (vs the one in doltserver) so
// this command has no extra dependencies that complicate testing on
// non-Linux hosts.
func processIsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); err == nil {
		return true
	}
	// Fallback: signal 0 — same trick as `kill -0 <pid>`.
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// reportRecord is the user-facing summary line for one decision.
type reportRecord struct {
	PID            int                    `json:"pid"`
	Port           int                    `json:"port"`
	PPID           int                    `json:"ppid"`
	ParentCmd      string                 `json:"parent_cmd"`
	Classification string                 `json:"classification"`
	Killed         bool                   `json:"killed"`
	_              doltProcClassification // (kept for clarity in tests)
}

func runDoltZapOrphans(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	config := doltserver.DefaultConfig(townRoot)
	canonicalPort := config.Port
	canonicalPID := 0
	if state, err := doltserver.LoadState(townRoot); err == nil && state != nil {
		// Treat the recorded pid as canonical only if the process is
		// actually still alive.
		if processIsAlive(state.PID) {
			canonicalPID = state.PID
		}
	}

	procs, err := findDoltProcs()
	if err != nil {
		return err
	}

	var preserved, orphans []doltProcInfo
	for _, p := range procs {
		cls := classifyDoltProc(p, canonicalPID, canonicalPort)
		if cls == classOrphan {
			orphans = append(orphans, p)
		} else {
			preserved = append(preserved, p)
		}
	}

	if doltZapOrphansJSON {
		return printZapJSON(canonicalPID, canonicalPort, preserved, orphans, doltZapOrphansDry)
	}

	fmt.Printf("Dolt orphan reap — canonical PID=%d port=%d\n", canonicalPID, canonicalPort)
	fmt.Printf("  Total dolt sql-server processes: %d\n", len(procs))
	fmt.Printf("  Preserve: %d\n", len(preserved))
	for _, p := range preserved {
		cls := classifyDoltProc(p, canonicalPID, canonicalPort)
		fmt.Printf("    %s pid=%d port=%d ppid=%d(%s) — %s\n",
			style.Bold.Render("✓"), p.PID, p.Port, p.PPID, p.ParentCmd, cls)
	}
	fmt.Printf("  Orphans: %d\n", len(orphans))
	for _, p := range orphans {
		fmt.Printf("    %s pid=%d port=%d ppid=%d(%s)\n",
			style.Warning.Render("→"), p.PID, p.Port, p.PPID, p.ParentCmd)
	}

	if doltZapOrphansDry {
		fmt.Printf("\n%s Dry-run — no kills\n", style.Warning.Render("~"))
		return nil
	}
	if len(orphans) == 0 {
		fmt.Printf("\n%s Nothing to do\n", style.Bold.Render("✓"))
		return nil
	}

	killed := 0
	for _, p := range orphans {
		if killSafely(p.PID, 3*time.Second) {
			killed++
			fmt.Printf("  %s killed pid=%d\n", style.Bold.Render("✓"), p.PID)
		} else {
			fmt.Printf("  %s failed to kill pid=%d (still alive)\n",
				style.Warning.Render("✗"), p.PID)
		}
	}
	fmt.Printf("\n%s Killed %d/%d orphan(s)\n",
		style.Bold.Render("✓"), killed, len(orphans))
	return nil
}

func printZapJSON(canonicalPID, canonicalPort int, preserved, orphans []doltProcInfo, dryRun bool) error {
	// Minimal JSON without importing encoding/json — keeps this file
	// dependency-light and consistent with the rest of internal/cmd's
	// hand-rolled JSON output (see existing `--json` flag handlers).
	fmt.Printf("{\n")
	fmt.Printf("  \"canonical_pid\": %d,\n", canonicalPID)
	fmt.Printf("  \"canonical_port\": %d,\n", canonicalPort)
	fmt.Printf("  \"dry_run\": %t,\n", dryRun)
	fmt.Printf("  \"preserved\": [")
	for i, p := range preserved {
		if i > 0 {
			fmt.Printf(", ")
		}
		cls := classifyDoltProc(p, canonicalPID, canonicalPort)
		fmt.Printf("{\"pid\":%d,\"port\":%d,\"ppid\":%d,\"parent_cmd\":%q,\"classification\":%q}",
			p.PID, p.Port, p.PPID, p.ParentCmd, cls.String())
	}
	fmt.Printf("],\n")
	fmt.Printf("  \"orphans\": [")
	for i, p := range orphans {
		if i > 0 {
			fmt.Printf(", ")
		}
		killed := !dryRun && !processIsAlive(p.PID)
		fmt.Printf("{\"pid\":%d,\"port\":%d,\"ppid\":%d,\"parent_cmd\":%q,\"classification\":\"orphan\",\"killed\":%t}",
			p.PID, p.Port, p.PPID, p.ParentCmd, killed)
	}
	fmt.Printf("]\n}\n")
	return nil
}

func init() {
	doltCmd.AddCommand(doltZapOrphansCmd)
	doltZapOrphansCmd.Flags().BoolVar(&doltZapOrphansDry, "dry-run", false,
		"List candidates without sending signals")
	doltZapOrphansCmd.Flags().BoolVar(&doltZapOrphansJSON, "json", false,
		"Machine-readable JSON output")
}
