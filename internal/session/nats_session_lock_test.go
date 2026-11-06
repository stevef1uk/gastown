package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestNatsSessionLifecycleLock(t *testing.T) {
	root := t.TempDir()
	first, err := lockNatsSession(context.Background(), root, "planner")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	if second, err := lockNatsSession(ctx, root, "planner"); err == nil {
		second.Unlock()
		t.Fatal("same session acquired concurrently")
	}
	other, err := lockNatsSession(context.Background(), root, "tester")
	if err != nil {
		t.Fatal(err)
	}
	other.Unlock()
}

func TestNatsSessionPIDCleanupPreservesReplacement(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".gt-nats-pids")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "planner")
	if err := os.WriteFile(path, []byte("222"), 0644); err != nil {
		t.Fatal(err)
	}
	removeNatsSessionPIDIfOwned(root, "planner", 111)
	if data, err := os.ReadFile(path); err != nil || string(data) != "222" {
		t.Fatalf("replacement lost: %q, %v", data, err)
	}
	removeNatsSessionPIDIfOwned(root, "planner", 222)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owner record remains: %v", err)
	}
}

func TestNatsStartRejectsExistingSessionBeforeSpawn(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".gt-nats-pids")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "planner")
	pid := strconv.Itoa(os.Getpid())
	if err := os.WriteFile(path, []byte(pid), 0644); err != nil {
		t.Fatal(err)
	}
	p := &NatsProvider{townRoot: root}
	if err := p.startInternal(context.Background(), "planner", root, "must not spawn", nil, false); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("got %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != pid {
		t.Fatalf("existing PID changed: %q, %v", data, err)
	}
}
