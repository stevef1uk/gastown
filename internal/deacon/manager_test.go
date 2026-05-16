package deacon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/session"
)

// mockProvider implements session.Provider for testing.
type mockProvider struct {
	existsResult  bool
	existsErr     error
	stopErr       error
	listResult    []string
	listErr       error
	sessionInfo   *session.SessionInfo
	sessionInfoErr error

	// Call tracking
	stopCalls  []string
	startCalls []sessionStartCall
}

type sessionStartCall struct {
	opts session.StartOptions
}

func (m *mockProvider) IsAvailable() bool { return true }
func (m *mockProvider) Start(ctx context.Context, opts session.StartOptions) error {
	m.startCalls = append(m.startCalls, sessionStartCall{opts})
	return nil
}
func (m *mockProvider) Stop(ctx context.Context, sessionID string, graceful bool) error {
	m.stopCalls = append(m.stopCalls, sessionID)
	return m.stopErr
}
func (m *mockProvider) Exists(ctx context.Context, sessionID string) (bool, error) {
	return m.existsResult, m.existsErr
}
func (m *mockProvider) List(ctx context.Context) ([]string, error) {
	return m.listResult, m.listErr
}
func (m *mockProvider) Inject(ctx context.Context, sessionID string, data string) error { return nil }
func (m *mockProvider) GetEnvironment(ctx context.Context, sessionID string) (map[string]string, error) {
	return nil, nil
}
func (m *mockProvider) SetEnvironment(ctx context.Context, sessionID, key, value string) error { return nil }
func (m *mockProvider) UnsetEnvironment(ctx context.Context, sessionID, key string) error {
	return nil
}
func (m *mockProvider) SetGlobalEnvironment(key, value string) error { return nil }
func (m *mockProvider) UnsetGlobalEnvironment(key string) error { return nil }
func (m *mockProvider) SetRemainOnExit(ctx context.Context, sessionID string, enabled bool) error { return nil }
func (m *mockProvider) Configure(ctx context.Context, sessionID string, cfg any) error { return nil }
func (m *mockProvider) GetMainPID(ctx context.Context, sessionID string) (string, error) { return "", nil }
func (m *mockProvider) GetServerPID(ctx context.Context) (int, error) { return 0, nil }
func (m *mockProvider) GetSessionInfo(ctx context.Context, sessionID string) (*session.SessionInfo, error) {
	return nil, m.sessionInfoErr
}
func (m *mockProvider) AttachSession(ctx context.Context, sessionID string) error { return nil }
func (m *mockProvider) CapturePane(ctx context.Context, sessionID string, lines int) (string, error) { return "", nil }
func (m *mockProvider) SendKeys(ctx context.Context, sessionID string, keys string) error { return nil }
func (m *mockProvider) SendKeysDebounced(ctx context.Context, sessionID string, keys string, debounceMs int) error { return nil }
func (m *mockProvider) IsIdle(ctx context.Context, sessionID string) (bool, error) { return true, nil }
func (m *mockProvider) IsAgentRunning(ctx context.Context, sessionID string) (bool, error) {
	return m.Exists(ctx, sessionID)
}
func (m *mockProvider) CleanupOrphanedSessions(isGTSession func(string) bool) (int, error) { return 0, nil }
func (m *mockProvider) StopAllSessions(ctx context.Context) error { return nil }
func (m *mockProvider) NudgeSession(ctx context.Context, sessionID, message, sender string) error { return nil }
func (m *mockProvider) EnsureSessionFresh(ctx context.Context, opts session.StartOptions) error {
	return nil
}
func (m *mockProvider) WaitForRuntimeReady(ctx context.Context, sessionID string, rc *config.RuntimeConfig, timeout time.Duration) error {
	return nil
}
func (m *mockProvider) CheckSessionHealth(ctx context.Context, sessionID string, maxInactivity time.Duration) constants.ZombieStatus {
	return constants.SessionHealthy
}
func (m *mockProvider) GetLastActivity(ctx context.Context, sessionID string) (time.Time, error) {
	return time.Time{}, nil
}
func (m *mockProvider) GetWorkDir(ctx context.Context, sessionID string) (string, error) {
	return "", nil
}
func (m *mockProvider) SendNotificationBanner(ctx context.Context, sessionID, from, subject string) error {
	return nil
}

func newTestManager(townRoot string, mock *mockProvider) *Manager {
	return &Manager{
		townRoot: townRoot,
		sp:       mock,
	}
}

func TestNewManager(t *testing.T) {
	m := NewManager("/tmp/test-town")
	if m.townRoot != "/tmp/test-town" {
		t.Errorf("townRoot = %q, want %q", m.townRoot, "/tmp/test-town")
	}
	if m.sp == nil {
		t.Error("sp should not be nil")
	}
}

func TestManager_SessionName(t *testing.T) {
	m := NewManager("/tmp/test-town")
	name := m.SessionName()
	if name == "" {
		t.Error("SessionName() should not be empty")
	}
	// Should match package-level SessionName
	if name != SessionName() {
		t.Errorf("method SessionName() = %q, package SessionName() = %q", name, SessionName())
	}
}

func TestManager_deaconDir(t *testing.T) {
	m := NewManager("/tmp/test-town")
	expected := filepath.Join("/tmp/test-town", "deacon")
	if m.deaconDir() != expected {
		t.Errorf("deaconDir() = %q, want %q", m.deaconDir(), expected)
	}
}

func TestStart_AlreadyRunning(t *testing.T) {
	mock := &mockProvider{
		existsResult: true,
	}
	m := newTestManager(t.TempDir(), mock)

	err := m.Start("", false)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("Start() error = %v, want ErrAlreadyRunning", err)
	}
}

func TestStart_NoExistingSession(t *testing.T) {
	// No existing session - Start proceeds to create one.
	// Will hit config/runtime calls which may error in test env.
	mock := &mockProvider{
		existsResult: false,
	}
	m := newTestManager(t.TempDir(), mock)

	_ = m.Start("", false)

	// Should NOT have tried to stop anything
	if len(mock.stopCalls) != 0 {
		t.Errorf("expected 0 stop calls, got %d", len(mock.stopCalls))
	}
}

func TestStart_ExistsError(t *testing.T) {
	// Exists error is ignored (running, _ := ...).
	// When Exists errors, running=false, so Start proceeds normally.
	mock := &mockProvider{
		existsResult: false,
		existsErr:    errors.New("provider not available"),
	}
	m := newTestManager(t.TempDir(), mock)

	_ = m.Start("", false)

	// Should NOT have tried to stop anything
	if len(mock.stopCalls) != 0 {
		t.Errorf("expected 0 stop calls when Exists errors, got %d", len(mock.stopCalls))
	}
}

func TestStop_NotRunning(t *testing.T) {
	mock := &mockProvider{
		existsResult: false,
	}
	m := newTestManager(t.TempDir(), mock)

	err := m.Stop()
	if !errors.Is(err, ErrNotRunning) {
		t.Errorf("Stop() error = %v, want ErrNotRunning", err)
	}
}

func TestStop_ExistsError(t *testing.T) {
	sessionErr := errors.New("provider crashed")
	mock := &mockProvider{
		existsErr: sessionErr,
	}
	m := newTestManager(t.TempDir(), mock)

	err := m.Stop()
	if err == nil {
		t.Fatal("Stop() should return error when Exists fails")
	}
	if !errors.Is(err, sessionErr) {
		t.Errorf("Stop() error = %v, should wrap %v", err, sessionErr)
	}
}

func TestStop_Success(t *testing.T) {
	mock := &mockProvider{
		existsResult: true,
	}
	m := newTestManager(t.TempDir(), mock)

	err := m.Stop()
	if err != nil {
		t.Errorf("Stop() error = %v, want nil", err)
	}
	if len(mock.stopCalls) != 1 {
		t.Errorf("expected 1 stop call, got %d", len(mock.stopCalls))
	}
}

func TestStop_StopFails(t *testing.T) {
	stopErr := errors.New("permission denied")
	mock := &mockProvider{
		existsResult: true,
		stopErr:      stopErr,
	}
	m := newTestManager(t.TempDir(), mock)

	err := m.Stop()
	if err == nil {
		t.Fatal("Stop() should return error when stop fails")
	}
	if !errors.Is(err, stopErr) {
		t.Errorf("Stop() error = %v, should wrap %v", err, stopErr)
	}
}

func TestIsRunning(t *testing.T) {
	tests := []struct {
		name    string
		running bool
		err     error
		wantRun bool
		wantErr bool
	}{
		{
			name:    "running",
			running: true,
			wantRun: true,
		},
		{
			name:    "not running",
			running: false,
			wantRun: false,
		},
		{
			name:    "error",
			err:     errors.New("provider error"),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockProvider{
				existsResult: tc.running,
				existsErr:    tc.err,
			}
			m := newTestManager(t.TempDir(), mock)

			running, err := m.IsRunning()
			if (err != nil) != tc.wantErr {
				t.Errorf("IsRunning() error = %v, wantErr = %v", err, tc.wantErr)
			}
			if running != tc.wantRun {
				t.Errorf("IsRunning() = %v, want %v", running, tc.wantRun)
			}
		})
	}
}

func TestStatus_NotRunning(t *testing.T) {
	mock := &mockProvider{
		existsResult: false,
	}
	m := newTestManager(t.TempDir(), mock)

	info, err := m.Status()
	if !errors.Is(err, ErrNotRunning) {
		t.Errorf("Status() error = %v, want ErrNotRunning", err)
	}
	if info != nil {
		t.Error("Status() should return nil info when not running")
	}
}

func TestStatus_ExistsError(t *testing.T) {
	sessionErr := errors.New("provider gone")
	mock := &mockProvider{
		existsErr: sessionErr,
	}
	m := newTestManager(t.TempDir(), mock)

	info, err := m.Status()
	if err == nil {
		t.Fatal("Status() should return error when Exists fails")
	}
	if !errors.Is(err, sessionErr) {
		t.Errorf("Status() error = %v, should wrap %v", err, sessionErr)
	}
	if info != nil {
		t.Error("Status() should return nil info on error")
	}
}

func TestStatus_Running(t *testing.T) {
	mock := &mockProvider{
		existsResult: true,
	}
	m := newTestManager(t.TempDir(), mock)

	info, err := m.Status()
	if err != nil {
		t.Errorf("Status() error = %v", err)
	}
	// For non-tmux provider, Status returns a basic info struct
	if info == nil {
		t.Error("Status() should return info when running")
	}
}
