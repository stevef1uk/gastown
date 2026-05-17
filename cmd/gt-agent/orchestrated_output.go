package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	orchestratedRoleLog       *os.File
	orchestratedStdoutWriters io.Writer = os.Stdout
	orchestratedStderrWriters io.Writer = os.Stderr
	orchestratedMirrorOnce    sync.Once
)

// orchestratedRoleLogPath returns the familiar per-role log path (typescript) for tail -f.
func orchestratedRoleLogPath(townRoot, role, rig, polecat string) string {
	if townRoot == "" {
		return ""
	}
	switch role {
	case "deacon", "mayor", "planner", "mechanic":
		return filepath.Join(townRoot, role, "typescript")
	}
	if rig == "" {
		return ""
	}
	if polecat != "" {
		if role == "crew" {
			return filepath.Join(townRoot, rig, "crew", polecat, "typescript")
		}
		if role == "polecat" {
			return filepath.Join(townRoot, rig, "polecat", "typescript")
		}
		return filepath.Join(townRoot, rig, "polecats", polecat, "typescript")
	}
	return filepath.Join(townRoot, rig, role, "typescript")
}

// setupOrchestratedOutputMirror duplicates stdout/stderr to the role typescript path
// (e.g. <rig>/architect/typescript) without dup2, so NATS session logging still works.
func setupOrchestratedOutputMirror(townRoot, role, rig string) {
	roleLog := orchestratedRoleLogPath(townRoot, role, rig, os.Getenv("GT_POLECAT"))
	if roleLog == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(roleLog), 0755); err != nil {
		return
	}
	f, err := os.OpenFile(roleLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	orchestratedRoleLog = f
	orchestratedStdoutWriters = io.MultiWriter(os.Stdout, f)
	orchestratedStderrWriters = io.MultiWriter(os.Stderr, f)
	orchestratedMirrorOnce.Do(func() {
		agentID := role
		if rig != "" {
			agentID = rig + "/" + role
		}
		_, _ = fmt.Fprintf(f, "\n=== gt-agent orchestrated %s @ %s ===\n", agentID, time.Now().Format(time.RFC3339))
	})
}

func orchestratedPrintf(format string, args ...interface{}) {
	_, _ = fmt.Fprintf(orchestratedStdoutWriters, format, args...)
}

func orchestratedFprintfStderr(format string, args ...interface{}) {
	_, _ = fmt.Fprintf(orchestratedStderrWriters, format, args...)
}
