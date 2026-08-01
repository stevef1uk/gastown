//go:build integration

// Package cmd contains integration tests for beads db initialization after clone.
//
// Bug: GitHub Issue #72
// When a repo with tracked .beads/ is added as a rig, the database doesn't exist
// (DB files are gitignored) and bd operations fail because no one runs `bd init`.
package cmd

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
)

// testCounter generates unique prefixes for each subtest to isolate
// Dolt databases on the shared server.
var testCounter atomic.Int32

// extractJSON finds the first JSON object in output that may contain non-JSON warnings.
// bd --json -q can still emit warnings to stdout before the JSON payload.
func extractJSON(output []byte) []byte {
	idx := strings.Index(string(output), "{")
	if idx < 0 {
		return output
	}
	return output[idx:]
}

// setupTestTownForBeadsInit creates a minimal town structure for testing gt rig add --adopt.
// Uses the shared Dolt server (managed by requireDoltServer) with unique prefixes.
// Named distinctly from rig_integration_test.go's setupTestTown, which returns only townRoot.
func setupTestTownForBeadsInit(t *testing.T) (townRoot, rigPath, gtBinary string) {
	t.Helper()

	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not installed, skipping test")
	}

	requireDoltServer(t)
	cleanStaleBeadsDatabases(t)
	gtBinary = buildGT(t)

	// Generate unique prefixes per test to avoid cross-test data leakage.
	n := testCounter.Add(1)
	hqPrefix := fmt.Sprintf("h%d", n)
	rigPrefix := fmt.Sprintf("r%d", n)

	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	configureTestGitIdentity(t, tmpDir)

	townRoot = filepath.Join(tmpDir, "test-town")
	rigPath = filepath.Join(townRoot, "seedrig", "mayor", "rig")

	// --- mayor/ ---
	mayorDir := filepath.Join(townRoot, "mayor")
	if err := os.MkdirAll(mayorDir, 0755); err != nil {
		t.Fatalf("mkdir mayor: %v", err)
	}
	writeJSONFile(t, filepath.Join(mayorDir, "town.json"), &config.TownConfig{
		Type:    "town",
		Name:    "test",
		Version: config.CurrentTownVersion,
	})

	rigsConfig := &config.RigsConfig{
		Version: config.CurrentRigsVersion,
		Rigs: map[string]config.RigEntry{
			"seedrig": {
				GitURL: "file:///dev/null",
				BeadsConfig: &config.BeadsConfig{
					Prefix: rigPrefix,
				},
			},
		},
	}
	if err := config.SaveRigsConfig(filepath.Join(mayorDir, "rigs.json"), rigsConfig); err != nil {
		t.Fatalf("save rigs.json: %v", err)
	}

	// --- settings/ ---
	if err := os.MkdirAll(filepath.Join(townRoot, "settings"), 0755); err != nil {
		t.Fatalf("mkdir settings: %v", err)
	}

	// --- town-level .beads/ ---
	townBeadsDir := filepath.Join(townRoot, ".beads")
	if err := os.MkdirAll(townBeadsDir, 0755); err != nil {
		t.Fatalf("mkdir town .beads: %v", err)
	}
	routes := []beads.Route{
		{Prefix: hqPrefix + "-", Path: "."},
		{Prefix: rigPrefix + "-", Path: "seedrig/mayor/rig"},
		{Prefix: "hq-cv-", Path: "."},
	}
	if err := beads.WriteRoutes(townBeadsDir, routes); err != nil {
		t.Fatalf("write routes: %v", err)
	}
	initBeadsDBForServer(t, townRoot, hqPrefix)

	// Drop test databases on cleanup
	t.Cleanup(func() {
		port := os.Getenv("GT_DOLT_PORT")
		if port == "" {
			port = "3307"
		}
		dsn := fmt.Sprintf("root@tcp(127.0.0.1:%s)/", port)
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			return
		}
		defer db.Close()
		for _, prefix := range []string{hqPrefix, rigPrefix} {
			dbName := "beads_" + prefix
			_, _ = db.Exec("DROP DATABASE IF EXISTS `" + dbName + "`")
		}
	})

	return townRoot, rigPath, gtBinary
}

// TestBeadsDbInitAfterClone tests that when a tracked beads repo is added as a rig,
// the beads database is properly initialized even though database files don't exist.
func TestBeadsDbInitAfterClone(t *testing.T) {
	townRoot, _, gtBinary := setupTestTownForBeadsInit(t)

	t.Run("TrackedRepoWithExistingPrefix", func(t *testing.T) {
		cleanStaleBeadsDatabases(t)

		// Create a repo with existing beads prefix "existing-prefix" AND issues
		// directly at the expected rig location
		rigDir := filepath.Join(townRoot, "myrig")
		createTrackedBeadsRepoWithIssues(t, rigDir, "existing-prefix", 3)

		// Add rig with --adopt --force (local repo has no git remote)
		// Pass --prefix to match the existing prefix
		cmd := exec.Command(gtBinary, "rig", "add", "myrig", "--adopt", "--force", "--prefix", "existing-prefix")
		cmd.Dir = townRoot
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("gt rig add failed: %v\nOutput: %s", err, output)
		}

		// Verify routes.jsonl has the prefix
		routesContent, err := os.ReadFile(filepath.Join(townRoot, ".beads", "routes.jsonl"))
		if err != nil {
			t.Fatalf("read routes.jsonl: %v", err)
		}

		if !strings.Contains(string(routesContent), `"prefix":"existing-prefix-"`) {
			t.Errorf("routes.jsonl should contain existing-prefix-, got:\n%s", routesContent)
		}

		// NOW TRY TO USE bd - this is the key test for the bug
		// Without the fix, the database doesn't exist and bd operations fail
		cmd = exec.Command("bd", "--json", "-q", "create",
			"--type", "task", "--title", "test-from-rig")
		cmd.Dir = rigDir
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bd create failed (bug!): %v\nOutput: %s\n\nThis is the bug: database doesn't exist after clone because bd init was never run", err, output)
		}

		var result struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(extractJSON(output), &result); err != nil {
			t.Fatalf("parse output: %v", err)
		}

		if !strings.HasPrefix(result.ID, "existing-prefix-") {
			t.Errorf("expected existing-prefix- prefix, got %s", result.ID)
		}
	})

	t.Run("TrackedRepoWithNoIssuesRequiresPrefix", func(t *testing.T) {
		cleanStaleBeadsDatabases(t)

		// Create a tracked beads repo with NO issues at the expected rig location
		rigDir := filepath.Join(townRoot, "emptyrig")
		createTrackedBeadsRepoWithNoIssues(t, rigDir, "empty-prefix")

		// Add rig WITH --prefix and --force (local repo has no git remote)
		cmd := exec.Command(gtBinary, "rig", "add", "emptyrig", "--adopt", "--force", "--prefix", "empty-prefix")
		cmd.Dir = townRoot
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("gt rig add with --prefix failed: %v\nOutput: %s", err, output)
		}

		// Verify routes.jsonl has the prefix
		routesContent, err := os.ReadFile(filepath.Join(townRoot, ".beads", "routes.jsonl"))
		if err != nil {
			t.Fatalf("read routes.jsonl: %v", err)
		}

		if !strings.Contains(string(routesContent), `"prefix":"empty-prefix-"`) {
			t.Errorf("routes.jsonl should contain empty-prefix-, got:\n%s", routesContent)
		}

		// Verify bd operations work with the configured prefix
		cmd = exec.Command("bd", "--json", "-q", "create",
			"--type", "task", "--title", "test-from-empty-repo")
		cmd.Dir = rigDir
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bd create failed: %v\nOutput: %s", err, output)
		}

		var result struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(extractJSON(output), &result); err != nil {
			t.Fatalf("parse output: %v", err)
		}

		if !strings.HasPrefix(result.ID, "empty-prefix-") {
			t.Errorf("expected empty-prefix- prefix, got %s", result.ID)
		}
	})

	t.Run("TrackedRepoWithPrefixMismatchErrors", func(t *testing.T) {
		cleanStaleBeadsDatabases(t)

		// Create a repo with existing beads prefix "real-prefix" with issues
		rigDir := filepath.Join(townRoot, "mismatchrig")
		createTrackedBeadsRepoWithIssues(t, rigDir, "real-prefix", 2)

		// Add rig with WRONG --prefix - should fail
		cmd := exec.Command(gtBinary, "rig", "add", "mismatchrig", "--adopt", "--force", "--prefix", "wrong-prefix")
		cmd.Dir = townRoot
		output, err := cmd.CombinedOutput()

		// Should fail
		if err == nil {
			t.Fatalf("gt rig add should have failed with prefix mismatch, but succeeded.\nOutput: %s", output)
		}

		// Verify error message mentions the mismatch
		outputStr := string(output)
		if !strings.Contains(outputStr, "prefix mismatch") {
			t.Errorf("expected 'prefix mismatch' in error, got:\n%s", outputStr)
		}
		if !strings.Contains(outputStr, "real-prefix") {
			t.Errorf("expected 'real-prefix' (detected) in error, got:\n%s", outputStr)
		}
		if !strings.Contains(outputStr, "wrong-prefix") {
			t.Errorf("expected 'wrong-prefix' (provided) in error, got:\n%s", outputStr)
		}
	})

	t.Run("TrackedRepoWithNoIssuesFallsBackToDerivedPrefix", func(t *testing.T) {
		cleanStaleBeadsDatabases(t)

		// Create a tracked beads repo with NO issues at the expected rig location
		rigDir := filepath.Join(townRoot, "testrig")
		createTrackedBeadsRepoWithNoIssues(t, rigDir, "original-prefix")

		// Add rig WITHOUT --prefix - should derive from rig name "testrig"
		cmd := exec.Command(gtBinary, "rig", "add", "testrig", "--adopt", "--force")
		cmd.Dir = townRoot
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("gt rig add (no --prefix) failed: %v\nOutput: %s", err, output)
		}

		// Verify bd operations work - the key test is that the database was initialized
		cmd = exec.Command("bd", "--json", "-q", "create",
			"--type", "task", "--title", "test-derived-prefix")
		cmd.Dir = rigDir
		output, err = cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bd create failed (database not initialized?): %v\nOutput: %s", err, output)
		}

		var result struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(extractJSON(output), &result); err != nil {
			t.Fatalf("parse output: %v", err)
		}

		// The ID should have SOME prefix (derived from "testrig")
		// We don't care exactly what it is, just that bd works
		if result.ID == "" {
			t.Error("expected non-empty issue ID")
		}
		t.Logf("Created issue with derived prefix: %s", result.ID)
	})

	t.Run("MissingMetadataTriggersReInit", func(t *testing.T) {
		cleanStaleBeadsDatabases(t)

		// Create a tracked beads repo with issues
		rigDir := filepath.Join(townRoot, "reinitrig")
		createTrackedBeadsRepoWithIssues(t, rigDir, "reinit-prefix", 2)

		// Forcibly remove metadata.json and dolt/ to simulate missing DB state.
		// This forces the rig.go initialization branch (metadata.json check).
		beadsDir := filepath.Join(rigDir, ".beads")
		os.Remove(filepath.Join(beadsDir, "metadata.json"))
		os.RemoveAll(filepath.Join(beadsDir, "dolt"))

		// Verify metadata.json is actually gone
		if _, err := os.Stat(filepath.Join(beadsDir, "metadata.json")); !os.IsNotExist(err) {
			t.Fatalf("expected metadata.json to be removed, but stat returned: %v", err)
		}

		// Add rig with --adopt --force
		cmd := exec.Command(gtBinary, "rig", "add", "reinitrig", "--adopt", "--force", "--prefix", "reinit-prefix")
		cmd.Dir = townRoot
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("gt rig add failed: %v\nOutput: %s", err, output)
		}

		// Verify the re-init path was triggered: adopt output should confirm init
		if !strings.Contains(string(output), "Initialized beads database") {
			t.Fatalf("expected 'Initialized beads database' in adopt output, got:\n%s", output)
		}

		// Verify the database artifacts were recreated
		metadataPath := filepath.Join(beadsDir, "metadata.json")
		if _, err := os.Stat(metadataPath); os.IsNotExist(err) {
			t.Fatal("metadata.json was not recreated by rig add --adopt")
		}
		doltDir := filepath.Join(beadsDir, "dolt")
		if _, err := os.Stat(doltDir); os.IsNotExist(err) {
			t.Fatal("dolt/ directory was not recreated by rig add --adopt")
		}

		t.Logf("Re-init path verified: metadata.json and dolt/ recreated after adopt")
	})
}

// createTrackedBeadsRepoWithIssues initializes a git repo at dir with a tracked
// .beads/ directory carrying the given prefix in metadata.json and count issues
// in issues.jsonl. Simulates a cloned beads project whose database prefix is
// discoverable via metadata.json dolt_database (dolt/ is gitignored, metadata.json
// is tracked, so it survives clone).
func createTrackedBeadsRepoWithIssues(t *testing.T, dir, prefix string, count int) {
	t.Helper()
	initTrackedBeadsRepo(t, dir)
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	writeJSONFile(t, filepath.Join(beadsDir, "metadata.json"), map[string]interface{}{
		"backend":       "dolt",
		"dolt_mode":     "server",
		"dolt_database": "beads_" + prefix,
	})
	if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"), []byte("prefix: "+prefix+"\nissue-prefix: "+prefix+"\n"), 0644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}
	var sb strings.Builder
	for i := 1; i <= count; i++ {
		fmt.Fprintf(&sb, "{\"id\":\"%s-%d\",\"title\":\"issue %d\",\"status\":\"open\"}\n", prefix, i, i)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "issues.jsonl"), []byte(sb.String()), 0644); err != nil {
		t.Fatalf("write issues.jsonl: %v", err)
	}
	commitTrackedBeadsRepo(t, dir)
}

// createTrackedBeadsRepoWithNoIssues initializes a git repo at dir with a tracked
// .beads/ directory but no issues and no detectable dolt_database prefix, so the
// adopt flow must use --prefix or derive a prefix from the rig name.
func createTrackedBeadsRepoWithNoIssues(t *testing.T, dir, prefix string) {
	t.Helper()
	initTrackedBeadsRepo(t, dir)
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	writeJSONFile(t, filepath.Join(beadsDir, "metadata.json"), map[string]interface{}{
		"backend":   "dolt",
		"dolt_mode": "server",
	})
	if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"), []byte(""), 0644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}
	commitTrackedBeadsRepo(t, dir)
}

// initTrackedBeadsRepo runs `git init` and configures a test identity in dir.
func initTrackedBeadsRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir repo %s: %v", dir, err)
	}
	for _, args := range [][]string{
		{"git", "init", "--initial-branch=main"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test User"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// commitTrackedBeadsRepo stages and commits everything in dir.
func commitTrackedBeadsRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "Initial commit"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}
