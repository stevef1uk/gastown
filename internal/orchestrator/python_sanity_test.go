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
