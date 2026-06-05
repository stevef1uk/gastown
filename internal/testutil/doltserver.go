//go:build !windows

package testutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql" // required by testcontainers Dolt module
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/dolt"
)

// DoltDockerImage is the Docker image used for Dolt test containers.
// DOLT_ROOT_HOST=% tells the entrypoint to create root@'%' (available
// since Dolt 1.46.0), which lets testcontainers connect via TCP.
const DoltDockerImage = "dolthub/dolt-sql-server:1.83.0"

var (
	doltCtr     *dolt.DoltContainer
	doltCtrMu   sync.Mutex
	doltCtrErr  error
	doltCtrPort string
	dockerOnce  sync.Once
	dockerAvail bool
)

// isDockerAvailable returns true if the Docker daemon is reachable.
// The result is cached after the first call.
func isDockerAvailable() bool {
	dockerOnce.Do(func() {
		dockerAvail = exec.Command("docker", "info").Run() == nil
	})
	return dockerAvail
}

// isTransientDoltContainerErr returns true for Docker/Dolt startup flakes that
// often clear on retry (Ryuk reaper still removing, Dolt SQL ping not ready).
func isTransientDoltContainerErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "unexpected container status") && strings.Contains(msg, "removing") {
		return true
	}
	return strings.Contains(msg, "error pinging db") ||
		strings.Contains(msg, "invalid connection") ||
		strings.Contains(msg, "unexpected EOF") ||
		strings.Contains(msg, "connection refused")
}

// runDoltContainerWithRetry calls dolt.Run, retrying on transient startup errors.
func runDoltContainerWithRetry(ctx context.Context) (*dolt.DoltContainer, error) {
	const maxRetries = 5
	delay := 2 * time.Second
	var lastErr error
	for attempt := range maxRetries {
		ctr, err := dolt.Run(ctx, DoltDockerImage,
			dolt.WithDatabase("gt_test"),
			testcontainers.WithEnv(map[string]string{"DOLT_ROOT_HOST": "%"}),
		)
		if err == nil {
			return ctr, nil
		}
		lastErr = err
		if !isTransientDoltContainerErr(err) {
			return nil, err
		}
		if attempt < maxRetries-1 {
			time.Sleep(delay)
			if delay < 8*time.Second {
				delay *= 2
			}
		}
	}
	return nil, lastErr
}

// startSharedDoltContainer starts the shared Dolt container and sets
// GT_DOLT_PORT and BEADS_DOLT_PORT process-wide.
func startSharedDoltContainer() {
	ctx := context.Background()
	ctr, err := runDoltContainerWithRetry(ctx)
	if err != nil {
		doltCtrErr = fmt.Errorf("starting Dolt container: %w", err)
		return
	}

	p, err := ctr.MappedPort(ctx, "3306/tcp")
	if err != nil {
		doltCtrErr = fmt.Errorf("getting mapped port: %w", err)
		_ = testcontainers.TerminateContainer(ctr)
		return
	}

	doltCtr = ctr
	doltCtrPort = p.Port()
	os.Setenv("GT_DOLT_PORT", doltCtrPort)    //nolint:tenv // intentional process-wide env
	os.Setenv("BEADS_DOLT_PORT", doltCtrPort) //nolint:tenv // intentional process-wide env
}

// StartIsolatedDoltContainer starts a per-test Dolt container and returns the
// mapped host port. GT_DOLT_PORT is set via t.Setenv (scoped to the test).
// The container is terminated automatically when the test finishes.
func StartIsolatedDoltContainer(t *testing.T) string {
	t.Helper()
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping test")
	}

	ctx := context.Background()
	ctr, err := runDoltContainerWithRetry(ctx)
	if err != nil {
		t.Fatalf("starting Dolt container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			t.Logf("terminating Dolt container: %v", err)
		}
	})

	port, err := ctr.MappedPort(ctx, "3306/tcp")
	if err != nil {
		t.Fatalf("getting mapped port: %v", err)
	}

	portStr := port.Port()
	t.Setenv("GT_DOLT_PORT", portStr)
	t.Setenv("BEADS_DOLT_PORT", portStr)
	return portStr
}

// ensureSharedDoltContainer starts the shared Dolt container once; retries when
// a prior attempt failed with a transient Docker/Dolt startup error.
func ensureSharedDoltContainer() error {
	doltCtrMu.Lock()
	defer doltCtrMu.Unlock()
	if doltCtr != nil {
		return nil
	}
	if doltCtrErr != nil && !isTransientDoltContainerErr(doltCtrErr) {
		return doltCtrErr
	}
	doltCtrErr = nil
	startSharedDoltContainer()
	return doltCtrErr
}

// EnsureDoltContainerForTestMain starts a shared Dolt container for use in
// TestMain functions. Call TerminateDoltContainer() after m.Run() to clean up.
// Sets both GT_DOLT_PORT and BEADS_DOLT_PORT process-wide.
func EnsureDoltContainerForTestMain() error {
	if !isDockerAvailable() {
		return fmt.Errorf("Docker not available")
	}
	return ensureSharedDoltContainer()
}

// RequireDoltContainer ensures a shared Dolt container is running. Skips the
// test if Docker is not available.
func RequireDoltContainer(t *testing.T) {
	t.Helper()
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping test")
	}
	if err := ensureSharedDoltContainer(); err != nil {
		t.Fatalf("Dolt container setup failed: %v", err)
	}
}

// DoltContainerAddr returns the address (host:port) of the Dolt container.
func DoltContainerAddr() string {
	return "127.0.0.1:" + doltCtrPort
}

// DoltContainerPort returns the mapped host port of the Dolt container.
func DoltContainerPort() string {
	return doltCtrPort
}

// TerminateDoltContainer stops and removes the shared Dolt container.
// Called from TestMain after m.Run().
func TerminateDoltContainer() {
	doltCtrMu.Lock()
	defer doltCtrMu.Unlock()
	if doltCtr != nil {
		_ = testcontainers.TerminateContainer(doltCtr)
		doltCtr = nil
	}
	doltCtrErr = nil
	doltCtrPort = ""
}
