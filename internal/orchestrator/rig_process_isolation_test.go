package orchestrator

import (
	"bufio"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRigCleanupPreservesOtherRig(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof required")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 required")
	}
	start := func(dir string) int {
		cmd := exec.Command("python3", "-u", "-c", `import socket,time
s=socket.socket();s.bind(('127.0.0.1',0));s.listen();print(s.getsockname()[1]);time.sleep(60)`)
		cmd.Dir = dir
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		line, err := bufio.NewReader(out).ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		port, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			t.Fatal(err)
		}
		return port
	}
	a, b := t.TempDir(), t.TempDir()
	portA, portB := start(a), start(b)
	if pids, err := KillRigTCPListeners(portB, a); err != nil || len(pids) != 0 {
		t.Fatalf("foreign process selected: %v %v", pids, err)
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(portB)), time.Second)
	if err != nil {
		t.Fatalf("other rig stopped: %v", err)
	}
	conn.Close()
	if pids, err := KillRigTCPListeners(portA, a); err != nil || len(pids) == 0 {
		t.Fatalf("own process not selected: %v %v", pids, err)
	}
}
