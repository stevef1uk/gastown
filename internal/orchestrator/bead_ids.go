package orchestrator

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
)

var summaryBeadIDRE = regexp.MustCompile(`\b([a-z][a-z0-9]{1,10}-[a-z0-9]{2,})\b`)

// RigIssuePrefix reads issue_prefix from the rig beads database (e.g. "de", "te").
func RigIssuePrefix(townRoot, rig string) (string, error) {
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	cmd := exec.Command("bd", "config", "get", "issue_prefix")
	cmd.Env = append([]string{"BEADS_DIR=" + beadsDir}, cmd.Environ()...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("bd config get issue_prefix: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// ListRigBeadIDSet returns known issue IDs (open + closed + in_progress) for a rig.
func ListRigBeadIDSet(townRoot, rig string) (map[string]bool, string, error) {
	prefix, err := RigIssuePrefix(townRoot, rig)
	if err != nil {
		return nil, "", err
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	ids := make(map[string]bool)
	for _, status := range []string{"open", "closed", "in_progress"} {
		args := beads.InjectFlatForListJSON([]string{"list", "--status=" + status, "--json", "--limit=0"})
		cmd := exec.Command("bd", args...)
		cmd.Env = append([]string{"BEADS_DIR=" + beadsDir}, cmd.Environ()...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return nil, prefix, fmt.Errorf("bd list %s: %w: %s", status, err, strings.TrimSpace(string(out)))
		}
		out = beads.StripStdoutWarnings(out)
		var rows []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(out, &rows); err != nil {
			return nil, prefix, fmt.Errorf("parse bd list %s: %w", status, err)
		}
		for _, r := range rows {
			id := strings.TrimSpace(beads.ExtractIssueID(r.ID))
			if id != "" {
				ids[id] = true
			}
		}
	}
	return ids, prefix, nil
}

// ValidateSummaryBeadIDs rejects hallucinated bead IDs in QA/planner summaries.
func ValidateSummaryBeadIDs(summary string, known map[string]bool, rigPrefix string) error {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil
	}
	rigPrefix = strings.TrimSpace(strings.ToLower(rigPrefix))
	var unknown []string
	seen := map[string]bool{}
	for _, m := range summaryBeadIDRE.FindAllStringSubmatch(summary, -1) {
		id := strings.ToLower(m[1])
		if seen[id] {
			continue
		}
		seen[id] = true
		if isIgnoredSummaryToken(id) {
			continue
		}
		if known != nil && known[id] {
			continue
		}
		if rigPrefix != "" && !strings.HasPrefix(id, rigPrefix+"-") {
			unknown = append(unknown, id+" (wrong prefix; rig uses "+rigPrefix+"-*)")
			continue
		}
		unknown = append(unknown, id)
	}
	if len(unknown) == 0 {
		return nil
	}
	return fmt.Errorf("summary cites bead ID(s) not in bd list — copy only from `bd list` output: %s", strings.Join(unknown, ", "))
}

func isIgnoredSummaryToken(id string) bool {
	switch id {
	case "gt-agent", "gt-role", "in-progress":
		return true
	}
	if strings.HasPrefix(id, "te-testgt") || strings.HasPrefix(id, "de-testgt") {
		return true // agent identity beads
	}
	return false
}
