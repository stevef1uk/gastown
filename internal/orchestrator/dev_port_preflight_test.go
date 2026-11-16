package orchestrator

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// startListenerWithDir starts a python TCP listener and returns its port.
// The process cwd is dir.
func startListenerWithDir(t *testing.T, dir string) int {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 required")
	}
	const holderPy = `
import socket, sys, time
s = socket.socket()
s.bind(("127.0.0.1", 0))
port = s.getsockname()[1]
sys.stdout.write(str(port) + "\n")
sys.stdout.flush()
s.listen(8)
time.sleep(3600)
`
	cmd := exec.Command("python3", "-c", holderPy)
	cmd.Dir = dir
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	line := make([]byte, 32)
	n, err := out.Read(line)
	if err != nil || n == 0 {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(string(line[:n-1]))
	if err != nil || port < 1 {
		t.Fatal(err)
	}
	return port
}

// waitPortOccupied polls until the bind probe reports the port as occupied.
func waitPortOccupied(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if occupied := portOccupied(port); occupied {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("port %d never became occupied", port)
}

func portOccupied(port int) bool {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}

func TestFindForeignDevPortListeners_foreignListenerOutsideRig(t *testing.T) {
	rig := t.TempDir()
	outside := t.TempDir()
	port := startListenerWithDir(t, outside)
	waitPortOccupied(t, port)

	v := WorkflowValidation{DevServerPort: port}
	conflicts := FindForeignDevPortListeners(v, rig)
	if len(conflicts) == 0 {
		t.Fatalf("expected foreign conflict on port %d", port)
	}
	if conflicts[0].Port != port {
		t.Fatalf("conflict port = %d, want %d", conflicts[0].Port, port)
	}
	if conflicts[0].Owner == "" {
		t.Fatal("owner description empty")
	}
}

func TestFindForeignDevPortListeners_rigOwnedListenerAllowed(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof required to attribute the rig-owned listener")
	}
	rig := t.TempDir()
	port := startListenerWithDir(t, rig)
	waitPortOccupied(t, port)

	v := WorkflowValidation{DevServerPort: port}
	if conflicts := FindForeignDevPortListeners(v, rig); len(conflicts) != 0 {
		t.Fatalf("expected no conflict for rig-owned listener, got %+v", conflicts)
	}
}

func TestFindForeignDevPortListeners_protectedPortSkipped(t *testing.T) {
	rig := t.TempDir()
	v := WorkflowValidation{DevServerPort: 3307}
	if conflicts := FindForeignDevPortListeners(v, rig); len(conflicts) != 0 {
		t.Fatalf("protected port must never be reported, got %+v", conflicts)
	}
}

func composePortFixture(t *testing.T, dir string) devPortContainer {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(config, []byte("services: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var c devPortContainer
	c.Name = "/finally-app-1"
	c.State.Running = true
	c.Config.Labels = map[string]string{
		"com.docker.compose.project":              "finally",
		"com.docker.compose.service":              "app",
		"com.docker.compose.project.working_dir":  dir,
		"com.docker.compose.project.config_files": config,
	}
	c.NetworkSettings.Ports = map[string][]struct{ HostPort string }{"3000/tcp": {{HostPort: "8000"}}}
	return c
}

func TestDevPortComposeOwnership(t *testing.T) {
	root := filepath.Join(t.TempDir(), "rig")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	for _, tc := range []struct {
		name   string
		change func(*devPortContainer)
		owned  bool
	}{
		{"rig-owned mapped host port", func(c *devPortContainer) {}, true},
		{"same container name different rig", func(c *devPortContainer) { *c = composePortFixture(t, outside) }, false},
		{"unlabelled container", func(c *devPortContainer) { c.Config.Labels = nil }, false},
		{"container port alone is not host port", func(c *devPortContainer) {
			c.NetworkSettings.Ports = map[string][]struct{ HostPort string }{"8000/tcp": {{HostPort: "9000"}}}
		}, false},
		{"udp is not tcp", func(c *devPortContainer) {
			c.NetworkSettings.Ports = map[string][]struct{ HostPort string }{"3000/udp": {{HostPort: "8000"}}}
		}, false},
		{"stopped container", func(c *devPortContainer) { c.State.Running = false }, false},
		{"compose config outside rig", func(c *devPortContainer) {
			other := composePortFixture(t, outside)
			c.Config.Labels["com.docker.compose.project.config_files"] = other.Config.Labels["com.docker.compose.project.config_files"]
		}, false},
		{"relative rig-local config", func(c *devPortContainer) {
			c.Config.Labels["com.docker.compose.project.config_files"] = "docker-compose.yml"
		}, true},
		{"sibling path prefix", func(c *devPortContainer) { *c = composePortFixture(t, root+"-other") }, false},
		{"symlink escapes rig", func(c *devPortContainer) {
			other := composePortFixture(t, outside)
			link := filepath.Join(root, "external.yml")
			if err := os.Symlink(other.Config.Labels["com.docker.compose.project.config_files"], link); err != nil {
				t.Fatal(err)
			}
			c.Config.Labels["com.docker.compose.project.config_files"] = link
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := composePortFixture(t, filepath.Join(root, "finally"))
			tc.change(&c)
			data, err := json.Marshal([]devPortContainer{c})
			if err != nil {
				t.Fatal(err)
			}
			_, owned := classifyDevPortContainers(data, 8000, root)
			if owned != tc.owned {
				t.Fatalf("owned=%v want %v: %s", owned, tc.owned, data)
			}
		})
	}
	owned := composePortFixture(t, filepath.Join(root, "finally"))
	foreign := composePortFixture(t, outside)
	data, _ := json.Marshal([]devPortContainer{owned, foreign})
	if desc, ok := classifyDevPortContainers(data, 8000, root); ok || !strings.Contains(desc, "Docker container") {
		t.Fatalf("mixed ownership accepted: %q %v", desc, ok)
	}
	if _, ok := classifyDevPortContainers([]byte("invalid JSON"), 8000, root); ok {
		t.Fatal("invalid inspect accepted")
	}
}

func stubComposePortDocker(t *testing.T, container devPortContainer) string {
	t.Helper()
	dir := t.TempDir()
	data, err := json.Marshal([]devPortContainer{container})
	if err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\ncase \"$1\" in\nps) printf '%s\\n' fixture-container ;;\ninspect) printf '%s\\n' '" + strings.ReplaceAll(string(data), "'", "'\\''") + "' ;;\n*) exit 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDevPortComposeFallbackWithoutLsof(t *testing.T) {
	root := t.TempDir()
	c := composePortFixture(t, filepath.Join(root, "finally"))
	port := startListenerWithDir(t, t.TempDir())
	c.NetworkSettings.Ports = map[string][]struct{ HostPort string }{"3000/tcp": {{HostPort: strconv.Itoa(port)}}}
	t.Setenv("PATH", stubComposePortDocker(t, c))
	if conflicts := FindForeignDevPortListeners(WorkflowValidation{DevServerPort: port}, root); len(conflicts) != 0 {
		t.Fatalf("rig Compose proxy treated as foreign: %+v", conflicts)
	}
}

func TestDevPortComposeDoesNotHideVisibleForeignProcess(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof required")
	}
	root := t.TempDir()
	c := composePortFixture(t, filepath.Join(root, "finally"))
	port := startListenerWithDir(t, t.TempDir())
	c.NetworkSettings.Ports = map[string][]struct{ HostPort string }{"3000/tcp": {{HostPort: strconv.Itoa(port)}}}
	t.Setenv("PATH", stubComposePortDocker(t, c)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if conflicts := FindForeignDevPortListeners(WorkflowValidation{DevServerPort: port}, root); len(conflicts) == 0 {
		t.Fatal("Docker labels hid a visible foreign host listener")
	}
}
