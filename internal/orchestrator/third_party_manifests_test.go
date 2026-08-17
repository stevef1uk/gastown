package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

// writeRigFile writes a file under rigDir at rel, creating parents.
func writeRigFile(t *testing.T, rigDir, rel, body string) {
	t.Helper()
	full := filepath.Join(rigDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDeclaredThirdPartyModules_nodePackage(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	writeRigFile(t, rigDir, "frontend/package.json", `{
  "dependencies": {"react": "^18.0.0", "next": "^14.0.0"},
  "devDependencies": {"tailwindcss": "^3.4.0", "@types/react": "^18.0.0"},
  "optionalDependencies": {"fsevents": "^2.0.0"}
}`)
	d := loadDeclaredThirdPartyModules(rigDir)
	if !d.matches("tailwindcss", ".ts") {
		t.Fatal("tailwindcss should be declared third-party")
	}
	if !d.matches("react", ".tsx") {
		t.Fatal("react should be declared third-party")
	}
	if !d.matches("@types/react", ".ts") {
		t.Fatal("scoped @types/react should be declared third-party")
	}
	if !d.matches("next", ".ts") {
		t.Fatal("next should be declared third-party")
	}
	if d.matches("mylocalmodule", ".ts") {
		t.Fatal("undeclared module must not be third-party")
	}
}

func TestLoadDeclaredThirdPartyModules_nodeSubpath(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	writeRigFile(t, rigDir, "frontend/package.json", `{"dependencies": {"react-dom": "^18.0.0", "@scope/pkg": "^1.0.0"}}`)
	d := loadDeclaredThirdPartyModules(rigDir)
	if !d.matches("react-dom/client", ".tsx") {
		t.Fatal("react-dom/client subpath should resolve to declared react-dom")
	}
	if !d.matches("@scope/pkg/sub", ".ts") {
		t.Fatal("@scope/pkg/sub should resolve to declared scoped package")
	}
}

func TestLoadDeclaredThirdPartyModules_goMod(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	writeRigFile(t, rigDir, "finally/go.mod", `module finally

go 1.25

require (
	github.com/gorilla/mux v1.8.1
	golang.org/x/net v0.30.0
)

require github.com/google/uuid v1.6.0
`)
	d := loadDeclaredThirdPartyModules(rigDir)
	if !d.matches("github.com/gorilla/mux", ".go") {
		t.Fatal("github.com/gorilla/mux should be declared third-party")
	}
	if !d.matches("github.com/gorilla/mux/routes", ".go") {
		t.Fatal("subpackage import should match declared module")
	}
	if !d.matches("golang.org/x/net/http2", ".go") {
		t.Fatal("golang.org/x/net subpackage should match")
	}
	if !d.matches("github.com/google/uuid", ".go") {
		t.Fatal("single-line require should be collected")
	}
	if d.matches("github.com/example/notdeclared", ".go") {
		t.Fatal("undeclared Go module must not be third-party")
	}
}

func TestLoadDeclaredThirdPartyModules_pythonPyProject(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	writeRigFile(t, rigDir, "finally/backend/pyproject.toml", `
[project]
name = "finally"
dependencies = [
  "fastapi>=0.100",
  "uvicorn[standard]==0.23.2",
]
[project.optional-dependencies]
dev = ["pytest>=7.0", "httpx"]
`)
	d := loadDeclaredThirdPartyModules(rigDir)
	for _, mod := range []string{"fastapi", "uvicorn", "pytest", "httpx"} {
		if !d.matches(mod, ".py") {
			t.Fatalf("%s should be declared third-party", mod)
		}
	}
	if !d.matches("fastapi.routing", ".py") {
		t.Fatal("fastapi submodule import should match declared root")
	}
	if d.matches("mymodule", ".py") {
		t.Fatal("undeclared python module must not be third-party")
	}
}

func TestLoadDeclaredThirdPartyModules_pythonRequirements(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	writeRigFile(t, rigDir, "finally/backend/requirements.txt", `# comment line
fastapi==0.100.0
requests>=2.0
`)
	d := loadDeclaredThirdPartyModules(rigDir)
	if !d.matches("fastapi", ".py") || !d.matches("requests", ".py") {
		t.Fatal("requirements.txt deps should be declared third-party")
	}
}

func TestLoadDeclaredThirdPartyModules_skipsNodeModulesAndVenv(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	writeRigFile(t, rigDir, "frontend/node_modules/somepkg/package.json", `{"dependencies": {"tailwindcss": "^3.0.0"}}`)
	writeRigFile(t, rigDir, "finally/.venv/package.json", `{"dependencies": {"pydantic": "^2.0.0"}}`)
	writeRigFile(t, rigDir, "finally/package.json", `{"dependencies": {"next": "^14.0.0"}}`)
	d := loadDeclaredThirdPartyModules(rigDir)
	if d.matches("tailwindcss", ".ts") {
		t.Fatal("deps inside node_modules must be ignored")
	}
	if d.matches("pydantic", ".ts") {
		t.Fatal("deps inside venv must be ignored")
	}
	if !d.matches("next", ".ts") {
		t.Fatal("real package.json dep should be collected")
	}
}

func TestDeclaredThirdPartyProbe_withoutInstalledModules(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	// tailwindcss is declared but never installed: probe must still say third-party
	// so missing-import detection never flags it as a project module needing a bead.
	writeRigFile(t, rigDir, "frontend/package.json", `{"devDependencies": {"tailwindcss": "^3.4.0"}}`)
	d := loadDeclaredThirdPartyModules(rigDir)
	if !declaredThirdPartyProbe(d, "tailwindcss", rigDir, ".ts") {
		t.Fatal("declared dep must be importable third-party even when node_modules is absent")
	}
}

func TestMatchesScannerExt_tsx(t *testing.T) {
	t.Parallel()
	sc := struct {
		ext  string
		exts []string
	}{ext: ".ts", exts: []string{".ts", ".tsx"}}
	if !matchesScannerExt("frontend/components/Chart.tsx", sc.ext, sc.exts) {
		t.Fatal(".tsx should match node scanner")
	}
	if !matchesScannerExt("frontend/app.ts", sc.ext, sc.exts) {
		t.Fatal(".ts should match node scanner")
	}
	if matchesScannerExt("frontend/app.js", sc.ext, sc.exts) {
		t.Fatal(".js should not match")
	}
}

func TestNodeModulesDirFor_nestedLayoutRoot(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	writeRigFile(t, rigDir, "frontend/node_modules/tailwindcss/package.json", `{"name": "tailwindcss"}`)
	if got := nodeModulesDirFor("tailwindcss", rigDir); got == "" {
		t.Fatal("should find nested node_modules under layout root")
	}
	if got := nodeModulesDirFor("next", rigDir); got != "" {
		t.Fatalf("undeclared+uninstalled module should not be found, got %q", got)
	}
}

func TestNodeModulesDirFor_skipsNestedContent(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	writeRigFile(t, rigDir, "frontend/node_modules/alpha/package.json", `{}`)
	writeRigFile(t, rigDir, "frontend/node_modules/beta/node_modules/alpha/package.json", `{}`)
	if got := nodeModulesDirFor("alpha", rigDir); got == "" {
		t.Fatal("alpha should be found at the top-level node_modules")
	}
}

func TestLoadDeclaredThirdPartyModules_nodeMatchNormalization(t *testing.T) {
	t.Parallel()
	d := &thirdPartyDeclared{nodePackage: map[string]bool{"react-dom": true}}
	if !d.matches("react-dom", ".ts") || !d.matches("react-dom/server", ".tsx") {
		t.Fatal("exact and subpath matches expected")
	}
	d2 := &thirdPartyDeclared{nodePackage: map[string]bool{"@scope/pkg": true}}
	if !d2.matches("@scope/pkg/deep/import", ".ts") {
		t.Fatal("scoped subpath should match")
	}
}

func TestPythonMatchRootAndDashes(t *testing.T) {
	t.Parallel()
	d := &thirdPartyDeclared{pythonRoot: map[string]bool{"fastapi": true, "python_dateutil": true}}
	if !d.matches("fastapi.middleware.cors", ".py") {
		t.Fatal("deep submodule should match declared root")
	}
	if !d.matches("python_dateutil.tz", ".py") {
		t.Fatal("declared python-dateutil (dashes normalized to _) should match its import root")
	}
}

func TestGoModuleMatchPrefixBoundary(t *testing.T) {
	t.Parallel()
	d := &thirdPartyDeclared{goModules: map[string]bool{"github.com/foo/bar": true}}
	if !d.matches("github.com/foo/bar", ".go") {
		t.Fatal("exact module should match")
	}
	if !d.matches("github.com/foo/bar/sub", ".go") {
		t.Fatal("subpackage should match")
	}
	if d.matches("github.com/foo/barbarian", ".go") {
		t.Fatal("must not match prefix without '/' boundary")
	}
}

func TestScanMissingImports_nodeDeclaredTailwindNotFlagged(t *testing.T) {
	t.Parallel()
	// Reproduces the fin rig loop: tailwindcss is imported by tailwind.config.ts,
	// is declared in frontend/package.json, and is never installed in node_modules.
	// It must NOT be reported as a missing implementation module.
	rigDir := t.TempDir()
	writeRigFile(t, rigDir, "finally/frontend/package.json", `{"devDependencies": {"tailwindcss": "^3.4.0"}}`)
	writeRigFile(t, rigDir, "finally/frontend/tailwind.config.ts", "import type { Config } from \"tailwindcss\";\n")
	v := WorkflowValidation{LayoutRoot: "finally", RequiredFiles: []string{"finally/frontend/tailwind.config.ts"}}
	missing := scanMissingImports(rigDir, v)
	if _, ok := missing["tailwindcss"]; ok {
		t.Fatalf("declared tailwindcss must not be a missing import: %v", missing)
	}
}

func TestScanMissingImports_nodeProjectModule(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	// Chart.tsx imports ./utils/format (relative, ignored) and a project module
	// frontend/src/api/client.ts that does not exist on disk. package.json in
	// required_files makes this a Node workflow.
	writeRigFile(t, rigDir, "finally/frontend/src/components/Chart.tsx", "import { formatDate } from \"./utils/format\";\nimport { getData } from \"api/client\";\n")
	v := WorkflowValidation{
		LayoutRoot:    "finally",
		RequiredFiles: []string{"finally/frontend/package.json", "finally/frontend/src/components/Chart.tsx"},
	}
	missing := scanMissingImports(rigDir, v)
	if _, ok := missing["api/client"]; !ok {
		t.Fatalf("api/client is a project module with no bead and should be flagged: %v", missing)
	}
}

func TestScanMissingImports_pythonDeclaredNotFlagged(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	writeRigFile(t, rigDir, "finally/backend/pyproject.toml", "[project]\nname = \"finally\"\ndependencies = [\"fastapi>=0.100\"]\n")
	writeRigFile(t, rigDir, "finally/backend/app/main.py", "from fastapi import FastAPI\n")
	v := WorkflowValidation{LayoutRoot: "finally", PythonVenvDir: ".venv", RequiredFiles: []string{"finally/backend/app/main.py"}}
	missing := scanMissingImports(rigDir, v)
	if _, ok := missing["fastapi"]; ok {
		t.Fatalf("declared fastapi must not be a missing import: %v", missing)
	}
}

func TestScanMissingImports_pythonProjectModule(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	// main.py at the backend root (module "main") imports app.canonical_routes, but
	// no app package exists on disk, so the import is a genuinely missing project module.
	writeRigFile(t, rigDir, "finally/backend/pyproject.toml", "[project]\nname = \"finally\"\ndependencies = []\n")
	writeRigFile(t, rigDir, "finally/backend/main.py", "import app.canonical_routes\n")
	v := WorkflowValidation{LayoutRoot: "finally", PythonVenvDir: ".venv", RequiredFiles: []string{"finally/backend/main.py"}}
	missing := scanMissingImports(rigDir, v)
	if _, ok := missing["app.canonical_routes"]; !ok {
		t.Fatalf("app.canonical_routes is a project module with no bead and should be flagged: %v", missing)
	}
}

func TestScanMissingImports_goDeclaredNotFlagged(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	writeRigFile(t, rigDir, "finally/go.mod", "module finally\n\ngo 1.25\n\nrequire github.com/gorilla/mux v1.8.1\n")
	writeRigFile(t, rigDir, "finally/cmd/server/main.go", "package main\n\nimport (\n\t\"github.com/gorilla/mux\"\n)\n")
	v := WorkflowValidation{LayoutRoot: "finally", RequiredFiles: []string{"finally/cmd/server/main.go"}}
	missing := scanMissingImports(rigDir, v)
	if _, ok := missing["github.com/gorilla/mux"]; ok {
		t.Fatalf("declared gorilla/mux must not be a missing import: %v", missing)
	}
}

func TestScanMissingImports_goProjectModule(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	writeRigFile(t, rigDir, "finally/go.mod", "module finally\n\ngo 1.25\n")
	writeRigFile(t, rigDir, "finally/cmd/server/main.go", "package main\n\nimport (\n\t\"finally/internal/api/handlers\"\n)\n")
	v := WorkflowValidation{
		LayoutRoot:       "finally",
		QAVerifyCommand:  "cd finally && go test ./...",
		RequiredFiles:    []string{"finally/cmd/server/main.go"},
		MinImplementationFileBytes: 1,
	}
	missing := scanMissingImports(rigDir, v)
	if _, ok := missing["finally/internal/api/handlers"]; !ok {
		t.Fatalf("project module with no bead should be flagged: %v", missing)
	}
}

func TestScanMissingImports_ignoresInstalledNodeModule(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	writeRigFile(t, rigDir, "frontend/node_modules/recharts/package.json", `{"name": "recharts"}`)
	writeRigFile(t, rigDir, "frontend/src/Chart.tsx", "import { LineChart } from \"recharts\";\n")
	v := WorkflowValidation{LayoutRoot: "frontend", RequiredFiles: []string{"frontend/src/Chart.tsx"}}
	missing := scanMissingImports(rigDir, v)
	if _, ok := missing["recharts"]; ok {
		t.Fatalf("installed recharts must not be a missing import: %v", missing)
	}
}

func TestReopenMissingImportBeadsNonFatalSignatureCompiles(t *testing.T) {
	// Guards the return contract used by ReconcileImplementBeads: warnings instead of
	// a hard error for project modules with no implementation bead.
	reopened, warnings, err := reopenMissingImportBeads("", "", WorkflowValidation{})
	if err != nil || reopened != nil || warnings != nil {
		t.Fatalf("empty town/rig must no-op: reopened=%v warnings=%v err=%v", reopened, warnings, err)
	}
}