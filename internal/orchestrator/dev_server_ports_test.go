package orchestrator

import (
	"net"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestKillTCPListenersOnPort_freesListener(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof required")
	}
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
	addr := "127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := KillTCPListenersOnPort(port); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			ln.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("port %d still in use", port)
}
