package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDockerVerifyPreservesFailureExitStatus(t *testing.T) {
	bin := t.TempDir()
	stub := "#!/bin/sh\ncase \"$1\" in\nbuild) exit \"${BUILD_EXIT:-0}\";;\nup) exit \"${TEST_EXIT:-0}\";;\nrmi) exit 1;;\nesac\nexit 0\n"
	for _, name := range []string{"docker-compose", "docker"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(stub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, input := range []string{
		"docker-compose up --exit-code-from playwright",
		"docker-compose build --no-cache && docker-compose up --exit-code-from playwright && docker rmi test-app:latest 2>/dev/null || true && docker image prune -f",
	} {
		command := NormalizeDockerQACommand(input)
		if again := NormalizeDockerQACommand(command); again != command {
			t.Fatalf("normalization is not idempotent: %s -> %s", command, again)
		}
		for _, tc := range []struct {
			name, build, test string
			want              int
		}{
			{"build failure", "17", "0", 17},
			{"test failure", "0", "23", 23},
			{"successful tests with failed image removal", "0", "0", 0},
		} {
			t.Run(tc.name, func(t *testing.T) {
				cmd := exec.Command("sh", "-c", command)
				cmd.Env = append(os.Environ(), "BUILD_EXIT="+tc.build, "TEST_EXIT="+tc.test)
				out, err := cmd.CombinedOutput()
				if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.want {
					t.Fatalf("want exit %d, got %v: %s\n%s", tc.want, err, out, command)
				}
			})
		}
	}
}
