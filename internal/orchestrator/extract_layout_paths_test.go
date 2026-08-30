package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractSpecLayoutPaths_SPECOnly(t *testing.T) {
	dir := t.TempDir()
	mayorRigDir := filepath.Join(dir, "rig", "mayor", "rig")
	if err := os.MkdirAll(mayorRigDir, 0755); err != nil {
		t.Fatal(err)
	}

	specContent := "# My Project\n\n## Layout\n\nmyapp/\n├── go.mod\n├── main.go\n├── internal/\n│   └── store/\n│       └── store.go\n"
	if err := os.WriteFile(filepath.Join(mayorRigDir, "SPEC.md"), []byte(specContent), 0644); err != nil {
		t.Fatal(err)
	}

	paths, ok, _ := extractSpecLayoutPaths(mayorRigDir)
	if !ok {
		t.Fatal("expected ok=true")
	}
	expected := []string{"myapp/go.mod", "myapp/main.go", "myapp/internal/store/store.go"}
	if len(paths) != len(expected) {
		t.Fatalf("expected %d paths, got %d: %v", len(expected), len(paths), paths)
	}
	for i, p := range expected {
		if paths[i] != p {
			t.Fatalf("path %d: expected %q, got %q", i, p, paths[i])
		}
	}
}

func TestExtractSpecLayoutPaths_BothSPECAndArchitecture(t *testing.T) {
	dir := t.TempDir()
	mayorRigDir := filepath.Join(dir, "rig", "mayor", "rig")
	if err := os.MkdirAll(mayorRigDir, 0755); err != nil {
		t.Fatal(err)
	}

	specContent := "# My Project\n\n## Layout\n\nmyapp/\n├── go.mod\n├── main.go\n"
	if err := os.WriteFile(filepath.Join(mayorRigDir, "SPEC.md"), []byte(specContent), 0644); err != nil {
		t.Fatal(err)
	}

	// architecture.md with expanded file list - using backtick format for detection
	archContent := "# Architecture\n\n## Planned file layout\n\n- `myapp/go.mod`\n- `myapp/main.go`\n- `myapp/internal/store/store.go`\n- `myapp/internal/store/schema.sql`\n- `myapp/internal/api/handler.go`\n- `myapp/cmd/server/main.go`\n- `myapp/backend/tests/test_api.py`\n- `myapp/backend/tests/test_market.py`\n"
	if err := os.WriteFile(filepath.Join(mayorRigDir, "architecture.md"), []byte(archContent), 0644); err != nil {
		t.Fatal(err)
	}

	paths, ok, _ := extractSpecLayoutPaths(mayorRigDir)
	if !ok {
		t.Fatal("expected ok=true")
	}

	expectedFiles := map[string]bool{
		"myapp/go.mod":                  true,
		"myapp/main.go":                 true,
		"myapp/internal/store/store.go": true,
		"myapp/internal/store/schema.sql": true,
		"myapp/internal/api/handler.go": true,
		"myapp/cmd/server/main.go":      true,
		"myapp/backend/tests/test_api.py": true,
		"myapp/backend/tests/test_market.py": true,
	}

	if len(paths) != len(expectedFiles) {
		t.Fatalf("expected %d unique paths, got %d: %v", len(expectedFiles), len(paths), paths)
	}
	for _, p := range paths {
		if !expectedFiles[p] {
			t.Fatalf("unexpected path: %q", p)
		}
	}
}

func TestExtractSpecLayoutPaths_ArchitectureWithTestFiles(t *testing.T) {
	dir := t.TempDir()
	mayorRigDir := filepath.Join(dir, "rig", "mayor", "rig")
	if err := os.MkdirAll(mayorRigDir, 0755); err != nil {
		t.Fatal(err)
	}

	specContent := "# FinAlly — AI Trading Workstation\n\n## Layout\n\nfinally/\n├── scripts/\n│   ├── start_mac.sh\n│   └── stop_mac.sh\n├── backend/\n│   └── app/\n│       └── main.py\n"
	if err := os.WriteFile(filepath.Join(mayorRigDir, "SPEC.md"), []byte(specContent), 0644); err != nil {
		t.Fatal(err)
	}

	// architecture.md expands with all test files - using backtick format for detection
	archContent := "# Architecture\n\n## Planned file layout\n\n" +
		"- `finally/scripts/start_mac.sh`\n" +
		"- `finally/scripts/stop_mac.sh`\n" +
		"- `finally/backend/app/main.py`\n" +
		"- `finally/backend/app/core/config.py`\n" +
		"- `finally/backend/app/db/connection.py`\n" +
		"- `finally/backend/app/db/schema.py`\n" +
		"- `finally/backend/app/db/seed.py`\n" +
		"- `finally/backend/app/portfolio/repository.py`\n" +
		"- `finally/backend/app/portfolio/service.py`\n" +
		"- `finally/backend/app/market/base.py`\n" +
		"- `finally/backend/app/market/simulator.py`\n" +
		"- `finally/backend/app/market/massive.py`\n" +
		"- `finally/backend/app/market/cache.py`\n" +
		"- `finally/backend/app/market/service.py`\n" +
		"- `finally/backend/app/market/sse.py`\n" +
		"- `finally/backend/app/llm/client.py`\n" +
		"- `finally/backend/app/llm/mock.py`\n" +
		"- `finally/backend/app/llm/prompt.py`\n" +
		"- `finally/backend/app/llm/structured.py`\n" +
		"- `finally/backend/app/api/schemas.py`\n" +
		"- `finally/backend/app/api/routes_health.py`\n" +
		"- `finally/backend/app/api/routes_market.py`\n" +
		"- `finally/backend/app/api/routes_portfolio.py`\n" +
		"- `finally/backend/app/api/routes_watchlist.py`\n" +
		"- `finally/backend/app/api/routes_chat.py`\n" +
		"- `finally/backend/app/api/static.py`\n" +
		"- `finally/backend/app/api/__init__.py`\n" +
		"- `finally/backend/app/main.py`\n" +
		"- `finally/backend/app/core/lifecycle.py`\n" +
		"- `finally/backend/tests/test_api.py`\n" +
		"- `finally/backend/tests/test_market.py`\n" +
		"- `finally/backend/tests/test_portfolio.py`\n" +
		"- `finally/backend/tests/test_llm.py`\n"
	if err := os.WriteFile(filepath.Join(mayorRigDir, "architecture.md"), []byte(archContent), 0644); err != nil {
		t.Fatal(err)
	}

	paths, ok, _ := extractSpecLayoutPaths(mayorRigDir)
	if !ok {
		t.Fatal("expected ok=true")
	}

	// Verify key test files are included
	testFiles := []string{
		"finally/backend/tests/test_api.py",
		"finally/backend/tests/test_market.py",
		"finally/backend/tests/test_portfolio.py",
		"finally/backend/tests/test_llm.py",
	}

	for _, tf := range testFiles {
		found := false
		for _, p := range paths {
			if p == tf {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing expected test file: %q", tf)
		}
	}

	// Should have many files from architecture
	if len(paths) < 30 {
		t.Fatalf("expected at least 30 files, got %d: %v", len(paths), paths)
	}
}