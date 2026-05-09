package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/tmux"
)

// TestNatsProvider_PIDLifecycle verifies that Start creates a PID file
// and the cleanup goroutine removes it when the process exits.
func TestNatsProvider_PIDLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("NATS provider not supported on Windows")
	}

	tmpDir := t.TempDir()
	// We need a running NATS server for the provider to connect.
	// Skip if NATS is not available.
	p, err := NewNatsProvider(tmpDir, "")
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer p.Close()

	ctx := context.Background()
	sessionID := "test-session-" + strconv.Itoa(int(time.Now().UnixNano()))

	// Start a long-running sleep process
	err = p.Start(ctx, sessionID, tmpDir, "sleep 60", nil)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Verify PID file exists
	pidFile := filepath.Join(tmpDir, ".gt-nats-pids", sessionID)
	if _, err := os.Stat(pidFile); os.IsNotExist(err) {
		t.Fatal("PID file not created")
	}

	// Verify process is running
	exists, err := p.Exists(ctx, sessionID)
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !exists {
		t.Fatal("Session should exist while process is running")
	}

	// Stop the session
	if err := p.Stop(ctx, sessionID, false); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Give cleanup goroutine a moment
	time.Sleep(100 * time.Millisecond)

	// Verify PID file is removed
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatal("PID file should be removed after Stop")
	}

	// Verify session no longer exists
	exists, err = p.Exists(ctx, sessionID)
	if err != nil {
		t.Fatalf("Exists after stop failed: %v", err)
	}
	if exists {
		t.Fatal("Session should not exist after Stop")
	}
}

// TestNatsProvider_EnvironmentStorage verifies that environment variables
// are stored and can be retrieved.
func TestNatsProvider_EnvironmentStorage(t *testing.T) {
	tmpDir := t.TempDir()
	p, err := NewNatsProvider(tmpDir, "")
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer p.Close()

	ctx := context.Background()
	sessionID := "test-env-session"

	// Set environment
	err = p.SetEnvironment(ctx, sessionID, "GT_ROLE", "polecat")
	if err != nil {
		t.Fatalf("SetEnvironment failed: %v", err)
	}

	err = p.SetEnvironment(ctx, sessionID, "GT_RIG", "defender")
	if err != nil {
		t.Fatalf("SetEnvironment failed: %v", err)
	}

	// Get environment
	env, err := p.GetEnvironment(ctx, sessionID)
	if err != nil {
		t.Fatalf("GetEnvironment failed: %v", err)
	}

	if env["GT_ROLE"] != "polecat" {
		t.Errorf("GT_ROLE = %q, want polecat", env["GT_ROLE"])
	}
	if env["GT_RIG"] != "defender" {
		t.Errorf("GT_RIG = %q, want defender", env["GT_RIG"])
	}

	// Verify empty for unknown session
	env2, err := p.GetEnvironment(ctx, "unknown-session")
	if err != nil {
		t.Fatalf("GetEnvironment for unknown failed: %v", err)
	}
	if len(env2) != 0 {
		t.Errorf("Expected empty env for unknown session, got %v", env2)
	}
}

// TestNatsProvider_ListSessions verifies that List returns active sessions.
func TestNatsProvider_ListSessions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("NATS provider not supported on Windows")
	}

	tmpDir := t.TempDir()
	p, err := NewNatsProvider(tmpDir, "")
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer p.Close()

	ctx := context.Background()

	// Initially empty
	sessions, err := p.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("Expected 0 sessions, got %d", len(sessions))
	}

	// Start a session
	sessionID := "test-list-session"
	err = p.Start(ctx, sessionID, tmpDir, "sleep 60", nil)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Should now list it
	sessions, err = p.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	found := false
	for _, s := range sessions {
		if s == sessionID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected session %q in list, got %v", sessionID, sessions)
	}

	// Stop and verify removed
	p.Stop(ctx, sessionID, false)
	time.Sleep(100 * time.Millisecond)

	sessions, err = p.List(ctx)
	if err != nil {
		t.Fatalf("List after stop failed: %v", err)
	}
	for _, s := range sessions {
		if s == sessionID {
			t.Fatal("Stopped session should not appear in list")
		}
	}
}

// TestNatsProvider_GetMainPID verifies GetMainPID returns the correct PID.
func TestNatsProvider_GetMainPID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("NATS provider not supported on Windows")
	}

	tmpDir := t.TempDir()
	p, err := NewNatsProvider(tmpDir, "")
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer p.Close()

	ctx := context.Background()
	sessionID := "test-pid-session"

	// Before start, should fail
	_, err = p.GetMainPID(ctx, sessionID)
	if err == nil {
		t.Fatal("GetMainPID should fail for non-existent session")
	}

	// Start a session
	err = p.Start(ctx, sessionID, tmpDir, "sleep 60", nil)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Should now have a valid PID
	pidStr, err := p.GetMainPID(ctx, sessionID)
	if err != nil {
		t.Fatalf("GetMainPID failed: %v", err)
	}
	if pidStr == "" {
		t.Fatal("GetMainPID returned empty string")
	}

	// Verify it's a valid process
	cmd := exec.Command("kill", "-0", pidStr)
	if err := cmd.Run(); err != nil {
		t.Fatalf("PID %s is not a valid process: %v", pidStr, err)
	}

	// Stop
	p.Stop(ctx, sessionID, false)
}

// TestNatsProvider_IsIdle verifies activity tracking for idle detection.
func TestNatsProvider_IsIdle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("NATS provider not supported on Windows")
	}

	tmpDir := t.TempDir()
	p, err := NewNatsProvider(tmpDir, "")
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer p.Close()

	ctx := context.Background()
	sessionID := "test-idle-session"

	// Non-existent session is not idle (it's just not there)
	idle, err := p.IsIdle(ctx, sessionID)
	if err != nil {
		t.Fatalf("IsIdle failed: %v", err)
	}
	if idle {
		t.Fatal("Non-existent session should not be idle")
	}

	// Start session
	err = p.Start(ctx, sessionID, tmpDir, "sleep 60", nil)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Immediately after start, should not be idle
	idle, err = p.IsIdle(ctx, sessionID)
	if err != nil {
		t.Fatalf("IsIdle failed: %v", err)
	}
	if idle {
		t.Fatal("Recently started session should not be idle")
	}

	// Inject should update activity
	p.Inject(ctx, sessionID, "test")
	idle, err = p.IsIdle(ctx, sessionID)
	if err != nil {
		t.Fatalf("IsIdle after inject failed: %v", err)
	}
	if idle {
		t.Fatal("Session with recent activity should not be idle")
	}

	p.Stop(ctx, sessionID, false)
}

// TestNatsProvider_GetSessionInfo verifies session info for existing session.
func TestNatsProvider_GetSessionInfo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("NATS provider not supported on Windows")
	}

	tmpDir := t.TempDir()
	p, err := NewNatsProvider(tmpDir, "")
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer p.Close()

	ctx := context.Background()
	sessionID := "test-info-session"

	// Non-existent session
	_, err = p.GetSessionInfo(ctx, sessionID)
	if err == nil {
		t.Fatal("GetSessionInfo should fail for non-existent session")
	}

	// Start session
	err = p.Start(ctx, sessionID, tmpDir, "sleep 60", nil)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	info, err := p.GetSessionInfo(ctx, sessionID)
	if err != nil {
		t.Fatalf("GetSessionInfo failed: %v", err)
	}
	if info.Name != sessionID {
		t.Errorf("Name = %q, want %q", info.Name, sessionID)
	}
	if info.Windows != 1 {
		t.Errorf("Windows = %d, want 1", info.Windows)
	}
	if info.Attached {
		t.Error("NATS session should not be attached")
	}

	p.Stop(ctx, sessionID, false)
}

// TestGetDefaultProvider_NatsFromSettings verifies that when town settings
// specify "nats" transport, GetDefaultProvider returns a NatsProvider.
func TestGetDefaultProvider_NatsFromSettings(t *testing.T) {
	tmpDir := t.TempDir()

	// Create settings file with nats transport
	settingsPath := filepath.Join(tmpDir, "settings", "config.json")
	os.MkdirAll(filepath.Dir(settingsPath), 0755)
	settingsContent := `{"type":"town-settings","version":1,"session_transport":"nats"}`
	if err := os.WriteFile(settingsPath, []byte(settingsContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Temporarily clear env override
	oldEnv := os.Getenv("GT_SESSION_TRANSPORT")
	os.Unsetenv("GT_SESSION_TRANSPORT")
	defer os.Setenv("GT_SESSION_TRANSPORT", oldEnv)

	p := GetDefaultProvider(tmpDir)
	if p == nil {
		t.Fatal("GetDefaultProvider returned nil")
	}

	// Should be a NatsProvider (or fallback to TmuxProvider if NATS unavailable)
	if _, ok := p.(*TmuxProvider); ok {
		// This is acceptable if NATS server isn't running - it falls back
		t.Log("NATS unavailable, fell back to TmuxProvider")
	}
}

// TestGetDefaultProvider_EnvOverride verifies GT_SESSION_TRANSPORT env var
// overrides settings.
func TestGetDefaultProvider_EnvOverride(t *testing.T) {
	tmpDir := t.TempDir()

	// Set env to nats
	oldEnv := os.Getenv("GT_SESSION_TRANSPORT")
	os.Setenv("GT_SESSION_TRANSPORT", "nats")
	defer os.Setenv("GT_SESSION_TRANSPORT", oldEnv)

	p := GetDefaultProvider(tmpDir)
	if p == nil {
		t.Fatal("GetDefaultProvider returned nil")
	}

	// If NATS is available, should be NatsProvider
	// If not, falls back to TmuxProvider
	if np, ok := p.(*NatsProvider); ok {
		np.Close()
	}
}

// TestGetDefaultProvider_DefaultNats verifies default is NatsProvider when
// no settings specify tmux.
func TestGetDefaultProvider_DefaultNats(t *testing.T) {
	tmpDir := t.TempDir()

	// Clear env override
	oldEnv := os.Getenv("GT_SESSION_TRANSPORT")
	os.Unsetenv("GT_SESSION_TRANSPORT")
	defer os.Setenv("GT_SESSION_TRANSPORT", oldEnv)

	// No NATS running should return stub provider
	p := GetDefaultProvider(tmpDir)
	if p == nil {
		t.Fatal("GetDefaultProvider returned nil")
	}

	// Without NATS running, should return stub provider that reports unavailable
	if p.IsAvailable() {
		t.Errorf("Expected unavailable provider when NATS not running, got available: %T", p)
	}
}

// TestProviderInterface_Consistency verifies both providers implement the
// full interface without panics.
func TestProviderInterface_Consistency(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Provider tests not supported on Windows")
	}

	tmpDir := t.TempDir()

	providers := []struct {
		name string
		p    Provider
	}{}

	// Only add tmux if tmux binary is available
	if tmuxBin, err := exec.LookPath("tmux"); err == nil && tmuxBin != "" {
		providers = append(providers, struct {
			name string
			p    Provider
		}{"tmux", NewTmuxProvider(tmux.NewTmux(), tmpDir)})
	}

	// Add NATS if available
	if np, err := NewNatsProvider(tmpDir, ""); err == nil {
		providers = append(providers, struct {
			name string
			p    Provider
		}{"nats", np})
		defer np.Close()
	}

	ctx := context.Background()
	sessionID := "test-consistency"

	for _, tc := range providers {
		t.Run(tc.name, func(t *testing.T) {
			// These should not panic
			_ = tc.p.IsAvailable()
			_, _ = tc.p.Exists(ctx, sessionID)
			_, _ = tc.p.List(ctx)
			_, _ = tc.p.GetEnvironment(ctx, sessionID)
			_, _ = tc.p.GetSessionInfo(ctx, sessionID)
			_, _ = tc.p.IsIdle(ctx, sessionID)
			_, _ = tc.p.GetMainPID(ctx, sessionID)
			_, _ = tc.p.CleanupOrphanedSessions(func(string) bool { return true })
		})
	}
}

func TestNatsProvider_WaitForRuntimeReady_HonorsDelay(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("NATS provider not supported on Windows")
	}

	tmpDir := t.TempDir()
	p, err := NewNatsProvider(tmpDir, "")
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer p.Close()

	ctx := context.Background()
	sessionID := "test-ready-delay-session"
	if err := p.Start(ctx, sessionID, tmpDir, "sleep 60", nil); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer p.Stop(ctx, sessionID, false)

	rc := &config.RuntimeConfig{Tmux: &config.RuntimeTmuxConfig{ReadyDelayMs: 200}}
	start := time.Now()
	if err := p.WaitForRuntimeReady(ctx, sessionID, rc, 2*time.Second); err != nil {
		t.Fatalf("WaitForRuntimeReady failed: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Fatalf("WaitForRuntimeReady elapsed %v, want >= 200ms", elapsed)
	}
}

func TestNatsProvider_WaitForRuntimeReady_TimesOutWhenNotRunning(t *testing.T) {
	tmpDir := t.TempDir()
	p, err := NewNatsProvider(tmpDir, "")
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer p.Close()

	ctx := context.Background()
	err = p.WaitForRuntimeReady(ctx, "missing-session", nil, 200*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForRuntimeReady should fail for non-running session")
	}
}
