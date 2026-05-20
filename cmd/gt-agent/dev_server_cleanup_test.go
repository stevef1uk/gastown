package main

import "testing"

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
