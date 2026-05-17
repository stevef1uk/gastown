package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type livenessTestProvider struct {
	stubProvider
	alive bool
}

func (p *livenessTestProvider) Exists(context.Context, string) (bool, error) {
	return true, nil
}

func (p *livenessTestProvider) IsAgentRunning(context.Context, string) (bool, error) {
	return p.alive, nil
}

func TestPipelineSessionNeedsRestart_deadAgent(t *testing.T) {
	ctx := context.Background()
	p := &livenessTestProvider{alive: false}
	if !PipelineSessionNeedsRestart(ctx, p, "/gt", "hq-planner", true) {
		t.Fatal("dead agent should need restart")
	}
}

func TestPipelineSessionNeedsRestart_aliveAgent(t *testing.T) {
	ctx := context.Background()
	p := &livenessTestProvider{alive: true}
	if PipelineSessionNeedsRestart(ctx, p, "/gt", "hq-planner", false) {
		t.Fatal("alive patrol agent should not need restart")
	}
}

func TestPipelineSessionNeedsRestart_missingOrchestratedFlag(t *testing.T) {
	ctx := context.Background()
	town := t.TempDir()
	sessionID := "te-mockrig-polecat"
	pidDir := filepath.Join(town, ".gt-nats-pids")
	if err := os.MkdirAll(pidDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, sessionID), []byte("999999\n"), 0644); err != nil {
		t.Fatal(err)
	}
	p := &livenessTestProvider{alive: true}
	if !PipelineSessionNeedsRestart(ctx, p, town, sessionID, true) {
		t.Fatal("want restart when orchestrated required but flag missing")
	}
}
