package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
// held exclusively by processes running inside the rig directory or containers
// with verified rig-local Compose labels and matching published TCP ports.
// Listeners owned by anything else, including unattributable listeners, are
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
// docker-proxy, invisible to lsof without root — may instead be attributed by
// Docker's actual port bindings and Compose project paths.
func foreignOwnerDesc(port int, rigRoot string) (string, bool) {
	lsof, err := exec.LookPath("lsof")
	if err != nil {
		if _, owned := dockerPortOwnedByRig(port, rigRoot); owned {
			return "", false
		}
		return "unidentified listener (lsof unavailable)", true
	}
	pids := lsofPIDsOnTCPPort(lsof, port)
	var foreign []string
	proxiesOnly := true
	for _, pid := range pids {
		if rigOwnedPID(lsof, pid, rigRoot) {
			continue
		}
		foreign = append(foreign, describePID(lsof, pid))
		if !dockerPortProxyPID(pid) {
			proxiesOnly = false
		}
	}
	if len(pids) > 0 && len(foreign) == 0 {
		return "", false
	}
	if proxiesOnly {
		if owner, owned := dockerPortOwnedByRig(port, rigRoot); owned {
			return "", false
		} else if owner != "" {
			return owner, true
		}
	}
	if len(foreign) > 0 {
		return strings.Join(foreign, "; "), true
	}
	return "unidentified listener (not owned by rig)", true
}

func dockerPortProxyPID(pid string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-p", pid, "-o", "comm=").Output()
	if err != nil {
		return false
	}
	switch filepath.Base(strings.TrimSpace(string(out))) {
	case "docker-proxy", "rootlesskit", "com.docker.backend", "vpnkit":
		return true
	}
	return false
}

type devPortContainer struct {
	Name            string
	State           struct{ Running bool }
	Config          struct{ Labels map[string]string }
	NetworkSettings struct {
		Ports map[string][]struct{ HostPort string }
	}
}

// dockerPortOwnedByRig uses read-only, bounded queries. Docker's publish filter
// can match the container port rather than the host port, so inspect and compare
// HostPort explicitly. Never infer ownership from a name such as "finally".
func dockerPortOwnedByRig(port int, rigRoot string) (string, bool) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, docker, "ps", "--quiet", "--filter", "publish="+strconv.Itoa(port)).Output()
	if err != nil {
		return "", false
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return "", false
	}
	args := append([]string{"inspect", "--type", "container"}, ids...)
	out, err = exec.CommandContext(ctx, docker, args...).Output()
	if err != nil {
		return "", false
	}
	return classifyDevPortContainers(out, port, rigRoot)
}

func classifyDevPortContainers(data []byte, port int, rigRoot string) (string, bool) {
	var containers []devPortContainer
	if err := json.Unmarshal(data, &containers); err != nil {
		return "", false
	}
	matched := false
	for _, container := range containers {
		publishes := false
		for target, bindings := range container.NetworkSettings.Ports {
			if !strings.HasSuffix(target, "/tcp") {
				continue
			}
			for _, binding := range bindings {
				if binding.HostPort == strconv.Itoa(port) {
					publishes = true
				}
			}
		}
		if !container.State.Running || !publishes {
			continue
		}
		matched = true
		labels := container.Config.Labels
		workingDir := labels["com.docker.compose.project.working_dir"]
		configFiles := labels["com.docker.compose.project.config_files"]
		owned := labels["com.docker.compose.project"] != "" && labels["com.docker.compose.service"] != "" &&
			pathWithinDevPortRig(rigRoot, workingDir) && configFiles != ""
		for _, config := range strings.Split(configFiles, ",") {
			config = strings.TrimSpace(config)
			if !filepath.IsAbs(config) {
				config = filepath.Join(workingDir, config)
			}
			owned = owned && pathWithinDevPortRig(rigRoot, config)
		}
		if !owned {
			return "Docker container " + strings.TrimPrefix(container.Name, "/") + " (Compose project outside rig or unidentified)", false
		}
	}
	return "", matched
}

func pathWithinDevPortRig(root, candidate string) bool {
	if candidate == "" || !filepath.IsAbs(candidate) {
		return false
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, resolved)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
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
