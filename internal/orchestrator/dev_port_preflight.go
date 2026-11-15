package orchestrator

import (
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ForeignPortListener describes a TCP listener on a port required by a rig
// workflow profile whose owning process does not belong to the rig.
type ForeignPortListener struct {
	Port  int
	Owner string
}

// FindForeignDevPortListeners performs a deterministic pre-flight check of the
// TCP ports a rig workflow requires (dev_server_port plus localhost ports
// referenced by QA verify commands). A port is usable only if it is free or
// held exclusively by processes running inside the rig directory. Listeners
// owned by anything else — including unidentifiable root/other-user listeners
// such as Docker port proxies, which lsof cannot attribute without root — are
// returned as foreign so the caller can pause the workflow instead of letting
// verification loop against a wrong server and burn the execution budget.
func FindForeignDevPortListeners(v WorkflowValidation, rigDir string) []ForeignPortListener {
	if rigDir == "" {
		return nil
	}
	root, err := filepath.EvalSymlinks(rigDir)
	if err != nil {
		return nil
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil
	}
	tr := buildDevServerTracker(v, rigDir)
	var conflicts []ForeignPortListener
	for port := range tr.ports {
		if l := classifyPortOwner(port, root); l != nil {
			conflicts = append(conflicts, *l)
		}
	}
	return conflicts
}

// classifyPortOwner reports a foreign listener on port, or nil when the port is
// free or held only by processes whose cwd is inside the rig root.
func classifyPortOwner(port int, rigRoot string) *ForeignPortListener {
	if port < 1 || port > 65535 {
		return nil
	}
	// Bind probe mirrors what the rig dev server does (Go listens with
	// SO_REUSEADDR, so TIME_WAIT sockets do not cause false positives).
	probe, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err == nil {
		_ = probe.Close()
		return nil
	}
	owner, foreign := foreignOwnerDesc(port, rigRoot)
	if !foreign {
		return nil
	}
	return &ForeignPortListener{Port: port, Owner: owner}
}

// foreignOwnerDesc best-effort identifies who holds the port. It reports
// (ownerDescription, true) when a listener is not owned by the rig, and
// (\"\", false) when every visible listener runs inside rigRoot. An occupied
// port with no attributable listener at all — a root/other-user owner such as
// docker-proxy, invisible to lsof without root — is treated as foreign.
func foreignOwnerDesc(port int, rigRoot string) (string, bool) {
	lsof, err := exec.LookPath("lsof")
	if err != nil {
		return "unidentified listener (lsof unavailable)", true
	}
	pids := lsofPIDsOnTCPPort(lsof, port)
	var foreign []string
	for _, pid := range pids {
		if rigOwnedPID(lsof, pid, rigRoot) {
			continue
		}
		foreign = append(foreign, describePID(lsof, pid))
	}
	if len(pids) > 0 && len(foreign) == 0 {
		return "", false
	}
	return "unidentified listener (not owned by rig)", true
}

// rigOwnedPID reports whether the process cwd resolves inside rigRoot.
func rigOwnedPID(lsof, pid, rigRoot string) bool {
	out, err := exec.Command(lsof, "-a", "-p", pid, "-d", "cwd", "-Fn").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "n") {
			continue
		}
		cwd, err := filepath.EvalSymlinks(strings.TrimPrefix(line, "n"))
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(rigRoot, cwd)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return true
		}
	}
	return false
}

// describePID builds a short human-readable owner description.
func describePID(lsof, pid string) string {
	out, err := exec.Command(lsof, "-a", "-p", pid, "-d", "cwd", "-Fn").Output()
	cwd := ""
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "n") {
				cwd = strings.TrimPrefix(line, "n")
				break
			}
		}
	}
	if cwd == "" {
		return fmt.Sprintf("pid %s", pid)
	}
	return fmt.Sprintf("pid %s cwd=%s", pid, cwd)
}
