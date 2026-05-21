package natsserver

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnsureRunning_alreadyHealthy(t *testing.T) {
	resetDockerHooks(t)
	dockerInspectRunningFn = func() bool { return true }
	healthCheckFn = func(int) bool { return true }
	if err := EnsureRunning(Config{Port: 4222, MonitorPort: 8222}); err != nil {
		t.Fatal(err)
	}
	if dockerStartCalls > 0 || dockerRunCalls > 0 {
		t.Fatalf("want no docker start/run, got start=%d run=%d", dockerStartCalls, dockerRunCalls)
	}
}

func TestStart_recreatesOnFailedDockerStart(t *testing.T) {
	resetDockerHooks(t)
	dockerInspectRunningFn = func() bool { return false }
	dockerInspectExistsFn = func() bool { return true }
	dockerStartFn = func() error { return errors.New("exit status 1") }
	dockerRunFn = func(Config) error {
		dockerRunCalls++
		dockerInspectRunningFn = func() bool { return true }
		return nil
	}
	healthCheckFn = func(int) bool { return true }
	if err := Start(Config{Port: 4222, MonitorPort: 8222}); err != nil {
		t.Fatal(err)
	}
	if dockerRunCalls != 1 {
		t.Fatalf("dockerRunCalls = %d, want 1", dockerRunCalls)
	}
}

func TestIsHealthy_http(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	port := 19999
	healthCheckFn = func(monitorPort int) bool {
		if monitorPort != port {
			return false
		}
		resp, err := http.Get(srv.URL)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}
	if !IsHealthy(Config{MonitorPort: port}) {
		t.Fatal("expected healthy")
	}
}

var dockerStartCalls, dockerRunCalls int

func resetDockerHooks(t *testing.T) {
	t.Helper()
	dockerStartCalls = 0
	dockerRunCalls = 0
	dockerInspectRunningFn = func() bool { return false }
	dockerInspectExistsFn = func() bool { return false }
	dockerStartFn = func() error {
		dockerStartCalls++
		return nil
	}
	dockerRunFn = func(Config) error {
		dockerRunCalls++
		dockerInspectRunningFn = func() bool { return true }
		return nil
	}
	dockerStopFn = func() error { return nil }
	dockerRmFn = func() error { return nil }
	healthCheckFn = func(int) bool { return true }
	t.Cleanup(func() {
		dockerInspectRunningFn = dockerInspectRunning
		dockerInspectExistsFn = dockerInspectExists
		dockerStartFn = dockerStart
		dockerRunFn = dockerRun
		dockerStopFn = dockerStop
		dockerRmFn = dockerRm
		healthCheckFn = healthCheckHTTP
	})
}
