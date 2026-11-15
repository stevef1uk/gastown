package orchestrator

import (
	"net"
	"os/exec"
	"strconv"
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
