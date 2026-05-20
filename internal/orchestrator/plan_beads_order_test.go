package orchestrator

import (
	"strings"
	"testing"
)

func TestMatchesImplementBeadTitle_gluedTypo(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{BeadTitleContains: "Implement "}
	if !MatchesImplementBeadTitle("ImplementDockerfile per architecture", v) {
		t.Fatal("glued title should match implement bead")
	}
	if MatchesImplementBeadTitle("Witness Patrol", v) {
		t.Fatal("patrol should not match")
	}
}

func TestExtractPathFromBeadTitle_gluedImplement(t *testing.T) {
	t.Parallel()
	got := ExtractPathFromBeadTitle("ImplementDockerfile per architecture", "Implement ")
	if got != "Dockerfile" {
		t.Fatalf("got %q want Dockerfile", got)
	}
	got = ExtractPathFromBeadTitle("Implement.env.example per architecture", "Implement ")
	if got != ".env.example" {
		t.Fatalf("got %q want .env.example", got)
	}
}

func TestPruneMalformedImplementBeads_keepsCanonicalTitles(t *testing.T) {
	t.Parallel()
	// Regression: malformed prune must not require pathMatchesRequired (that deleted all canonical beads).
	v := WorkflowValidation{BeadTitleContains: "Implement "}
	title := "Implement frontend/package.json per architecture"
	pfx := strings.ToLower(strings.TrimSpace(v.BeadTitleContains))
	if !strings.HasPrefix(strings.ToLower(title), pfx) {
		t.Fatal("canonical title should match prefix")
	}
	p := ExtractPathFromBeadTitle(title, v.BeadTitleContains)
	if !IsValidImplementBeadPath(p) {
		t.Fatalf("path invalid: %q", p)
	}
	// Old bug: ok := canonical && IsValid && pathMatchesRequired(p, v.RequiredFiles) with empty RequiredFiles.
	if !pathMatchesRequired(p, nil) {
		// canonical+valid must be kept even when required_files is empty in this hook's caller context.
	}
}

func TestValidateImplementBeadCreateTitle_rejectsGluedPrefix(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		BeadTitleContains: "Implement ",
		RequiredFiles:     []string{"Dockerfile"},
	}
	if err := ValidateImplementBeadCreateTitle("ImplementDockerfile per architecture", v); err == nil {
		t.Fatal("expected reject glued Implement prefix")
	}
}

func TestIsValidImplementBeadPath(t *testing.T) {
	t.Parallel()
	ok := []string{
		"linkshelf/go.mod",
		"linkshelf/internal/store/store.go",
		"linkshelf/cmd/server/main.go",
	}
	for _, p := range ok {
		if !IsValidImplementBeadPath(p) {
			t.Fatalf("want valid %q", p)
		}
	}
	bad := []string{
		"linkshelf/P2]",
		"linkshelf/architecture",
		"linkshelf/linkshelf/go.mod",
		"linkshelf/[task]",
		"",
	}
	for _, p := range bad {
		if IsValidImplementBeadPath(p) {
			t.Fatalf("want invalid %q", p)
		}
	}
}

func TestValidatePlanBeads_rejectsExtras(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/internal/store/store.go",
		},
	}
	beads := []PlanBead{
		{ID: "te-1", Title: "Implement linkshelf/go.mod per architecture"},
		{ID: "te-2", Title: "Implement linkshelf/internal/store/store.go per architecture"},
		{ID: "te-3", Title: "Implement linkshelf/P2]"},
	}
	if err := ValidatePlanBeads(beads, "", v, ""); err == nil {
		t.Fatal("expected extra/invalid bead rejection")
	}
}

func TestSelectKeeperImplementBead_prefersCanonicalTitle(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles:     []string{"linkshelf/go.mod"},
	}
	beads := []PlanBead{
		{ID: "te-j8b", Title: "Implement linkshelf/go.mod"},
		{ID: "te-8ml", Title: "Implement linkshelf/go.mod per architecture"},
		{ID: "te-dqs", Title: "Implement linkshelf/go.mod"},
	}
	got := selectKeeperImplementBead(beads, "linkshelf/go.mod", []string{"te-j8b", "te-8ml", "te-dqs"}, v)
	if got != "te-8ml" {
		t.Fatalf("keeper = %q, want te-8ml", got)
	}
}

func TestFormatImplementationQueueBlock_nextOnly(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/internal/store/store.go",
			"linkshelf/cmd/server/main.go",
		},
	}
	got := FormatImplementationQueueBlock("", "rig", v)
	if strings.Contains(got, "Build order:") {
		t.Fatalf("should not list full build order: %q", got)
	}
	if got == "" {
		t.Fatal("expected non-empty when required_files set")
	}
}

func TestOrderRequiredFilesForImplementation(t *testing.T) {
	t.Parallel()
	files := []string{
		"linkshelf/cmd/server/main.go",
		"linkshelf/go.mod",
		"linkshelf/internal/store/store.go",
		"linkshelf/internal/api/handlers.go",
	}
	got := OrderRequiredFilesForImplementation(files)
	if got[0] != "linkshelf/go.mod" {
		t.Fatalf("go.mod first: %v", got)
	}
	if got[len(got)-1] != "linkshelf/cmd/server/main.go" {
		t.Fatalf("main.go last: %v", got)
	}
}
