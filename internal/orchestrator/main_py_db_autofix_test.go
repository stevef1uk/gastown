package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTryAutoFixMainPyStoreDB_missingGlobal(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "mayor", "rig")
	layout := "myapp"
	appDir := filepath.Join(rigDir, layout)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Python entrypoint with db = None + startup function that assigns locally without global
	src := `import sqlite3
from fastapi import FastAPI

app = FastAPI()
db = None

@app.on_event("startup")
async def startup():
    db = sqlite3.connect("app.db")
    db.execute("CREATE TABLE IF NOT EXISTS items (id INTEGER PRIMARY KEY)")

@app.get("/ping")
def ping():
    cur = db.cursor()
    cur.execute("SELECT 1")
    return {"ok": True}
`
	mainPy := filepath.Join(appDir, "main.py")
	if err := os.WriteFile(mainPy, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	v := WorkflowValidation{
		LayoutRoot: layout,
		RequiredFiles: []string{
			"myapp/main.py",
			"myapp/requirements.txt",
		},
	}

	rel, err := TryAutoFixMainPyStoreDB(rigDir, v)
	if err != nil {
		t.Fatal(err)
	}
	if rel == "" {
		t.Fatal("expected auto-fix to apply")
	}

	got, err := os.ReadFile(mainPy)
	if err != nil {
		t.Fatal(err)
	}
	body := string(got)

	if !strings.Contains(body, "global db") {
		t.Fatalf("expected 'global db' in patched file:\n%s", body)
	}
	if !strings.Contains(body, `db = sqlite3.connect("app.db")`) {
		t.Fatalf("expected original assignment to remain:\n%s", body)
	}
}

func TestTryAutoFixMainPyStoreDB_alreadyHasGlobal(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "mayor", "rig")
	layout := "app"
	appDir := filepath.Join(rigDir, layout)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}

	src := `import sqlite3
from fastapi import FastAPI

app = FastAPI()
db = None

@app.on_event("startup")
async def startup():
    global db
    db = sqlite3.connect("app.db")
`
	mainPy := filepath.Join(appDir, "main.py")
	if err := os.WriteFile(mainPy, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	v := WorkflowValidation{
		LayoutRoot: layout,
		RequiredFiles: []string{
			"app/main.py",
		},
	}

	rel, err := TryAutoFixMainPyStoreDB(rigDir, v)
	if err != nil {
		t.Fatal(err)
	}
	if rel != "" {
		t.Fatal("expected no auto-fix when global already present")
	}
}

func TestTryAutoFixMainPyStoreDB_noneVarNotNone(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "mayor", "rig")
	layout := "app"
	appDir := filepath.Join(rigDir, layout)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}

	// db is assigned directly, not None
	src := `import sqlite3
from fastapi import FastAPI

app = FastAPI()
db = sqlite3.connect("app.db")
`
	mainPy := filepath.Join(appDir, "main.py")
	if err := os.WriteFile(mainPy, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	v := WorkflowValidation{
		LayoutRoot: layout,
		RequiredFiles: []string{
			"app/main.py",
		},
	}

	rel, err := TryAutoFixMainPyStoreDB(rigDir, v)
	if err != nil {
		t.Fatal(err)
	}
	if rel != "" {
		t.Fatal("expected no auto-fix when var is not None")
	}
}

func TestTryAutoFixMainPyStoreDB_createEnginePattern(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "mayor", "rig")
	layout := "project"
	appDir := filepath.Join(rigDir, layout)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}

	// SQLAlchemy pattern
	src := `from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker

engine = None
SessionLocal = None

def init_db():
    engine = create_engine("sqlite:///./app.db")
    SessionLocal = sessionmaker(bind=engine)
`
	mainPy := filepath.Join(appDir, "app.py")
	if err := os.WriteFile(mainPy, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	v := WorkflowValidation{
		LayoutRoot: layout,
		RequiredFiles: []string{
			"project/app.py",
		},
	}

	rel, err := TryAutoFixMainPyStoreDB(rigDir, v)
	if err != nil {
		t.Fatal(err)
	}
	if rel == "" {
		t.Fatal("expected auto-fix for create_engine pattern")
	}

	got, err := os.ReadFile(mainPy)
	if err != nil {
		t.Fatal(err)
	}
	body := string(got)

	if !strings.Contains(body, "global engine") {
		t.Fatalf("expected 'global engine' in patched file:\n%s", body)
	}
	if !strings.Contains(body, "global SessionLocal") {
		t.Fatalf("expected 'global SessionLocal' in patched file:\n%s", body)
	}
}

func TestTryAutoFixMainPyStoreDBFromOutput_noneType(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "mayor", "rig")
	layout := "app"
	appDir := filepath.Join(rigDir, layout)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}

	src := `import sqlite3
db = None

def init():
    db = sqlite3.connect("app.db")
`
	mainPy := filepath.Join(appDir, "main.py")
	if err := os.WriteFile(mainPy, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	v := WorkflowValidation{
		LayoutRoot: layout,
		RequiredFiles: []string{
			"app/main.py",
		},
	}

	smokeOutput := `Traceback (most recent call last):
  File "/app/main.py", line 8, in serve
    db.execute("SELECT 1")
AttributeError: 'NoneType' object has no attribute 'execute'
`

	rel, err := TryAutoFixMainPyStoreDBFromOutput(rigDir, v, smokeOutput)
	if err != nil {
		t.Fatal(err)
	}
	if rel == "" {
		t.Fatal("expected auto-fix from smoke output")
	}

	got, err := os.ReadFile(mainPy)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "global db") {
		t.Fatalf("expected 'global db' after smoke-output fix:\n%s", string(got))
	}
}

func TestTryAutoFixMainPyStoreDBFromOutput_noMatch(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "app",
		RequiredFiles: []string{
			"app/main.py",
		},
	}
	// No NoneType error — should not trigger
	rel, err := TryAutoFixMainPyStoreDBFromOutput("/tmp", v, "connection refused")
	if err != nil {
		t.Fatal(err)
	}
	if rel != "" {
		t.Fatal("expected no fix for non-NoneType errors")
	}

	// NoneType but not DB-related
	rel, err = TryAutoFixMainPyStoreDBFromOutput("/tmp", v, "AttributeError: 'NoneType' object has no attribute 'foo'")
	if err != nil {
		t.Fatal(err)
	}
	if rel != "" {
		t.Fatal("expected no fix for non-DB NoneType error")
	}
}

func TestPyEntrypointRelPath(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "myapp",
		RequiredFiles: []string{
			"myapp/main.py",
			"myapp/requirements.txt",
		},
	}
	got := pyEntrypointRelPath(v)
	if got != "myapp/main.py" {
		t.Fatalf("want myapp/main.py, got %q", got)
	}
}

func TestPyEntrypointRelPath_appPy(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "backend",
		RequiredFiles: []string{
			"backend/app.py",
			"backend/api/routes.py",
		},
	}
	got := pyEntrypointRelPath(v)
	if got != "backend/app.py" {
		t.Fatalf("want backend/app.py, got %q", got)
	}
}
