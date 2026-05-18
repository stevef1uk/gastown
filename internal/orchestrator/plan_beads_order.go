package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/steveyegge/gastown/internal/config"
)

// IsValidImplementBeadPath reports whether a path extracted from a bead title is a real repo file path.
func IsValidImplementBeadPath(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" || strings.Contains(path, "..") {
		return false
	}
	if strings.ContainsAny(path, "[]") || strings.Contains(path, " per ") {
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
	if !strings.Contains(path, "/") && !strings.Contains(base, ".") {
		return false
	}
	parts := strings.Split(path, "/")
	for _, seg := range parts {
		if seg == "" || strings.Contains(seg, "[") || strings.Contains(seg, "]") {
			return false
		}
	}
	// Double layout prefix: linkshelf/linkshelf/...
	if len(parts) >= 2 && parts[0] == parts[1] {
		return false
	}
	return true
}

// ValidateImplementBeadCreateTitle ensures bd create titles map to a profile required path.
func ValidateImplementBeadCreateTitle(title string, v WorkflowValidation) error {
	v = v.ForActivePhase()
	path := ExtractPathFromBeadTitle(title, v.BeadTitleContains)
	if !IsValidImplementBeadPath(path) {
		return fmt.Errorf("bead title must be %q<file-path> per architecture (got invalid path %q)", v.BeadTitleContains, path)
	}
	if len(v.RequiredFiles) == 0 {
		return nil
	}
	if !pathMatchesRequired(path, v.RequiredFiles) {
		return fmt.Errorf("bead path %q is not in profile required_files — only create beads for: %s", path, strings.Join(v.RequiredFiles, ", "))
	}
	return nil
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

func implementationPathScore(p string) int {
	lower := strings.ToLower(p)
	switch {
	case strings.HasSuffix(lower, "go.mod"):
		return 0
	case strings.Contains(lower, "/internal/store/"):
		return 10
	case strings.Contains(lower, "/internal/api/"):
		return 20
	case strings.Contains(lower, "/web/"):
		return 30
	case strings.HasSuffix(lower, "_test.go"):
		return 85
	case strings.Contains(lower, "/cmd/"):
		return 90
	default:
		return 50
	}
}

// ListOpenImplementBeads returns open implementation beads for the rig.
func ListOpenImplementBeads(townRoot, rig string, v WorkflowValidation) ([]PlanBead, error) {
	return listImplementBeadsByStatus(townRoot, rig, v, "open")
}

// ListImplementBeadsOpenOrInProgress returns open and in_progress implement beads.
func ListImplementBeadsOpenOrInProgress(townRoot, rig string, v WorkflowValidation) ([]PlanBead, error) {
	inProg, err := listImplementBeadsByStatus(townRoot, rig, v, "in_progress")
	if err != nil {
		return nil, err
	}
	open, err := listImplementBeadsByStatus(townRoot, rig, v, "open")
	if err != nil {
		return nil, err
	}
	return append(inProg, open...), nil
}

// EnforceSingleImplementInProgress leaves at most one implement bead in_progress (the queue head).
func EnforceSingleImplementInProgress(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	inProg, err := listImplementBeadsByStatus(townRoot, rig, v, "in_progress")
	if err != nil {
		return nil, err
	}
	if len(inProg) <= 1 {
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
			if pathMatchesRequired(p, []string{want}) {
				if _, ok := idByPath[want]; !ok {
					idByPath[want] = b.ID
					titleByID[b.ID] = b.Title
				}
			}
		}
	}
	order := OrderRequiredFilesForImplementation(v.RequiredFiles)
	for _, want := range order {
		for p, id := range idByPath {
			if pathMatchesRequired(p, []string{want}) {
				title := titleByID[id]
				if title == "" {
					title = want
				}
				return &PlanBead{ID: id, Title: title}, nil
			}
		}
	}
	return nil, nil
}

// PruneDuplicateImplementBeads deletes duplicate open beads for the same required_files path,
// keeping the canonical planner title when present.
func PruneDuplicateImplementBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	if len(v.RequiredFiles) == 0 {
		return nil, nil
	}
	open, err := ListOpenImplementBeads(townRoot, rig, v)
	if err != nil {
		return nil, err
	}
	pathToIDs := map[string][]string{}
	for _, b := range open {
		p := ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains)
		if p == "" || !IsValidImplementBeadPath(p) {
			continue
		}
		pathToIDs[p] = append(pathToIDs[p], b.ID)
	}
	toDelete := map[string]bool{}
	for _, want := range v.RequiredFiles {
		ids := dedupeStrings(beadIDsForPath(pathToIDs, want))
		if len(ids) <= 1 {
			continue
		}
		keeper := selectKeeperImplementBead(open, want, ids, v)
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
		cmd := exec.Command("bd", "delete", id, "--force")
		cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return deleted, fmt.Errorf("bd delete %s: %w: %s", id, err, strings.TrimSpace(string(out)))
		}
		deleted = append(deleted, id)
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

// PruneExtraImplementBeads deletes open implement beads whose paths are invalid or not in required_files.
func PruneExtraImplementBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
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
	for _, b := range open {
		p := ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains)
		if IsValidImplementBeadPath(p) && pathMatchesRequired(p, v.RequiredFiles) {
			continue
		}
		cmd := exec.Command("bd", "delete", b.ID, "--force")
		cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return deleted, fmt.Errorf("bd delete %s: %w: %s", b.ID, err, strings.TrimSpace(string(out)))
		}
		deleted = append(deleted, b.ID)
	}
	return deleted, nil
}

// PlanningBeadTitle returns the canonical open-task title for a required_files path.
func PlanningBeadTitle(requiredPath string, v WorkflowValidation) string {
	path := filepath.ToSlash(strings.TrimSpace(requiredPath))
	pfx := strings.TrimSpace(v.BeadTitleContains)
	if pfx == "" {
		pfx = "Implement "
	}
	layout := strings.Trim(strings.TrimPrefix(strings.ToLower(pfx), "implement "), "/")
	if layout != "" && strings.HasPrefix(path, layout+"/") {
		path = strings.TrimPrefix(path, layout+"/")
	}
	return strings.TrimSpace(pfx + path) + " per architecture"
}

// EnsurePlanningImplementBeads creates open implement beads for any missing required_files paths.
func EnsurePlanningImplementBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	if len(v.RequiredFiles) == 0 {
		return nil, nil
	}
	open, err := ListOpenImplementBeads(townRoot, rig, v)
	if err != nil {
		return nil, err
	}
	pathToID := map[string]string{}
	for _, b := range open {
		p := ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains)
		if p == "" || !IsValidImplementBeadPath(p) {
			continue
		}
		if _, ok := pathToID[p]; !ok {
			pathToID[p] = b.ID
		}
		for _, want := range v.RequiredFiles {
			if pathMatchesRequired(p, []string{want}) {
				if _, ok := pathToID[want]; !ok {
					pathToID[want] = b.ID
				}
			}
		}
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	workDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var created []string
	for _, want := range v.RequiredFiles {
		want = filepath.ToSlash(strings.TrimSpace(want))
		if want == "" {
			continue
		}
		has := false
		for p, id := range pathToID {
			if id != "" && pathMatchesRequired(p, []string{want}) {
				has = true
				break
			}
		}
		if has {
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

func parseBeadIDFromCreateOutput(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		for _, f := range fields {
			f = strings.Trim(f, "[](),")
			if strings.HasPrefix(f, "te-") || strings.HasPrefix(f, "hq-") {
				return f
			}
		}
	}
	return ""
}

// FormatPlanningBeadBootstrapBlock lists exact bd create lines for the planner.
func FormatPlanningBeadBootstrapBlock(rig string, v WorkflowValidation) string {
	if len(v.RequiredFiles) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Planning: implementation beads required\n\n")
	b.WriteString("You must run **CMD:** `bd create` lines in this session before JSON success. ")
	b.WriteString("Do not claim beads exist without command output showing new `te-xxx` IDs.\n\n")
	b.WriteString("Example (one line per file, adjust paths if needed):\n\n")
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
	b.WriteString(" bytes) with real IDs, `wc -c plan.md`, then JSON success in a **later** turn.\n")
	return b.String()
}

// ImplementBeadPathForID resolves the file path for an implement bead ID.
func ImplementBeadPathForID(townRoot, rig, beadID string, v WorkflowValidation) string {
	beadID = strings.TrimSpace(beadID)
	if beadID == "" {
		return ""
	}
	active, err := ListImplementBeadsOpenOrInProgress(townRoot, rig, v)
	if err != nil {
		return ""
	}
	for _, b := range active {
		if b.ID != beadID {
			continue
		}
		return ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains)
	}
	return ""
}

// FormatImplementationQueueBlock returns a single-line "next bead" hint for the polecat.
// Build order is enforced by gt-agent guards and NextOpenImplementBead — no full list needed.
func FormatImplementationQueueBlock(townRoot, rig string, v WorkflowValidation) string {
	if len(v.RequiredFiles) == 0 {
		return ""
	}
	order := OrderRequiredFilesForImplementation(v.RequiredFiles)
	next, err := NextOpenImplementBead(townRoot, rig, v)
	if err != nil {
		return fmt.Sprintf("**Next bead:** (error: %v)", err)
	}
	if next == nil {
		return "**Next bead:** none open — `bd list --status=closed` or JSON success if all implement beads are closed."
	}
	step, total := 0, len(order)
	for i, want := range order {
		if pathMatchesRequired(next.Title, []string{want}) {
			step = i + 1
			break
		}
	}
	mayorDir := filepath.Join(townRoot, rig, "mayor", "rig")
	beadPath := ExtractPathFromBeadTitle(next.Title, v.BeadTitleContains)
	verify := GoImplementationVerifyCommandForBead(v, mayorDir, beadPath)
	if step > 0 {
		return fmt.Sprintf("**Next bead (%d/%d):** %s → `%s` — work only this ID until `bd close`. Verify: `%s`.",
			step, total, next.ID, next.Title, verify)
	}
	return fmt.Sprintf("**Next bead:** %s → `%s` — work only this ID until `bd close`. Verify: `%s`.",
		next.ID, next.Title, verify)
}
