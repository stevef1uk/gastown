package orchestrator

import (
	"strings"
	"testing"
)

// PlaywrightNPMVersion must match the version shipped inside the runner image's
// browsers (derived from PlaywrightRunnerBaseTag). A mismatch is the classic
// "Executable doesn't exist" / "Browser not installed" failure in the E2E
// container, because package.json @playwright/test is installed against the
// image's preinstalled browser binaries.
func TestPlaywrightNPMVersion_matchesRunnerBaseTag(t *testing.T) {
	got := PlaywrightNPMVersion()
	if got == "" {
		t.Fatal("PlaywrightNPMVersion() returned empty")
	}
	if !strings.Contains(PlaywrightRunnerBaseTag, got) {
		t.Fatalf("PlaywrightNPMVersion()=%q not derived from PlaywrightRunnerBaseTag=%q", got, PlaywrightRunnerBaseTag)
	}
}
