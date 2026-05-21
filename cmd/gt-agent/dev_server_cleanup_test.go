package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestDevServerTracker_noteCommand_ports(t *testing.T) {
	t.Parallel()
	tr := newDevServerTracker()
	tr.noteCommand(`go run ./cmd/server & sleep 1 && curl -s http://localhost:8080/ && curl http://127.0.0.1:8080/api/foo`)
	if !tr.goRunSeen {
		t.Fatal("expected goRunSeen")
	}
	if _, ok := tr.ports[8080]; !ok {
		t.Fatalf("expected port 8080 tracked, got %v", tr.ports)
	}
	if _, ok := tr.ports[3307]; ok {
		t.Fatal("must not track protected Dolt port")
	}
}

func TestDevServerTracker_noteCommand_envPort(t *testing.T) {
	t.Parallel()
	tr := newDevServerTracker()
	tr.noteCommand(`PORT=9090 go run ./cmd/server`)
	if _, ok := tr.ports[9090]; !ok {
		t.Fatalf("expected port 9090, got %v", tr.ports)
	}
}

func TestDevServerTracker_needsCleanup(t *testing.T) {
	t.Parallel()
	if newDevServerTracker().needsCleanup() {
		t.Fatal("empty tracker should not need cleanup")
	}
	tr := newDevServerTracker()
	tr.noteCommand("go build ./...")
	if tr.needsCleanup() {
		t.Fatal("go build alone should not trigger cleanup")
	}
	tr.noteCommand("go run ./cmd/server")
	if !tr.needsCleanup() {
		t.Fatal("go run should trigger cleanup")
	}
}

func TestCommandStartsDevServer(t *testing.T) {
	t.Parallel()
	if !commandStartsDevServer(`cd x && go run ./cmd/server & curl`) {
		t.Fatal("expected go run")
	}
	if commandStartsDevServer("go build ./...") {
		t.Fatal("go build should not trigger")
	}
}

func TestBuildStaleDevServerTracker_fromQACommand(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mayor := filepath.Join(dir, "linkshelf", "cmd", "server")
	if err := os.MkdirAll(mayor, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mayor, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go run ./cmd/server & curl -sf http://127.0.0.1:8080/",
	}
	tr := buildStaleDevServerTracker(v, dir)
	if !tr.goRunSeen {
		t.Fatal("expected goRunSeen from QA command and server main")
	}
	if _, ok := tr.ports[8080]; !ok {
		t.Fatalf("expected port 8080 from QA command, got %v", tr.ports)
	}
}

func TestBuildStaleDevServerTracker_noServerMain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
	}
	tr := buildStaleDevServerTracker(v, dir)
	if tr.needsCleanup() {
		t.Fatalf("expected no cleanup without server main, got ports=%v goRun=%v", tr.ports, tr.goRunSeen)
	}
}

func TestFreeDevServersBeforeCommand_skipsGoBuild(t *testing.T) {
	t.Parallel()
	port, stop := startSubprocessPortHolder(t)
	defer stop()
	freeDevServersBeforeCommand("go build ./...")
	if err := dialPort(port); err != nil {
		t.Fatalf("go build must not kill holder: %v", err)
	}
}

func TestFreeDevServersBeforeCommand_freesPortInCommand(t *testing.T) {
	requirePortCleanupTools(t)
	port, stop := startSubprocessPortHolder(t)
	defer stop()
	cmd := fmt.Sprintf("go run ./cmd/server & curl -sf http://127.0.0.1:%d/", port)
	freeDevServersBeforeCommand(cmd)
	waitPortFree(t, port)
}

func TestKillTCPListeners_freesEphemeralPort(t *testing.T) {
	requirePortCleanupTools(t)
	port, stop := startSubprocessPortHolder(t)
	defer stop()
	killTCPListeners(port)
	waitPortFree(t, port)
}

func TestStopRigDevServersScript_freesPort(t *testing.T) {
	script := filepath.Join("..", "..", "scripts", "stop-rig-dev-servers.sh")
	if _, err := os.Stat(script); err != nil {
		t.Skip("stop-rig-dev-servers.sh not found from test cwd")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash required")
	}
	requirePortCleanupTools(t)
	port, stop := startSubprocessPortHolder(t)
	defer stop()
	cmd := exec.Command("bash", script, strconv.Itoa(port))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}
	waitPortFree(t, port)
}

func TestKillTCPListeners_skipsProtectedPort(t *testing.T) {
	t.Parallel()
	// Must not panic or kill real Dolt; protected ports return early.
	killTCPListeners(3307)
}

// portHolderPy binds 127.0.0.1:0 in a child process so fuser -k does not kill the test binary.
const portHolderPy = `
import socket, sys, time
s = socket.socket()
s.bind(("127.0.0.1", 0))
port = s.getsockname()[1]
sys.stdout.write(str(port) + "\n")
sys.stdout.flush()
s.listen(8)
time.sleep(3600)
`

func requirePortCleanupTools(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("fuser"); err != nil {
		if _, err2 := exec.LookPath("lsof"); err2 != nil {
			t.Skip("need fuser or lsof to test port cleanup")
		}
	}
}

func startSubprocessPortHolder(t *testing.T) (port int, stop func()) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 required for port holder subprocess")
	}
	cmd := exec.Command("python3", "-c", portHolderPy)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line := make([]byte, 32)
	n, err := out.Read(line)
	if err != nil || n == 0 {
		_ = cmd.Process.Kill()
		t.Fatalf("read port from holder: %v", err)
	}
	port, err = strconv.Atoi(string(line[:n-1]))
	if err != nil || port < 1 {
		_ = cmd.Process.Kill()
		t.Fatalf("parse port %q: %v", line[:n], err)
	}
	if protectedDevPorts[port] {
		_ = cmd.Process.Kill()
		t.Skip("ephemeral port collided with protected port")
	}
	return port, func() { _ = cmd.Process.Kill() }
}

func dialPort(port int) error {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err != nil {
		return err
	}
	return conn.Close()
}

func waitPortFree(t *testing.T, port int) {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			ln.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("port %d still in use after cleanup", port)
}

func TestRoleAndTrackNeedDevServerCleanup(t *testing.T) {
	t.Parallel()
	if !roleNeedsDevServerCleanup("qa") || !roleNeedsDevServerCleanup("polecat") {
		t.Fatal("qa and polecat roles need cleanup")
	}
	if roleNeedsDevServerCleanup("mayor") {
		t.Fatal("mayor should not need cleanup")
	}
	if !trackNeedsDevServerCleanup("implementation") || !trackNeedsDevServerCleanup("qa") {
		t.Fatal("implementation and qa tracks need cleanup")
	}
}
