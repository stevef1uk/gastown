package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

var (
	orchestratedLogger   *log.Logger
	orchestratedLogFile  *os.File
)

// initOrchestratedLogger sends operational lines to stderr and, when available,
// appends to logs/sessions/<session>.log (same file nats-wrapper uses).
func initOrchestratedLogger(townRoot, sessionName string) {
	w := orchestratedStderrWriters
	if townRoot != "" && sessionName != "" {
		logDir := filepath.Join(townRoot, "logs", "sessions")
		_ = os.MkdirAll(logDir, 0755)
		path := filepath.Join(logDir, sessionName+".log")
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			orchestratedLogFile = f
			w = io.MultiWriter(w, f)
		}
	}
	orchestratedLogger = log.New(w, "[gt-agent] ", log.LstdFlags)
}

// closeOrchestratedLogger closes the session log file opened by initOrchestratedLogger.
func closeOrchestratedLogger() {
	if orchestratedLogFile != nil {
		orchestratedLogFile.Close()
		orchestratedLogFile = nil
	}
}

func orchestratedLog(format string, args ...interface{}) {
	if orchestratedLogger == nil {
		fmt.Fprintf(os.Stderr, "[gt-agent] "+format+"\n", args...)
		return
	}
	orchestratedLogger.Printf(format, args...)
}

func orchestratedQuiet() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("GT_AGENT_QUIET")), "1") ||
		strings.EqualFold(strings.TrimSpace(os.Getenv("GT_AGENT_QUIET")), "true")
}
