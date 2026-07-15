package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
)

// deleteImplementBead removes a bead by ID, ignoring "not found" errors (the bead may have
// been deleted by an earlier prune step). Returns true if the bead was actually deleted.
func deleteImplementBead(beadsDir, workDir, id string) (bool, error) {
	cmd := exec.Command("bd", "delete", id, "--force")
	cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	output := strings.TrimSpace(string(out))
	if strings.Contains(output, "not found") || strings.Contains(output, "does not exist") {
		return false, nil
	}
	return false, fmt.Errorf("bd delete %s: %w: %s", id, err, output)
}

// IsValidImplementBeadPath reports whether a path extracted from a bead title is a real repo file path.
func IsValidImplementBeadPath(path string) bool {
	path = SanitizeNativeEditRelPath(path)
	if path == "" || strings.Contains(path, "..") {
		return false
	}
	if strings.ContainsAny(path, "`") || strings.Contains(path, "**") {
		return false
	}
	if strings.ContainsAny(path, "[]") || strings.Contains(path, " per ") {
		return false
	}
	lower := strings.ToLower(path)
	if strings.Contains(lower, "command to create") || strings.Contains(lower, "per architecture") {
		return false
	}
	if strings.Contains(path, "<<<<<<<") || strings.Contains(path, ">>>>>>>") || strings.Contains(path, "=======") {
		return false
	}
	if strings.HasPrefix(lower, "command ") || strings.Contains(lower, " blocks.") {
		return false
	}
	for _, r := range path {
		if unicode.IsControl(r) {
			return false
		}
	}
	// Reject titles that glued markdown/list junk (e.g. "linkshelf/P2]", "architecture").
	if strings.HasSuffix(path, "/") || strings.HasSuffix(path, "-") {
		return false
	}
	base := filepath.Base(path)
	if base == "" || base == "." || base == "architecture" || base == "Implement" {
		return false
	}
	if strings.Contains(base, " ") {
		return false
	}
	if !strings.Contains(path, "/") && !strings.Contains(base, ".") {
		switch base {
		case "Dockerfile", "Makefile", "LICENSE", "README", "Containerfile":
			return true
		default:
			return false
		}
	}
	parts := strings.Split(path, "/")
	for _, seg := range parts {
		if seg == "" || strings.Contains(seg, "[") || strings.Contains(seg, "]") {
			return false
		}
		if seg != strings.TrimSpace(seg) || strings.HasPrefix(seg, "`") || strings.HasPrefix(seg, "**") {
			return false
		}
	}
	// Double layout prefix: linkshelf/linkshelf/...
	if len(parts) >= 2 && parts[0] == parts[1] {
		return false
	}
	return true
}

// ValidatePlanningBeadCreate guards bd create during planning (profile prefix, required_files, no duplicates).
func ValidatePlanningBeadCreate(townRoot, rig, title string, v WorkflowValidation) error {
	v = v.ForActivePhase()
	if strings.TrimSpace(title) == "" {
		return fmt.Errorf("bead title is empty")
	}
	if townRoot != "" && rig != "" && len(v.RequiredFiles) > 0 {
		covered, err := OpenImplementCoversRequiredFiles(townRoot, rig, v)
		if err != nil {
			return err
		}
		if covered {
			return fmt.Errorf("open implement beads already cover required_files — do not bd create; run bd list --status=open, expand plan.md if needed, then JSON success (or: gt rig sync-planning %s --force)", rig)
		}
	}
	return ValidateImplementBeadCreateTitle(title, v)
}

// ValidateImplementBeadCreateTitle ensures bd create titles map to a profile required path.
func ValidateImplementBeadCreateTitle(title string, v WorkflowValidation) error {
	v = v.ForActivePhase()
	pfx := v.BeadTitleContains
	if strings.TrimSpace(pfx) != "" && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(title)), strings.ToLower(pfx)) {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(title)), "implement") {
			return fmt.Errorf("bead title must start with %q before the file path (got %q)", pfx, title)
		}
	}
	path := ExtractPathFromBeadTitle(title, v.BeadTitleContains)
	path = NormalizeBeadPathForLayout(path, v.LayoutRoot)
	if !IsValidImplementBeadPath(path) {
		return fmt.Errorf("bead title must be %q<file-path> per architecture (got invalid path %q)", v.BeadTitleContains, path)
	}
	if len(v.RequiredFiles) == 0 {
		return nil
	}
	if !pathMatchesRequiredForProfile(path, v.RequiredFiles, v) {
		msg := fmt.Sprintf("bead path %q is not in active-phase required_files", path)
		if id := v.ActivePhaseID(); id != "" {
			msg += fmt.Sprintf(" (phase %q)", id)
		}
		return fmt.Errorf("%s — only create beads for: %s", msg, strings.Join(v.RequiredFiles, ", "))
	}
	return nil
}

// PathMatchesRequiredFile reports whether path is a profile required_files entry (or same basename).
func PathMatchesRequiredFile(path string, required string) bool {
	return pathMatchesRequired(path, []string{required})
}

// PathMatchesRequiredFileForProfile is like PathMatchesRequiredFile but honors exact-path profiles.
func PathMatchesRequiredFileForProfile(path string, required string, v WorkflowValidation) bool {
	return pathMatchesRequiredForProfile(path, []string{required}, v)
}

func pathMatchesRequired(path string, required []string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	for _, want := range required {
		want = filepath.ToSlash(strings.TrimSpace(want))
		if want == "" {
			continue
		}
		if path == want || filepath.Base(path) == filepath.Base(want) {
			return true
		}
	}
	return false
}

// PathMatchesImplementFile reports whether written and beadPath refer to the same implement file.
func PathMatchesImplementFile(written, beadPath string) bool {
	written = filepath.ToSlash(strings.TrimSpace(written))
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if written == "" || beadPath == "" {
		return false
	}
	if written == beadPath {
		return true
	}
	if filepath.Base(written) == filepath.Base(beadPath) {
		return true
	}
	return strings.HasSuffix(written, "/"+beadPath) || strings.HasSuffix(beadPath, "/"+written)
}

// implementBeadsHookMap provides per-(townRoot, rig) hooks, safe for t.Parallel().
// Tests should use setListImplementBeadsByStatusHook instead of assigning
// ListImplementBeadsByStatusHook directly, which is only for global overrides.
var implementBeadsHookMap sync.Map

func implementBeadsHookKey(townRoot, rig string) string {
	return townRoot + "\x00" + rig
}

// ListImplementBeadsByStatusHook is the global (non-parallel-safe) hook for bd list stubs.
// Prefer setListImplementBeadsByStatusHook for per-(townRoot, rig) overrides.
var ListImplementBeadsByStatusHook func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error)

func listImplementBeadsForGuard(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
	key := implementBeadsHookKey(townRoot, rig)
	if raw, ok := implementBeadsHookMap.Load(key); ok {
		hook, ok2 := raw.(func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error))
		if ok2 && hook != nil {
			beads, err := hook(townRoot, rig, v, status)
			if err != nil {
				return nil, err
			}
			return filterImplementBeads(beads, v), nil
		}
	}
	if ListImplementBeadsByStatusHook != nil {
		beads, err := ListImplementBeadsByStatusHook(townRoot, rig, v, status)
		if err != nil {
			return nil, err
		}
		return filterImplementBeads(beads, v), nil
	}
	return listImplementBeadsByStatus(townRoot, rig, v, status)
}

func filterImplementBeads(beads []PlanBead, v WorkflowValidation) []PlanBead {
	if len(beads) == 0 {
		return nil
	}
	out := make([]PlanBead, 0, len(beads))
	for _, b := range beads {
		// Listing open/in-progress implement beads is used for "what's open" /
		// "what can be worked on" checks. It should not filter by whether the
		// embedded path is currently part of v.RequiredFiles; that filtering is
		// validated later by plan.bead validation logic.
		if looksLikeOpenImplementBeadTitle(b.Title, v) {
			out = append(out, b)
		}
	}
	return out
}

// ImplementPathHasOnlyClosedBeads reports whether every implement bead for writtenPath is closed
// (no open or in_progress bead for that path). Used to block polecat from stomping finished files.
func ImplementPathHasOnlyClosedBeads(townRoot, rig, writtenPath string, v WorkflowValidation) (bool, error) {
	v = v.ForActivePhase()
	writtenPath = NormalizePlannerBeadPath(writtenPath, v.LayoutRoot, rig)
	if writtenPath == "" {
		return false, nil
	}
	for _, status := range []string{"open", "in_progress"} {
		beads, err := listImplementBeadsForGuard(townRoot, rig, v, status)
		if err != nil {
			return false, err
		}
		for _, b := range beads {
			p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot, rig)
			if PathMatchesImplementFileForProfile(writtenPath, p, v) {
				return false, nil
			}
		}
	}
	closed, err := listImplementBeadsForGuard(townRoot, rig, v, "closed")
	if err != nil {
		return false, err
	}
	for _, b := range closed {
		p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot, rig)
		if PathMatchesImplementFileForProfile(writtenPath, p, v) {
			return true, nil
		}
	}
	return false, nil
}

// ClosedImplementBeadForPath returns the closed implement bead ID for filePath, if all beads on that path are closed.
func ClosedImplementBeadForPath(townRoot, rig, filePath string, v WorkflowValidation) (beadID string, ok bool) {
	filePath = NormalizePlannerBeadPath(filePath, v.LayoutRoot, rig)
	closedOnly, err := ImplementPathHasOnlyClosedBeads(townRoot, rig, filePath, v)
	if err != nil || !closedOnly {
		return "", false
	}
	closed, err := listImplementBeadsForGuard(townRoot, rig, v, "closed")
	if err != nil {
		return "", false
	}
	for _, b := range closed {
		p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot, rig)
		if PathMatchesImplementFileForProfile(filePath, p, v) {
			return b.ID, true
		}
	}
	return "", false
}

// FormatClosedDependencyCompileHints explains verify failures in earlier implement files whose beads are closed.
// cmdOutput is the failed go command output; when empty, hints are conservative (resume without stored output).
func FormatClosedDependencyCompileHints(townRoot, rig, activeBeadPath string, errorPaths []string, cmdOutput string, v WorkflowValidation) string {
	activeBeadPath = filepath.ToSlash(strings.TrimSpace(activeBeadPath))
	if activeBeadPath == "" || len(errorPaths) == 0 {
		return ""
	}
	seen := map[string]bool{}
	var lines []string
	for _, p := range errorPaths {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" || p == activeBeadPath || seen[p] {
			continue
		}
		seen[p] = true
		if PathMatchesImplementWrite(p, activeBeadPath, v.RequiredFiles, v) {
			continue
		}
		id, ok := ClosedImplementBeadForPath(townRoot, rig, p, v)
		if !ok {
			continue
		}
		if !ShouldSuggestReopenClosedDep(activeBeadPath, p, cmdOutput, v) {
			continue
		}
		lines = append(lines, fmt.Sprintf(
			"- `%s` belongs to closed bead **%s** — go output cites this file (or a cross-package import). Reopen only if that file needs changes: `bd update %s --status=open` → **EDIT:** / **WRITE:** → Verify → `bd close %s`, then continue **`%s`**.",
			p, id, id, id, activeBeadPath,
		))
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace("### Reopen closed implement beads (only when go output cites that file)\n" +
		strings.Join(lines, "\n") +
		"\n\n**When go output cites a closed dependency file above:** run `CMD:` `bd update <id> --status=open` → fix that file → Verify → `bd close <id>`, then continue the active bead. " +
		"If errors are only in `*_test.go`, fix tests to match the active production file first — do not reopen other closed beads in the same package unless go output cites them. " +
		"Copy bead IDs from `bd list --status=closed`. Use JSON `failure` only if reopen/fix is impossible (name bead ID + error).")
}

// AllowedEarlierImplementDependencyWrite reports whether written is a profile required_file
// that polecat order builds before activePath (e.g. store/tasks while the cmd/main bead is active).
// Returns false when writtenPath's implement bead(s) are closed — fixes must use that bead reopened.
func AllowedEarlierImplementDependencyWrite(townRoot, rig, activePath, writtenPath string, v WorkflowValidation) bool {
	required := v.RequiredFiles
	// During phased delivery, also accept files from earlier phases as valid dependencies.
	if v.HasPhasedDelivery() {
		required = v.UnionRequiredFiles()
	}
	if len(required) == 0 || !pathMatchesRequired(writtenPath, required) {
		return false
	}
	activePath = filepath.ToSlash(strings.TrimSpace(activePath))
	writtenPath = filepath.ToSlash(strings.TrimSpace(writtenPath))
	if activePath == "" || writtenPath == "" || activePath == writtenPath {
		return false
	}
	if implementationPathScore(writtenPath) >= implementationPathScore(activePath) {
		return false
	}
	// Prevent rewriting files from completed phases (earlier than active phase),
	// unless we're in the final phase where integration fixes are expected.
	if v.HasPhasedDelivery() && v.FileInCompletedPhase(writtenPath) && !v.IsFinalDeliveryPhase() {
		return false
	}
	closedOnly, err := ImplementPathHasOnlyClosedBeads(townRoot, rig, writtenPath, v)
	if err == nil && closedOnly {
		return false
	}
	return true
}

// OrderRequiredFilesForImplementation returns required_files in polecat build order.
func OrderRequiredFilesForImplementation(files []string) []string {
	type item struct {
		path  string
		score int
	}
	items := make([]item, 0, len(files))
	for _, f := range files {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" {
			continue
		}
		items = append(items, item{path: f, score: implementationPathScore(f)})
	}
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].score < items[i].score || (items[j].score == items[i].score && items[j].path < items[i].path) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
	out := make([]string, len(items))
	for i := range items {
		out[i] = items[i].path
	}
	return out
}

// EarlierRequiredFilesForBead returns profile paths built before activePath (store before cmd/main, etc.).
func EarlierRequiredFilesForBead(activePath string, required []string) []string {
	activePath = filepath.ToSlash(strings.TrimSpace(activePath))
	if activePath == "" || len(required) == 0 {
		return nil
	}
	activeScore := implementationPathScore(activePath)
	var out []string
	for _, p := range OrderRequiredFilesForImplementation(required) {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" || p == activePath {
			continue
		}
		if implementationPathScore(p) < activeScore {
			out = append(out, p)
		}
	}
	return out
}

func implementationPathScore(p string) int {
	lower := strings.ToLower(p)
	switch {
	case strings.HasSuffix(lower, "go.mod"):
		return 0
	case strings.HasSuffix(lower, "requirements.txt") || strings.HasSuffix(lower, "pyproject.toml"):
		return 5
	case strings.Contains(lower, "/internal/store/schema.go") || strings.Contains(lower, "/internal/store/migrate.go"):
		return 8
	case strings.Contains(lower, "/internal/store/"):
		return 10
	case strings.Contains(lower, "/web/"):
		if strings.HasSuffix(lower, "index.html") {
			return 18
		}
		if strings.HasSuffix(lower, ".css") {
			return 15
		}
		if strings.HasSuffix(lower, ".js") {
			return 16
		}
		return 17
	case strings.Contains(lower, "/internal/api/"):
		return 20
	case strings.HasSuffix(lower, "_test.go"):
		return 85
	case strings.Contains(lower, "/cmd/"):
		return 90
	case strings.HasSuffix(lower, "dockerfile") || strings.HasSuffix(lower, "containerfile"):
		return 96
	case strings.Contains(lower, "docker-compose"):
		return 97
	case strings.HasSuffix(lower, ".dockerignore"):
		return 95
	default:
		return 50
	}
}

// ListOpenImplementBeads returns open implementation beads for the rig.
func ListOpenImplementBeads(townRoot, rig string, v WorkflowValidation) ([]PlanBead, error) {
	return listImplementBeadsForGuard(townRoot, rig, v, "open")
}

// ListImplementBeadsOpenOrInProgress returns open and in_progress implement beads.
func ListImplementBeadsOpenOrInProgress(townRoot, rig string, v WorkflowValidation) ([]PlanBead, error) {
	inProg, err := listImplementBeadsForGuard(townRoot, rig, v, "in_progress")
	if err != nil {
		return nil, err
	}
	open, err := listImplementBeadsForGuard(townRoot, rig, v, "open")
	if err != nil {
		return nil, err
	}
	return append(inProg, open...), nil
}

// EnforceSingleImplementInProgress leaves at most one implement bead in_progress (the queue head).
func EnforceSingleImplementInProgress(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	inProg, err := listImplementBeadsForGuard(townRoot, rig, v, "in_progress")
	if err != nil {
		return nil, err
	}
	if len(inProg) == 0 {
		return nil, nil
	}
	next, err := NextOpenImplementBead(townRoot, rig, v)
	if err != nil {
		return nil, err
	}
	keep := ""
	if next != nil {
		keep = next.ID
	}
	if keep == "" && len(inProg) > 0 {
		keep = inProg[0].ID
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	workDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var reopened []string
	for _, b := range inProg {
		if b.ID == keep {
			continue
		}
		cmd := exec.Command("bd", "update", b.ID, "--status=open")
		cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return reopened, fmt.Errorf("bd update %s --status=open: %w: %s", b.ID, err, strings.TrimSpace(string(out)))
		}
		reopened = append(reopened, b.ID)
	}
	return reopened, nil
}

// PromoteImplementQueueHead enforces a single in_progress implement bead and promotes the
// profile-order queue head from open→in_progress when needed.
func PromoteImplementQueueHead(townRoot, rig string, v WorkflowValidation) (promoted string, reopened []string, err error) {
	reopened, err = EnforceSingleImplementInProgress(townRoot, rig, v)
	if err != nil {
		return "", reopened, err
	}
	next, err := NextOpenImplementBead(townRoot, rig, v)
	if err != nil || next == nil || strings.TrimSpace(next.ID) == "" {
		return "", reopened, nil
	}
	inProg, err := listImplementBeadsForGuard(townRoot, rig, v, "in_progress")
	if err != nil {
		return "", reopened, err
	}
	for _, b := range inProg {
		if b.ID == next.ID {
			return "", reopened, nil
		}
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	workDir := filepath.Join(townRoot, rig, "mayor", "rig")
	cmd := exec.Command("bd", "update", next.ID, "--status=in_progress")
	cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	cmd.Dir = workDir
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		return "", reopened, fmt.Errorf("bd update %s --status=in_progress: %w: %s", next.ID, runErr, strings.TrimSpace(string(out)))
	}
	return next.ID, reopened, nil
}

// NextOpenImplementBead returns the next bead to implement following profile order (open or in_progress).
func NextOpenImplementBead(townRoot, rig string, v WorkflowValidation) (*PlanBead, error) {
	if len(v.RequiredFiles) == 0 {
		return nil, nil
	}
	active, err := ListImplementBeadsOpenOrInProgress(townRoot, rig, v)
	if err != nil {
		return nil, err
	}
	idByPath := map[string]string{}
	titleByID := map[string]string{}
	for _, b := range active {
		p := ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains)
		if p == "" || !IsValidImplementBeadPath(p) {
			continue
		}
		if _, ok := idByPath[p]; !ok {
			idByPath[p] = b.ID
			titleByID[b.ID] = b.Title
		}
		for _, want := range v.RequiredFiles {
			if pathMatchesRequiredForProfile(p, []string{want}, v) {
				if _, ok := idByPath[want]; !ok {
					idByPath[want] = b.ID
					titleByID[b.ID] = b.Title
				}
			}
		}
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	eval := newImplementBeadVerifyEvaluator(rigDir, v)
	order := OrderRequiredFilesForImplementation(v.RequiredFiles)
	for _, want := range order {
		if IsProjectSetupArtifactPath(want, v) {
			continue
		}
		for p, id := range idByPath {
			if !pathMatchesRequiredForProfile(p, []string{want}, v) {
				continue
			}
			if eval.VerifySatisfied(want) {
				continue
			}
			title := titleByID[id]
			if title == "" {
				title = want
			}
			return &PlanBead{ID: id, Title: title}, nil
		}
	}
	return nil, nil
}

// BeadsDatabaseReady reports whether the rig has an on-disk beads store that bd can list.
// Unit tests and fresh rig dirs often have no .beads yet; repair/reset hooks should no-op then.
// Requires the resolved BEADS_DIR to exist (bd may otherwise walk up and hit an unrelated town DB).
func BeadsDatabaseReady(townRoot, rig string) bool {
	if _, err := exec.LookPath("bd"); err != nil {
		return false
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	if beadsDir == "" {
		return false
	}
	if _, err := os.Stat(beadsDir); err != nil {
		return false
	}
	args := beads.InjectFlatForListJSON([]string{"list", "--status=open", "--json", "--limit=0"})
	cmd := exec.Command("bd", args...)
	cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	_, err := cmd.CombinedOutput()
	return err == nil
}

// ResetPlanningPhase deletes all open implement beads for the active phase, removes plan.md,
// and recreates canonical implement beads. Used on planning timeout / stuck recovery.
func ResetPlanningPhase(townRoot, rig string, v WorkflowValidation) (string, error) {
	if !BeadsDatabaseReady(townRoot, rig) {
		return "", nil
	}
	v = v.ForActivePhase()
	var parts []string
	open, err := listAllOpenBeads(townRoot, rig)
	if err != nil {
		return "", err
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	workDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var deleted []string
	for _, b := range open {
		lower := strings.ToLower(strings.TrimSpace(b.Title))
		// Broad match: glued titles (ImplementDockerfile) omit the Implement<space> prefix.
		if !strings.Contains(lower, "implement") || !strings.Contains(lower, "per arch") {
			continue
		}
		if ok, err := deleteImplementBead(beadsDir, workDir, b.ID); err != nil {
			return joinStrings(parts, "; "), err
		} else if ok {
			deleted = append(deleted, b.ID)
		}
	}
	if len(deleted) > 0 {
		parts = append(parts, "deleted implement beads: "+joinStrings(deleted, ", "))
	}
	if removed, err := RemoveStalePlanMD(townRoot, rig, v); err != nil {
		return joinStrings(parts, "; "), err
	} else if removed {
		parts = append(parts, "removed plan.md")
	}
	created, err := EnsurePlanningImplementBeads(townRoot, rig, v)
	if err != nil {
		return joinStrings(parts, "; "), err
	}
	if len(created) > 0 {
		parts = append(parts, "recreated: "+joinStrings(created, ", "))
	}
	if _, err := WritePlanningPlanMD(townRoot, rig, v); err != nil {
		return joinStrings(parts, "; "), err
	}
	parts = append(parts, "wrote plan.md")
	return joinStrings(parts, "; "), nil
}

// RepairPlanningBeadSet dedupes and prunes open implement beads to match the active planning phase.
func RepairPlanningBeadSet(townRoot, rig string, v WorkflowValidation) (string, error) {
	if !BeadsDatabaseReady(townRoot, rig) {
		return "", nil
	}
	v = v.ForActivePhase()
	var parts []string
	malformed, err := PruneMalformedImplementBeads(townRoot, rig, v)
	if err != nil {
		return "", err
	}
	if len(malformed) > 0 {
		parts = append(parts, "pruned malformed: "+joinStrings(malformed, ", "))
	}
	legacy, err := PruneLegacyImplementBeadTitles(townRoot, rig, v)
	if err != nil {
		return "", err
	}
	if len(legacy) > 0 {
		parts = append(parts, "pruned legacy titles: "+joinStrings(legacy, ", "))
	}
	flat, err := PruneNonRequiredOpenImplementBeads(townRoot, rig, v)
	if err != nil {
		return "", err
	}
	if len(flat) > 0 {
		parts = append(parts, "pruned non-required paths: "+joinStrings(flat, ", "))
	}
	if removed, err := RemoveStalePlanMD(townRoot, rig, v); err != nil {
		return "", err
	} else if removed {
		parts = append(parts, "removed stale plan.md")
	}
	dupes, err := PruneDuplicateImplementBeads(townRoot, rig, v)
	if err != nil {
		return "", err
	}
	if len(dupes) > 0 {
		parts = append(parts, "deduped: "+joinStrings(dupes, ", "))
	}
	if pruned, err := PruneOpenImplementBeadsForClosedPaths(townRoot, rig, v); err != nil {
		return "", err
	} else if len(pruned) > 0 {
		parts = append(parts, "pruned open dupes of closed paths: "+joinStrings(pruned, ", "))
	}
	if reopened, err := ReopenClosedImplementBeadsForMissingOpenRequired(townRoot, rig, v); err != nil {
		return "", err
	} else if len(reopened) > 0 {
		parts = append(parts, "reopened closed for missing open required: "+joinStrings(reopened, ", "))
	}
	deleted, err := PruneExtraImplementBeads(townRoot, rig, v)
	if err != nil {
		return "", err
	}
	if len(deleted) > 0 {
		parts = append(parts, "pruned extras: "+joinStrings(deleted, ", "))
	}
	created, err := EnsurePlanningImplementBeads(townRoot, rig, v)
	if err != nil {
		return "", err
	}
	if len(created) > 0 {
		parts = append(parts, "created: "+joinStrings(created, ", "))
	}
	return joinStrings(parts, "; "), nil
}

// PruneOpenImplementBeadsForClosedPaths deletes open/in_progress implement beads when another
// closed bead already covers the same file path (prevents reset_implementation_phase from
// deleting on-disk files for work that was already closed on a duplicate bead ID).
func PruneOpenImplementBeadsForClosedPaths(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	v = v.ForActivePhase()
	closed, err := listImplementBeadsByStatus(townRoot, rig, v, "closed")
	if err != nil {
		return nil, err
	}
	closedPaths := map[string]bool{}
	for _, b := range closed {
		if !MatchesImplementBeadTitle(b.Title, v) {
			continue
		}
		p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot, rig)
		if p != "" {
			closedPaths[p] = true
		}
	}
	if len(closedPaths) == 0 {
		return nil, nil
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	workDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var deleted []string
	for _, status := range []string{"open", "in_progress"} {
		beads, err := listImplementBeadsByStatus(townRoot, rig, v, status)
		if err != nil {
			return deleted, err
		}
		for _, b := range beads {
			if !MatchesImplementBeadTitle(b.Title, v) {
				continue
			}
			p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot, rig)
			if p == "" || !closedPaths[p] {
				continue
			}
			if ok, err := deleteImplementBead(beadsDir, workDir, b.ID); err != nil {
				return deleted, err
			} else if ok {
				deleted = append(deleted, b.ID+" ("+p+")")
			}
		}
	}
	return deleted, nil
}

// PruneDuplicateImplementBeads deletes duplicate open/in_progress beads for the same required_files path,
// keeping the in_progress queue head or canonical planner title when present.
func PruneDuplicateImplementBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	return pruneDuplicateActiveImplementBeads(townRoot, rig, v)
}

// PruneDuplicateClosedImplementBeads deletes duplicate closed beads for the same path
// (e.g. two closed go.mod beads). Keeps one closed bead per path for audit; removes extras.
func PruneDuplicateClosedImplementBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	return pruneDuplicateImplementBeadsByStatus(townRoot, rig, v, "closed")
}

type activeImplementBead struct {
	PlanBead
	status string
}

func pruneDuplicateActiveImplementBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	v = v.ForActivePhase()
	if len(v.RequiredFiles) == 0 {
		return nil, nil
	}
	var beads []activeImplementBead
	for _, status := range []string{"open", "in_progress"} {
		list, err := listImplementBeadsByStatus(townRoot, rig, v, status)
		if err != nil {
			return nil, err
		}
		for _, b := range list {
			beads = append(beads, activeImplementBead{PlanBead: b, status: status})
		}
	}
	pathToIDs := map[string][]string{}
	for _, b := range beads {
		p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot, rig)
		if p == "" || !IsValidImplementBeadPath(p) {
			continue
		}
		pathToIDs[p] = append(pathToIDs[p], b.ID)
	}
	toDelete := map[string]bool{}
	for _, want := range v.RequiredFiles {
		ids := dedupeStrings(beadIDsForPathProfile(pathToIDs, want, v))
		if len(ids) <= 1 {
			continue
		}
		keeper := selectKeeperActiveImplementBead(beads, want, ids, v)
		for _, id := range ids {
			if id != keeper {
				toDelete[id] = true
			}
		}
	}
	for p, ids := range pathToIDs {
		if len(ids) <= 1 {
			continue
		}
		if pathMatchesRequiredForProfile(p, v.RequiredFiles, v) {
			continue
		}
		keeper := selectKeeperActiveImplementBead(beads, p, dedupeStrings(ids), v)
		for _, id := range ids {
			if id != keeper {
				toDelete[id] = true
			}
		}
	}
	if len(toDelete) == 0 {
		return nil, nil
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	workDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var deleted []string
	for id := range toDelete {
		if ok, err := deleteImplementBead(beadsDir, workDir, id); err != nil {
			return deleted, err
		} else if ok {
			deleted = append(deleted, id)
		}
	}
	return deleted, nil
}

func selectKeeperActiveImplementBead(beads []activeImplementBead, want string, ids []string, v WorkflowValidation) string {
	idSet := map[string]bool{}
	for _, id := range ids {
		idSet[id] = true
	}
	var inProgress []string
	for _, b := range beads {
		if !idSet[b.ID] {
			continue
		}
		if b.status == "in_progress" {
			inProgress = append(inProgress, b.ID)
		}
	}
	if len(inProgress) == 1 {
		return inProgress[0]
	}
	planOnly := make([]PlanBead, 0, len(ids))
	for _, b := range beads {
		if idSet[b.ID] {
			planOnly = append(planOnly, b.PlanBead)
		}
	}
	return selectKeeperImplementBead(planOnly, want, ids, v)
}

func pruneDuplicateImplementBeadsByStatus(townRoot, rig string, v WorkflowValidation, status string) ([]string, error) {
	v = v.ForActivePhase()
	if len(v.RequiredFiles) == 0 {
		return nil, nil
	}
	beads, err := listImplementBeadsByStatus(townRoot, rig, v, status)
	if err != nil {
		return nil, err
	}
	pathToIDs := map[string][]string{}
	for _, b := range beads {
		p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot, rig)
		if p == "" || !IsValidImplementBeadPath(p) {
			continue
		}
		pathToIDs[p] = append(pathToIDs[p], b.ID)
	}
	toDelete := map[string]bool{}
	for _, want := range v.RequiredFiles {
		ids := dedupeStrings(beadIDsForPathProfile(pathToIDs, want, v))
		if len(ids) <= 1 {
			continue
		}
		keeper := selectKeeperImplementBead(beads, want, ids, v)
		for _, id := range ids {
			if id != keeper {
				toDelete[id] = true
			}
		}
	}
	for p, ids := range pathToIDs {
		if len(ids) <= 1 {
			continue
		}
		if pathMatchesRequiredForProfile(p, v.RequiredFiles, v) {
			continue // already handled via required_files loop
		}
		keeper := selectKeeperImplementBead(beads, p, dedupeStrings(ids), v)
		for _, id := range ids {
			if id != keeper {
				toDelete[id] = true
			}
		}
	}
	if len(toDelete) == 0 {
		return nil, nil
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	workDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var deleted []string
	for id := range toDelete {
		if ok, err := deleteImplementBead(beadsDir, workDir, id); err != nil {
			return deleted, err
		} else if ok {
			deleted = append(deleted, id)
		}
	}
	return deleted, nil
}

func selectKeeperImplementBead(beads []PlanBead, want string, ids []string, v WorkflowValidation) string {
	canonical := PlanningBeadTitle(want, v)
	idSet := map[string]bool{}
	for _, id := range ids {
		idSet[id] = true
	}
	for _, b := range beads {
		if !idSet[b.ID] {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(b.Title), canonical) {
			return b.ID
		}
	}
	var perArch []string
	for _, b := range beads {
		if !idSet[b.ID] {
			continue
		}
		if strings.Contains(b.Title, " per architecture") {
			perArch = append(perArch, b.ID)
		}
	}
	if len(perArch) == 1 {
		return perArch[0]
	}
	if len(perArch) > 1 {
		return lexicographicMinID(perArch)
	}
	return lexicographicMinID(ids)
}

func lexicographicMinID(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	min := ids[0]
	for _, id := range ids[1:] {
		if id < min {
			min = id
		}
	}
	return min
}

// PruneOpenImplementBeadsOutsideRequired deletes open implement beads whose path is not an exact
// active-phase required_files entry (used after delivery phase advance; avoids basename false positives).
func PruneOpenImplementBeadsOutsideRequired(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	v = v.ForActivePhase()
	if len(v.RequiredFiles) == 0 {
		return nil, nil
	}
	required := make(map[string]bool)
	for _, want := range v.RequiredFiles {
		want = filepath.ToSlash(strings.TrimSpace(want))
		if want != "" {
			required[want] = true
		}
	}
	open, err := listAllOpenBeads(townRoot, rig)
	if err != nil {
		return nil, err
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	workDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var deleted []string
	for _, b := range open {
		if !MatchesImplementBeadTitle(b.Title, v) {
			continue
		}
		p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot, rig)
		if p == "" || required[p] {
			continue
		}
		if ok, err := deleteImplementBead(beadsDir, workDir, b.ID); err != nil {
			return deleted, err
		} else if ok {
			deleted = append(deleted, b.ID)
		}
	}
	return deleted, nil
}

// PruneNonRequiredOpenImplementBeads deletes open implement-like beads whose path is not an exact
// required_files entry (e.g. linkshelf/handlers.go when the profile lists linkshelf/internal/api/handlers.go).
func PruneNonRequiredOpenImplementBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	v = v.ForActivePhase()
	if len(v.RequiredFiles) == 0 {
		return nil, nil
	}
	beads, err := listImplementBeadsForPrune(townRoot, rig, v)
	if err != nil {
		return nil, err
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	workDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var deleted []string
	augmented := requiredFilesWithCorrelatedTests(v.RequiredFiles, v)
	for _, b := range beads {
		if !looksLikeOpenImplementBeadTitle(b.Title, v) {
			continue
		}
		p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot, rig)
		if !IsValidImplementBeadPath(p) || pathMatchesRequiredForProfile(p, augmented, v) {
			continue
		}
		// Try with normalized augmented entries (rig prefix stripped) for exact mode
		matched := false
		for _, a := range augmented {
			if n := NormalizePlannerBeadPath(a, v.LayoutRoot, rig); n != "" && pathMatchesRequiredForProfile(p, []string{n}, v) {
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if ok, err := deleteImplementBead(beadsDir, workDir, b.ID); err != nil {
			return deleted, err
		} else if ok {
			deleted = append(deleted, b.ID)
		}
	}
	return deleted, nil
}

func listImplementBeadsForPrune(townRoot, rig string, v WorkflowValidation) ([]PlanBead, error) {
	if RequiresExactImplementPaths(v) {
		// Flat invalid titles (linkshelf/handlers.go) fail MatchesImplementBeadTitle but must
		// still be pruned when they are not exact required_files entries.
		return listImplementLikeBeadsOpenOrInProgress(townRoot, rig, v)
	}
	return listAllOpenBeads(townRoot, rig)
}

// listImplementLikeBeadsOpenOrInProgress lists open and in_progress beads whose titles look
// implement-like (Implement … / bead_title_contains), without requiring MatchesImplementBeadTitle.
func listImplementLikeBeadsOpenOrInProgress(townRoot, rig string, v WorkflowValidation) ([]PlanBead, error) {
	var out []PlanBead
	for _, status := range []string{"open", "in_progress"} {
		beads, err := listAllBeadsByStatus(townRoot, rig, status)
		if err != nil {
			return nil, err
		}
		for _, b := range beads {
			if looksLikeOpenImplementBeadTitle(b.Title, v) {
				out = append(out, b)
			}
		}
	}
	return out, nil
}

func listAllBeadsByStatus(townRoot, rig, status string) ([]PlanBead, error) {
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	args := beads.InjectFlatForListJSON([]string{"list", "--status=" + status, "--json", "--limit=0"})
	cmd := exec.Command("bd", args...)
	cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("bd list %s: %w: %s", status, err, strings.TrimSpace(string(out)))
	}
	out = beads.StripStdoutWarnings(out)
	var rows []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, err
	}
	var result []PlanBead
	for _, r := range rows {
		id := strings.TrimSpace(beads.ExtractIssueID(r.ID))
		if id == "" {
			continue
		}
		result = append(result, PlanBead{ID: id, Title: strings.TrimSpace(r.Title)})
	}
	return result, nil
}

func listAllOpenBeads(townRoot, rig string) ([]PlanBead, error) {
	return listAllBeadsByStatus(townRoot, rig, "open")
}

func looksLikeOpenImplementBeadTitle(title string, v WorkflowValidation) bool {
	if MatchesImplementBeadTitle(title, v) {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(title))
	if strings.HasPrefix(lower, "implement ") {
		return true
	}
	pfx := strings.TrimSpace(v.BeadTitleContains)
	return pfx != "" && strings.HasPrefix(lower, strings.ToLower(pfx))
}

// PruneLegacyImplementBeadTitles deletes open implement-like beads that do not use the profile's
// canonical title prefix (e.g. legacy "Implement linkshelf/main.go" when the profile expects
// "Link Shelf /linkshelf/...").
func PruneLegacyImplementBeadTitles(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	v = v.ForActivePhase()
	pfx := strings.TrimSpace(v.BeadTitleContains)
	if pfx == "" {
		return nil, nil
	}
	open, err := listAllOpenBeads(townRoot, rig)
	if err != nil {
		return nil, err
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	workDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var deleted []string
	for _, b := range open {
		if !isNonCanonicalImplementBeadTitle(b.Title, v) {
			continue
		}
		if ok, err := deleteImplementBead(beadsDir, workDir, b.ID); err != nil {
			return deleted, err
		} else if ok {
			deleted = append(deleted, b.ID)
		}
	}
	return deleted, nil
}

// isNonCanonicalImplementBeadTitle reports legacy planner titles (e.g. "Implement linkshelf/main.go")
// when the profile expects a different prefix (e.g. "Link Shelf /linkshelf/...").
func isNonCanonicalImplementBeadTitle(title string, v WorkflowValidation) bool {
	pfx := strings.TrimSpace(v.BeadTitleContains)
	if pfx == "" {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(title))
	lowerPfx := strings.ToLower(pfx)
	if strings.HasPrefix(lower, lowerPfx) {
		return false
	}
	if strings.HasPrefix(lower, "implement ") {
		return true
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout != "" && strings.Contains(lower, "implement") && strings.Contains(lower, layout) {
		return true
	}
	return false
}

// PruneExtraImplementBeads deletes open implement beads whose paths are invalid or not in required_files.
func PruneExtraImplementBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	v = v.ForActivePhase()
	if len(v.RequiredFiles) == 0 {
		return nil, nil
	}
	open, err := ListOpenImplementBeads(townRoot, rig, v)
	if err != nil {
		return nil, err
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	workDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var deleted []string
	augmented := requiredFilesWithCorrelatedTests(v.RequiredFiles, v)
	for _, b := range open {
		p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot, rig)
		if IsValidImplementBeadPath(p) && pathMatchesRequiredForProfile(p, augmented, v) {
			continue
		}
		// Try with normalized augmented entries for exact mode
		matched := false
		for _, a := range augmented {
			if n := NormalizePlannerBeadPath(a, v.LayoutRoot, rig); n != "" && pathMatchesRequiredForProfile(p, []string{n}, v) {
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if ok, err := deleteImplementBead(beadsDir, workDir, b.ID); err != nil {
			return deleted, err
		} else if ok {
			deleted = append(deleted, b.ID)
		}
	}
	return deleted, nil
}

// PruneMalformedImplementBeads deletes open implement-like beads with glued titles or invalid paths.
func PruneMalformedImplementBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	v = v.ForActivePhase()
	open, err := listAllOpenBeads(townRoot, rig)
	if err != nil {
		return nil, err
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	workDir := filepath.Join(townRoot, rig, "mayor", "rig")
	pfx := strings.ToLower(strings.TrimSpace(v.BeadTitleContains))
	var deleted []string
	for _, b := range open {
		if !MatchesImplementBeadTitle(b.Title, v) {
			continue
		}
		lowerTitle := strings.ToLower(strings.TrimSpace(b.Title))
		canonical := pfx != "" && strings.HasPrefix(lowerTitle, pfx)
		p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot, rig)
		// Only remove glued/invalid titles here; PruneExtraImplementBeads handles wrong-phase paths.
		if canonical && IsValidImplementBeadPath(p) {
			continue
		}
		if ok, err := deleteImplementBead(beadsDir, workDir, b.ID); err != nil {
			return deleted, err
		} else if ok {
			deleted = append(deleted, b.ID)
		}
	}
	return deleted, nil
}

var planPlaceholderBeadIDRE = regexp.MustCompile(`(?m)^###\s+(?:fi|te|hq)-00[0-9]:`)

// RemoveStalePlanMD deletes plan.md when it is too small or cites placeholder bead IDs.
func RemoveStalePlanMD(townRoot, rig string, v WorkflowValidation) (bool, error) {
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	path := filepath.Join(rigDir, "plan.md")
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	remove := info.Size() < EffectiveMinPlanBytes(rigDir, v)
	if !remove {
		data, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		remove = planPlaceholderBeadIDRE.Match(data)
	}
	if !remove {
		return false, nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	return true, nil
}

// PlanningBeadTitle returns the canonical open-task title for a required_files path.
func PlanningBeadTitle(requiredPath string, v WorkflowValidation) string {
	path := filepath.ToSlash(strings.TrimSpace(requiredPath))
	pfx := v.BeadTitleContains
	if strings.TrimSpace(pfx) == "" {
		pfx = "Implement "
	}
	layout := strings.Trim(strings.TrimPrefix(strings.ToLower(pfx), "implement "), "/")
	if layout != "" && strings.HasPrefix(path, layout+"/") {
		path = strings.TrimPrefix(path, layout+"/")
	}
	return strings.TrimSpace(pfx + path) + " per architecture"
}

// openBeadCoversRequiredPath reports whether an open or in_progress implement bead covers want.
func openBeadCoversRequiredPath(townRoot, rig, want string, v WorkflowValidation) (bool, error) {
	active, err := ListImplementBeadsOpenOrInProgress(townRoot, rig, v)
	if err != nil {
		return false, err
	}
	want = NormalizePlannerBeadPath(want, v.LayoutRoot, rig)
	for _, b := range active {
		if !looksLikeOpenImplementBeadTitle(b.Title, v) {
			continue
		}
		p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot, rig)
		if pathMatchesRequiredForProfile(p, []string{want}, v) {
			return true, nil
		}
	}
	return false, nil
}

// ReopenClosedImplementBeadsForMissingOpenRequired reopens closed implement beads when a
// required_files path has no open/in_progress bead (e.g. go.mod closed after project_setup).
func ReopenClosedImplementBeadsForMissingOpenRequired(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	v = v.ForActivePhase()
	if len(v.RequiredFiles) == 0 {
		return nil, nil
	}
	var reopened []string
	for _, want := range v.RequiredFiles {
		want = filepath.ToSlash(strings.TrimSpace(want))
		if want == "" {
			continue
		}
		// Skip project_setup artifacts (go.mod, go.sum, requirements.txt, pyproject.toml)
		// These are handled by the setup phase, not polecat implementation.
		if IsProjectSetupArtifactPath(want, v) {
			continue
		}
		covered, err := openBeadCoversRequiredPath(townRoot, rig, want, v)
		if err != nil {
			return reopened, err
		}
		if covered {
			continue
		}
		id, ok := ClosedImplementBeadForPath(townRoot, rig, want, v)
		if !ok {
			continue
		}
		if err := bdUpdateImplementBeadStatus(townRoot, rig, id, "open"); err != nil {
			return reopened, err
		}
		reopened = append(reopened, id+" ("+want+")")
	}
	return reopened, nil
}

// ImplementBeadIsStillOpen reports whether beadID is open or in_progress in the beads store.
func ImplementBeadIsStillOpen(townRoot, rig, beadID string, v WorkflowValidation) bool {
	beadID = strings.TrimSpace(beadID)
	if beadID == "" {
		return false
	}
	for _, status := range []string{"open", "in_progress"} {
		beads, err := listImplementBeadsByStatus(townRoot, rig, v, status)
		if err != nil {
			continue
		}
		for _, b := range beads {
			if b.ID == beadID {
				return true
			}
		}
	}
	return false
}

// EnsurePlanningImplementBeads creates open implement beads for any missing required_files paths.
func EnsurePlanningImplementBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	v = v.ForActivePhase()
	if len(v.RequiredFiles) == 0 {
		return nil, nil
	}
	open, err := ListOpenImplementBeads(townRoot, rig, v)
	if err != nil {
		return nil, err
	}
	pathToID := map[string]string{}
	for _, b := range open {
		p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot, rig)
		if p == "" || !IsValidImplementBeadPath(p) {
			continue
		}
		if _, ok := pathToID[p]; !ok {
			pathToID[p] = b.ID
		}
		for _, want := range v.RequiredFiles {
			if pathMatchesRequiredForProfile(p, []string{want}, v) {
				if _, ok := pathToID[want]; !ok {
					pathToID[want] = b.ID
				}
			}
		}
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	workDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var created []string

	// Include correlated test paths so each source file gets a paired test bead
	// (e.g. main.py → test_main.py). Without this, the phase verify runs pytest
	// against test files that have no bead, deadlocking bead auto-close.
	wants := v.RequiredFiles
	if !WorkflowUsesDocker(v) {
		wants = requiredFilesWithCorrelatedTests(v.RequiredFiles, v)
	}
	for _, want := range wants {
		want = filepath.ToSlash(strings.TrimSpace(want))
		if want == "" {
			continue
		}
		covered, err := openBeadCoversRequiredPath(townRoot, rig, want, v)
		if err != nil {
			return created, err
		}
		if covered {
			continue
		}
		if id, ok := ClosedImplementBeadForPath(townRoot, rig, want, v); ok {
			rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
			if newImplementBeadVerifyEvaluator(rigDir, v).VerifySatisfied(want) {
				continue
			}
			if err := bdUpdateImplementBeadStatus(townRoot, rig, id, "open"); err != nil {
				return created, err
			}
			created = append(created, id)
			pathToID[want] = id
			continue
		}
		title := PlanningBeadTitle(want, v)
		desc := fmt.Sprintf("Implement %s: see architecture.md", want)
		cmd := exec.Command("bd", "create", "--type", "task", "--title", title, "--description", desc)
		cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return created, fmt.Errorf("bd create %q: %w: %s", title, err, strings.TrimSpace(string(out)))
		}
		id := parseBeadIDFromCreateOutput(string(out))
		if id != "" {
			created = append(created, id)
			pathToID[want] = id
		}
	}
	return created, nil
}

// requiredFilesWithCorrelatedTests returns RequiredFiles augmented with the
// correlated test path (test_*.[py|go]) for each source file, when that test
// path is not already in the list. This ensures the planner creates a bead for
// every test file the phase verify expects to exist.
func requiredFilesWithCorrelatedTests(files []string, v WorkflowValidation) []string {
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		seen[filepath.ToSlash(strings.TrimSpace(f))] = true
	}
	out := make([]string, 0, len(files)+len(files)/2)
	for _, f := range files {
		f = filepath.ToSlash(strings.TrimSpace(f))
		out = append(out, f)
		if IsTestImplementPath(f) {
			continue
		}
		testPath := CorrelatedTestPathForSource(f, v)
		if testPath == "" || seen[testPath] {
			continue
		}
		out = append(out, testPath)
		seen[testPath] = true
	}
	return out
}

func parseBeadIDFromCreateOutput(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "Created issue:") {
			continue
		}
		rest := line[strings.Index(line, "Created issue:")+len("Created issue:"):]
		rest = strings.TrimSpace(strings.TrimPrefix(rest, "✓"))
		if before, _, ok := strings.Cut(rest, "—"); ok {
			return strings.TrimSpace(before)
		}
		if before, _, ok := strings.Cut(rest, " - "); ok {
			return strings.TrimSpace(before)
		}
		if f := strings.Fields(rest); len(f) > 0 {
			return strings.Trim(f[0], "[](),")
		}
	}
	return ""
}

// FormatPlanningBeadBootstrapBlock lists exact bd create lines for the planner.
func FormatPlanningBeadBootstrapBlock(townRoot, rig string, v WorkflowValidation) string {
	v = v.ForActivePhase()
	if len(v.RequiredFiles) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Planning: implementation beads required\n\n")
	if townRoot != "" && rig != "" {
		if open, err := ListOpenImplementBeads(townRoot, rig, v); err == nil && len(open) > 0 {
			b.WriteString("**Open implement beads — copy these IDs into plan.md (never fi-001 placeholders):**\n\n")
			for _, bead := range open {
				p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(bead.Title, v.BeadTitleContains), v.LayoutRoot, rig)
				b.WriteString(fmt.Sprintf("- `%s` → `%s`\n", bead.ID, p))
			}
			b.WriteString("\n")
		}
	}
	if v.HasPhasedDelivery() {
		id := v.ActivePhaseID()
		if id == "" {
			if p, ok := v.ActivePhase(); ok {
				id = strings.TrimSpace(p.ID)
			}
		}
		b.WriteString(fmt.Sprintf("**Active phase `%s` only** — do not `bd create` for paths that appear in architecture.md but are not listed below.\n\n", id))
	}
	b.WriteString("If open implement beads already cover every path below, **do not `bd create`** — run `bd list --status=open`, write or expand `plan.md` only, then JSON success. ")
	b.WriteString("Otherwise run **CMD:** `bd create` lines in this session before JSON success. ")
	b.WriteString("Do not claim beads exist without command output showing new bead IDs. ")
	b.WriteString("Titles must include a **space** after `Implement` (e.g. `Implement Dockerfile per architecture`, never `ImplementDockerfile`).\n\n")
	b.WriteString("One `bd create` per path below (do not add extras):\n\n")
	for _, p := range v.RequiredFiles {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		title := PlanningBeadTitle(p, v)
		b.WriteString(fmt.Sprintf("CMD: export BEADS_DIR=$GT_ROOT/%s/.beads && cd %s/mayor/rig && bd create --type task --title %q --description=%q\n",
			rig, rig, title, fmt.Sprintf("Implement %s: see architecture.md", p)))
	}
	b.WriteString("\nThen `bd list --status=open`, write plan.md (≥ ")
	b.WriteString(fmt.Sprintf("%d", v.MinPlanBytes))
	b.WriteString(" bytes) with a ## Bead map: one ### <id>: <full-path> section per file (scope, architecture ref, acceptance bullets; include *_test.go / tests/test_*.py paths from architecture). ")
	b.WriteString("A 3-line checklist is too small and will fail wc -c. Use real IDs from bd list only. ")
	b.WriteString("`wc -c plan.md`, then JSON success in a **later** turn.\n")
	return b.String()
}

// ImplementBeadPathForID resolves the file path for an implement bead ID.
func ImplementBeadPathForID(townRoot, rig, beadID string, v WorkflowValidation) string {
	beadID = strings.TrimSpace(beadID)
	if beadID == "" {
		return ""
	}
	for _, status := range []string{"open", "in_progress", "closed"} {
		beads, err := listImplementBeadsForGuard(townRoot, rig, v, status)
		if err != nil {
			continue
		}
		for _, b := range beads {
			if b.ID != beadID {
				continue
			}
			return NormalizeBeadPathForLayout(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot)
		}
	}
	return ""
}

// FormatImplementationQueueBlock returns a "next bead" hint and full queue table for the polecat.
func FormatImplementationQueueBlock(townRoot, rig string, v WorkflowValidation) string {
	if len(v.RequiredFiles) == 0 {
		return ""
	}
	order := OrderRequiredFilesForImplementation(v.RequiredFiles)
	next, err := NextOpenImplementBead(townRoot, rig, v)
	if err != nil {
		return fmt.Sprintf("**Next bead:** (error: %v)", err)
	}

	all, _ := ListImplementBeadsOpenOrInProgress(townRoot, rig, v)
	var b strings.Builder
	if len(all) > 1 {
		b.WriteString("### Queue (all open/in_progress beads)\n\n")
		b.WriteString("| Bead ID | Path |\n")
		b.WriteString("|---------|------|\n")
		for _, bead := range all {
			p := NormalizeBeadPathForLayout(ExtractPathFromBeadTitle(bead.Title, v.BeadTitleContains), v.LayoutRoot)
			if p == "" {
				p = bead.Title
			}
			marker := ""
			if next != nil && bead.ID == next.ID {
				marker = " ← next"
			}
			b.WriteString(fmt.Sprintf("| `%s` | `%s`%s |\n", bead.ID, p, marker))
		}
		b.WriteString("\n")
	}

	if next == nil {
		b.WriteString("**Next bead:** none open — `bd list --status=closed` or JSON success if all implement beads are closed.")
		return b.String()
	}
	step, total := 0, len(order)
	beadPath := NormalizeBeadPathForLayout(ExtractPathFromBeadTitle(next.Title, v.BeadTitleContains), v.LayoutRoot)
	for i, want := range order {
		if pathMatchesRequiredForProfile(beadPath, []string{want}, v) {
			step = i + 1
			break
		}
	}
	mayorDir := filepath.Join(townRoot, rig, "mayor", "rig")
	verify := AgentShellVerifyCommand(rig, v, mayorDir, beadPath)
	if step > 0 {
		b.WriteString(fmt.Sprintf("**Next bead (%d/%d):** %s → `%s` — work only this ID until `bd close`. Verify: `%s`.",
			step, total, next.ID, next.Title, verify))
	} else {
		b.WriteString(fmt.Sprintf("**Next bead:** %s → `%s` — work only this ID until `bd close`. Verify: `%s`.",
			next.ID, next.Title, verify))
	}
	return b.String()
}
