package orchestrator

import (
	"strings"
	"testing"
)

func TestValidateSummaryBeadIDs_wrongPrefix(t *testing.T) {
	known := map[string]bool{"de-5d0": true, "de-b3w": true}
	err := ValidateSummaryBeadIDs("reopen te-5d0 te-b3w for implementation", known, "de")
	if err == nil || !strings.Contains(err.Error(), "te-5d0") {
		t.Fatalf("expected wrong-prefix error, got %v", err)
	}
}

func TestValidateSummaryBeadIDs_ignoresDeliveryPhaseSlug(t *testing.T) {
	known := map[string]bool{"te-xoo": true}
	summary := "active phase api-handlers requires an implement bead for handlers.go"
	if err := ValidateSummaryBeadIDs(summary, known, "te"); err != nil {
		t.Fatalf("delivery phase slug must not be treated as bead ID: %v", err)
	}
}

func TestValidateSummaryBeadIDs_knownIDs(t *testing.T) {
	known := map[string]bool{"de-5d0": true}
	if err := ValidateSummaryBeadIDs("reopen de-5d0; stub main.py", known, "de"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSummaryBeadIDs_unknownID(t *testing.T) {
	known := map[string]bool{"de-5d0": true}
	err := ValidateSummaryBeadIDs("reopen de-zzzz not in list", known, "de")
	if err == nil {
		t.Fatal("expected unknown id error")
	}
}

func TestIsAgentIdentityBeadID(t *testing.T) {
	for _, id := range []string{"xx-mockrig-architect", "xx-mockrig-qa", "xx-mockrig-crew-steve"} {
		if !isAgentIdentityBeadID(id) {
			t.Fatalf("expected agent identity: %s", id)
		}
	}
	if isAgentIdentityBeadID("xx-abc123") {
		t.Fatal("task bead should not match")
	}
}

func TestValidateSummaryBeadIDs_ignoresPathTokens(t *testing.T) {
	known := map[string]bool{"fi-vjd": true}
	summary := "missing beads for finally/docker-compose.yml; only fi-vjd exists"
	if err := ValidateSummaryBeadIDs(summary, known, "fi"); err != nil {
		t.Fatalf("path tokens must not be treated as bead IDs: %v", err)
	}
}

func TestValidateSummaryBeadIDs_ignoresAgentIdentity(t *testing.T) {
	known := map[string]bool{"xx-5d0": true}
	if err := ValidateSummaryBeadIDs("also xx-mockrig-architect", known, "xx"); err != nil {
		t.Fatalf("agent identity beads should be ignored: %v", err)
	}
}

func TestValidateSummaryBeadIDs_ignoresEnglishHyphenatedWords(t *testing.T) {
	known := map[string]bool{"t3-t9l": true}
	summary := "re-run the verification; the bead t3-t9l is closed and co-op fails"
	if err := ValidateSummaryBeadIDs(summary, known, "t3"); err != nil {
		t.Fatalf("English hyphenated words must not be bead IDs: %v", err)
	}
}

func TestValidateSummaryBeadIDs_matchesDigitPrefixBead(t *testing.T) {
	known := map[string]bool{"t3-t9l": true}
	if err := ValidateSummaryBeadIDs("bead t3-t9l is closed", known, "t3"); err != nil {
		t.Fatalf("digit-bearing prefix bead must validate: %v", err)
	}
	err := ValidateSummaryBeadIDs("bead t3-fake9 is not real", known, "t3")
	if err == nil || !strings.Contains(err.Error(), "t3-fake9") {
		t.Fatalf("expected unknown digit-prefix bead error, got %v", err)
	}
}

func TestNormalizePytestCommand(t *testing.T) {
	in := "cd myapp/pkg && pytest -q"
	want := "cd myapp/pkg && python3 -m pytest -q"
	if got := NormalizePytestCommand(in); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	setup := "test -x .venv/bin/python3 && .venv/bin/python3 -c 'import pytest'"
	if got := NormalizePytestCommand(setup); got != setup {
		t.Fatalf("must not rewrite import check: got %q", got)
	}
}

func TestNormalizePipCommand(t *testing.T) {
	in := "pip install -r requirements.txt"
	want := "python3 -m pip install -r requirements.txt"
	if got := NormalizePipCommand(in); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	upgrade := ".venv/bin/pip install --upgrade pip"
	if got := NormalizePipCommand(upgrade); got != upgrade {
		t.Fatalf("must not rewrite package name pip: got %q", got)
	}
}

func TestNormalizeDockerQACommand(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "chained down then up",
			in:   "cd finally && docker-compose -f test/docker-compose.test.yml down && docker-compose -f test/docker-compose.test.yml up --exit-code-from playwright",
			want: "cd finally && docker-compose -f test/docker-compose.test.yml down && docker-compose -f test/docker-compose.test.yml build --no-cache && docker-compose -f test/docker-compose.test.yml up --exit-code-from playwright",
		},
		{
			name: "docker compose v2 with build flag on up",
			in:   "docker compose -f docker-compose.yml up --build --exit-code-from playwright",
			want: "docker compose -f docker-compose.yml build --no-cache && docker compose -f docker-compose.yml up --exit-code-from playwright",
		},
		{
			name: "bare compose up",
			in:   "docker-compose up --exit-code-from playwright",
			want: "docker-compose build --no-cache && docker-compose up --exit-code-from playwright",
		},
		{
			name: "already hardened is idempotent",
			in:   "docker-compose build --no-cache && docker-compose up --exit-code-from playwright",
			want: "docker-compose build --no-cache && docker-compose up --exit-code-from playwright",
		},
		{
			name: "non-docker command untouched",
			in:   "cd backend && python -m pytest -v tests/",
			want: "cd backend && python -m pytest -v tests/",
		},
		{
			name: "build only command untouched",
			in:   "docker-compose -f docker-compose.yml build --no-cache",
			want: "docker-compose -f docker-compose.yml build --no-cache",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeDockerQACommand(tc.in); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
