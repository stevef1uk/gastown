package orchestrator

import (
	"strings"
	"testing"
)

func TestCheckPythonSourceValid_rejectsShellPaste(t *testing.T) {
	src := []byte("import python3 -m pytest\nfrom flask import Flask\n")
	err := CheckPythonSourceValid(src, "pkg/main.py")
	if err == nil {
		t.Fatal("expected reject")
	}
	if !strings.Contains(err.Error(), "shell") {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "pkg/main.py") {
		t.Fatalf("error should name display path: %v", err)
	}
}

func TestCheckPythonSourceValid_acceptsNormal(t *testing.T) {
	src := []byte("from flask import Flask\napp = Flask(__name__)\n")
	if err := CheckPythonSourceValid(src, "main.py"); err != nil {
		t.Fatal(err)
	}
}

func TestCheckPythonSourceValid_acceptsImportPytest(t *testing.T) {
	src := []byte("import pytest\nfrom defender.backend.main import app\n")
	if err := CheckPythonSourceValid(src, "defender/backend/tests/test_api.py"); err != nil {
		t.Fatalf("import pytest is valid test code: %v", err)
	}
}

func TestCheckPythonSourceValid_skipsNonPythonFiles(t *testing.T) {
	// playwright.config.js contains "python3 -m uvicorn" as a shell command string —
	// this must not be rejected since it is a JavaScript file, not Python.
	src := []byte("module.exports = defineConfig({\n  webServer: {\n    command: \"python3 -m uvicorn main:app --host 127.0.0.1 --port 8000\",\n  }\n});\n")
	if err := CheckPythonSourceValid(src, "defender/playwright.config.js"); err != nil {
		t.Fatalf("JS file with python3 -m in string should not be rejected: %v", err)
	}
}
