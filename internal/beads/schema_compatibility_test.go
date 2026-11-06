package beads

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	beadsdk "github.com/steveyegge/beads"
	"github.com/steveyegge/gastown/internal/deps"
)

// Opt in to a native Dolt server in a temporary directory. Never use the town's
// server: this regression exercises SDK/CLI schema creation on an empty database.
func TestFreshBeadsSDKCLICompatibility(t *testing.T) {
	if os.Getenv("GT_BEADS_SCHEMA_TEST") != "1" {
		t.Skip("set GT_BEADS_SCHEMA_TEST=1 for isolated native Dolt schema test")
	}
	dolt, err := exec.LookPath("dolt")
	if err != nil {
		t.Fatal(err)
	}
	bd := os.Getenv("GT_BEADS_TEST_BD")
	if bd == "" {
		bd, err = exec.LookPath("bd")
		if err != nil {
			t.Fatal(err)
		}
	}
	out, err := exec.Command(bd, "version").Output()
	if err != nil || !strings.Contains(string(out), "bd version "+deps.BeadsVersion) {
		t.Fatalf("need matching bd %s: %s (%v)", deps.BeadsVersion, out, err)
	}
	root := t.TempDir()
	data := filepath.Join(root, "data")
	if err := os.MkdirAll(data, 0755); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	server := exec.CommandContext(ctx, dolt, "sql-server", "--data-dir", data, "-H", "127.0.0.1", "-P", strconv.Itoa(port))
	server.Dir = root
	log, err := os.Create(filepath.Join(root, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	server.Stdout = log
	server.Stderr = log
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { server.Process.Kill(); server.Wait() }()
	db, err := sql.Open("mysql", fmt.Sprintf("root@tcp(127.0.0.1:%d)/?timeout=1s", port))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ready := false
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		if db.PingContext(ctx) == nil {
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		content, _ := os.ReadFile(log.Name())
		t.Fatalf("server not ready: %s", content)
	}
	if _, err := db.ExecContext(ctx, "CREATE DATABASE fresh"); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repo")
	beadsDir := filepath.Join(repo, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "BEADS_") || strings.HasPrefix(key, "GT_") {
			t.Setenv(key, "")
		}
	}
	t.Setenv("BEADS_DIR", beadsDir)
	t.Setenv("BEADS_DOLT_PORT", strconv.Itoa(port))
	t.Setenv("BEADS_DOLT_SERVER_PORT", strconv.Itoa(port))
	t.Setenv("BEADS_DOLT_AUTO_START", "0")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "127.0.0.1")
	metadata, _ := json.Marshal(map[string]any{"backend": "dolt", "database": "dolt", "dolt_database": "fresh", "dolt_mode": "server", "dolt_server_host": "127.0.0.1"})
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), metadata, 0644); err != nil {
		t.Fatal(err)
	}
	run := func(binary string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Schema Test", "GIT_AUTHOR_EMAIL=schema@example.test", "GIT_COMMITTER_NAME=Schema Test", "GIT_COMMITTER_EMAIL=schema@example.test")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", binary, args, err, out)
		}
		return string(out)
	}
	run("git", "init", "-b", "main")
	// Match the mixed access pattern used by Gastown: SDK first, CLI init, SDK
	// writes, CLI list. The old SDK lacked leases and left newer migrations dirty.
	store, err := beadsdk.OpenFromConfig(ctx, beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	run(bd, "init", "--prefix", "fr", "--server", "--server-port", strconv.Itoa(port), "--force")
	store, err = beadsdk.OpenFromConfig(ctx, beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	issue := &beadsdk.Issue{Title: "fresh schema regression", IssueType: beadsdk.TypeTask, Status: beadsdk.StatusOpen, Priority: 2}
	err = store.CreateIssue(ctx, issue, "schema-test")
	store.Close()
	if err != nil {
		t.Fatal(err)
	}
	listed := run(bd, "list", "--status=open", "--flat", "--limit=0", "--json")
	if !strings.Contains(listed, "fresh schema regression") {
		t.Fatalf("CLI cannot read SDK issue: %s", listed)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM fresh.leases").Scan(&count); err != nil {
		t.Fatalf("lease schema missing: %v", err)
	}
	// A down/up cycle must reconnect to the same database, never bootstrap a
	// shadow server while the managed server is absent.
	db.Close()
	if err := server.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := server.Wait(); err != nil {
		t.Fatal(err)
	}
	downCtx, downCancel := context.WithTimeout(ctx, 8*time.Second)
	downCmd := exec.CommandContext(downCtx, bd, "list", "--json", "--flat")
	downCmd.Dir = repo
	downOutput, downErr := downCmd.CombinedOutput()
	downCancel()
	if downErr == nil {
		t.Fatalf("bd unexpectedly succeeded with managed server down: %s", downOutput)
	}
	if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second); err == nil {
		conn.Close()
		t.Fatal("bd auto-started a server while managed Dolt was down")
	}
	server = exec.CommandContext(ctx, dolt, "sql-server", "--data-dir", data, "-H", "127.0.0.1", "-P", strconv.Itoa(port))
	server.Dir = root
	server.Stdout = log
	server.Stderr = log
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("mysql", fmt.Sprintf("root@tcp(127.0.0.1:%d)/fresh?timeout=1s", port))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ready = false
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		if db.PingContext(ctx) == nil {
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatal("restarted server not ready")
	}
	var prefix string
	if err := db.QueryRowContext(ctx, "SELECT value FROM config WHERE `key`='issue_prefix'").Scan(&prefix); err != nil || prefix != "fr" {
		t.Fatalf("prefix after restart: %q (%v)", prefix, err)
	}
	if listed = run(bd, "list", "--status=open", "--flat", "--limit=0", "--json"); !strings.Contains(listed, "fresh schema regression") {
		t.Fatalf("issue lost after restart: %s", listed)
	}

}
