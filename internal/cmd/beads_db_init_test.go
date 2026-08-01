//go:build integration

// Package cmd contains integration tests for beads db initialization after clone.
//
// Run with: go test -tags=integration ./internal/cmd -run TestBeadsDbInitAfterClone -v
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
	"github.com/steveyegge/gastown/internal/testutil"
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

// setupTestTown creates a minimal town structure for testing gt rig add --adopt.
// Uses the shared Dolt server (managed by requireDoltServer) with unique prefixes.
func setupTestTown(t *testing.T) (townRoot, rigPath, gtBinary string) {
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
	rigPath = filepath.Join(townRoot, "testrig", "mayor", "rig")

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
			"testrig": {
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
		{Prefix: rigPrefix + "-", Path: "testrig/mayor/rig"},
		{Prefix: "hq-cv-", Path: "."},
	}
	if err := beads.WriteRoutes(townBeadsDir, routes); err != nil {
		t.Fatalf("write routes: %v", err)
	}
	initBeadsDBForServer(t, townRoot, hqPrefix)

	// --- testrig directory ---
	if err := os.MkdirAll(rigPath, 0755); err != nil {
		t.Fatalf("mkdir rigPath: %v", err)
	}
	initBeadsDBForServer(t, rigPath, rigPrefix)

	// Redirect: testrig/.beads/ -> mayor/rig/.beads
	rigBeadsRedirect := filepath.Join(townRoot, "testrig", ".beads")
	if err := os.MkdirAll(rigBeadsRedirect, 0755); err != nil {
		t.Fatalf("mkdir rig .beads redirect: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rigBeadsRedirect, "redirect"), []byte("mayor/rig/.beads"), 0644); err != nil {
		t.Fatalf("write rig redirect: %v", err)
	}

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
			_ = db.Exec("DROP DATABASE IF EXISTS `" + dbName + "`")
		}
	})

	return townRoot, rigPath, gtBinary
}

// TestBeadsDbInitAfterClone tests that when a tracked beads repo is added as a rig,
// the beads database is properly initialized even though database files don't exist.
func TestBeadsDbInitAfterClone(t *testing.T) {
	townRoot, rigPath, gtBinary := setupTestTown(t)

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

// initBeadsDBForServer initializes a beads DB that can operate against the
// shared Dolt test server. Uses local init (bd init --prefix --server-port)
// which reliably creates the schema and records the ephemeral port in
// metadata.json so subsequent bd commands reach the test server.
func initBeadsDBForServer(t *testing.T, dir, prefix string) {
	t.Helper()

	args := []string{"init", "--prefix", prefix}
	// Forward GT_DOLT_PORT so bd connects to the ephemeral test server
	// instead of defaulting to port 3307.
	// bd v1.0.0+ defaults to embedded mode; --server is required to use an
	// external server (v0.57.0 defaulted to server mode and ignored --server).
	if p := os.Getenv("GT_DOLT_PORT"); p != "" {
		args = append(args, "--server", "--server-port", p)
	}
	cmd := exec.Command("bd", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	t.Logf("bd init --prefix %s in %s: exit=%v\n%s", prefix, dir, err, out)
	if err != nil {
		t.Fatalf("bd init failed in %s: %v\n%s", dir, err, out)
	}

	// Create empty issues.jsonl to prevent bd auto-export from corrupting
	// routes.jsonl (same as initBeadsDBWithPrefix does).
	issuesPath := filepath.Join(dir, ".beads", "issues.jsonl")
	if err := os.WriteFile(issuesPath, []byte(""), 0644); err != nil {
		t.Fatalf("create issues.jsonl in %s: %v", dir, err)
	}

	if err := beads.EnsureCustomTypes(filepath.Join(dir, ".beads")); err != nil {
		t.Fatalf("ensure custom types in %s: %v", dir, err)
	}
}

// writeJSONFile marshals v as indented JSON and writes it to path,
// creating parent directories as needed.
func writeJSONFile(t *testing.T, path string, v interface{}) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal JSON for %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}