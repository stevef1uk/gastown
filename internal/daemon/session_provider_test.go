package daemon

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/session"
)

// stubAvailable is a minimal Provider used to test ensureSessionProvider retention.
type stubAvailable struct{}

func (stubAvailable) IsAvailable() bool { return true }
func (stubAvailable) Start(context.Context, session.StartOptions) error { return nil }
func (stubAvailable) Stop(context.Context, string, bool) error          { return nil }
func (stubAvailable) Exists(context.Context, string) (bool, error)     { return false, nil }
func (stubAvailable) List(context.Context) ([]string, error)             { return nil, nil }
func (stubAvailable) Inject(context.Context, string, string) error      { return nil }
func (stubAvailable) NudgeSession(context.Context, string, string, string) error {
	return nil
}
func (stubAvailable) GetEnvironment(context.Context, string) (map[string]string, error) {
	return nil, nil
}
func (stubAvailable) SetEnvironment(context.Context, string, string, string) error { return nil }
func (stubAvailable) UnsetEnvironment(context.Context, string, string) error     { return nil }
func (stubAvailable) SetGlobalEnvironment(string, string) error                    { return nil }
func (stubAvailable) UnsetGlobalEnvironment(string) error                          { return nil }
func (stubAvailable) SetRemainOnExit(context.Context, string, bool) error          { return nil }
func (stubAvailable) Configure(context.Context, string, any) error                 { return nil }
func (stubAvailable) EnsureSessionFresh(context.Context, session.StartOptions) error {
	return nil
}
func (stubAvailable) IsAgentRunning(context.Context, string) (bool, error) { return false, nil }
func (stubAvailable) WaitForRuntimeReady(context.Context, string, *config.RuntimeConfig, time.Duration) error {
	return nil
}
func (stubAvailable) CleanupOrphanedSessions(func(string) bool) (int, error) { return 0, nil }
func (stubAvailable) StopAllSessions(context.Context) error                  { return nil }
func (stubAvailable) GetMainPID(context.Context, string) (string, error)     { return "", nil }
func (stubAvailable) GetServerPID(context.Context) (int, error)              { return 0, nil }
func (stubAvailable) IsIdle(context.Context, string) (bool, error)           { return true, nil }
func (stubAvailable) CapturePane(context.Context, string, int) (string, error) { return "", nil }
func (stubAvailable) AttachSession(context.Context, string) error            { return nil }
func (stubAvailable) SendKeysDebounced(context.Context, string, string, int) error {
	return nil
}
func (stubAvailable) GetSessionInfo(context.Context, string) (*session.SessionInfo, error) {
	return nil, nil
}
func (stubAvailable) GetWorkDir(context.Context, string) (string, error) { return "", nil }
func (stubAvailable) CheckSessionHealth(context.Context, string, time.Duration) constants.ZombieStatus {
	return constants.SessionDead
}
func (stubAvailable) SendNotificationBanner(context.Context, string, string, string) error {
	return nil
}
func (stubAvailable) GetLastActivity(context.Context, string) (time.Time, error) {
	return time.Time{}, nil
}

func TestEnsureSessionProvider_keepsAvailableProvider(t *testing.T) {
	sp := stubAvailable{}
	d := &Daemon{
		sp:     sp,
		config: DefaultConfig(t.TempDir()),
		logger: log.New(io.Discard, "", 0),
	}
	before := d.sp
	d.ensureSessionProvider()
	if d.sp != before {
		t.Fatal("expected existing available provider to be kept")
	}
}
