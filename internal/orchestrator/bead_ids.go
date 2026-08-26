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

// Bead IDs use a short rig prefix (e.g. t3-t9l, fi-vjd). Do not match long hyphenated words like docker-compose.
var summaryBeadIDRE = regexp.MustCompile(`(?i)\b([a-z0-9]{2,4}-[a-z0-9]{2,8})\b`)

// buildSummaryBeadIDRE returns a regex anchored on this rig's real prefix
// (from bd config issue_prefix). Anchoring on the prefix means English
// hyphenated words ("re-run", "co-op") never match, and digit-bearing
// prefixes ("t3-*") are matched correctly. Falls back to a loose scan when
// no prefix is available.
func buildSummaryBeadIDRE(rigPrefix string) *regexp.Regexp {
	p := strings.ToLower(strings.TrimSpace(rigPrefix))
	if p == "" {
		return regexp.MustCompile(`(?i)\b([a-z0-9]{2,4}-[a-z0-9]{2,8})\b`)
	}
	return regexp.MustCompile(`(?i)\b(` + regexp.QuoteMeta(p) + `-[a-z0-9]{2,8})\b`)
}

// looksLikeForeignBeadIssueID reports a short-code token from another rig
// (e.g. te-5d0) while rejecting English hyphenated words (re-run, co-op).
// Bead short-codes are base-36 and virtually always contain a digit, so a
// foreign token must contain a digit to be treated as a bead reference.
func looksLikeForeignBeadIssueID(id string) bool {
	if !looksLikeBeadIssueID(id) {
		return false
	}
	return strings.ContainsAny(id, "0123456789")
}

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

// RigBeadTitlesByID lists all rig issues (open + closed + in_progress) as ID → title.
// Used to verify TEST_PLAN.md bead mappings against what each bead actually owns.
func RigBeadTitlesByID(townRoot, rig string) (map[string]string, error) {
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	titles := make(map[string]string)
	for _, status := range []string{"open", "closed", "in_progress"} {
		args := beads.InjectFlatForListJSON([]string{"list", "--status=" + status, "--json", "--limit=0"})
		cmd := exec.Command("bd", args...)
		cmd.Env = append([]string{"BEADS_DIR=" + beadsDir}, cmd.Environ()...)
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
			return nil, fmt.Errorf("parse bd list %s: %w", status, err)
		}
		for _, r := range rows {
			id := strings.TrimSpace(strings.ToLower(beads.ExtractIssueID(r.ID)))
			if id != "" && r.Title != "" {
				titles[id] = r.Title
			}
		}
	}
	return titles, nil
}

// ExtractKnownRigBeadIDsFromSummary returns rig-prefixed bead IDs mentioned in text that exist in known.
// rigPrefix comes from bd config (issue_prefix) for this rig — not a hard-coded prefix.
func ExtractKnownRigBeadIDsFromSummary(summary, rigPrefix string, known map[string]bool) []string {
	summary = strings.TrimSpace(summary)
	if summary == "" || len(known) == 0 {
		return nil
	}
	rigPrefix = strings.ToLower(strings.TrimSpace(rigPrefix))
	var out []string
	seen := map[string]bool{}
	for _, m := range buildSummaryBeadIDRE(rigPrefix).FindAllStringSubmatch(summary, -1) {
		id := strings.ToLower(m[1])
		if seen[id] || isIgnoredSummaryToken(id) || summaryMentionsAgentIdentityBead(summary, id) {
			continue
		}
		if rigPrefix != "" && !strings.HasPrefix(id, rigPrefix+"-") {
			continue
		}
		if !known[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// ValidateSummaryBeadIDs rejects hallucinated bead IDs in QA/planner summaries.
// The primary scan is anchored on this rig's real prefix (so "re-run" is never
// a bead); a secondary scan only flags foreign-prefix tokens that contain a
// digit (real bead short-codes are base-36), avoiding English hyphenated words.
func ValidateSummaryBeadIDs(summary string, known map[string]bool, rigPrefix string) error {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil
	}
	rigPrefix = strings.TrimSpace(strings.ToLower(rigPrefix))
	var unknown []string
	seen := map[string]bool{}

	record := func(id, note string) {
		if seen[id] {
			return
		}
		seen[id] = true
		if note != "" {
			unknown = append(unknown, id+" "+note)
		} else {
			unknown = append(unknown, id)
		}
	}

	// Primary pass: this rig's own beads, anchored on the real prefix.
	for _, m := range buildSummaryBeadIDRE(rigPrefix).FindAllStringSubmatch(summary, -1) {
		id := strings.ToLower(m[1])
		if isIgnoredSummaryToken(id) || summaryMentionsAgentIdentityBead(summary, id) {
			continue
		}
		if known != nil && known[id] {
			continue
		}
		record(id, "")
	}

	// Secondary pass: foreign-prefix tokens that look like bead short-codes.
	if rigPrefix != "" {
		for _, m := range summaryBeadIDRE.FindAllStringSubmatch(summary, -1) {
			id := strings.ToLower(m[1])
			if seen[id] || isIgnoredSummaryToken(id) || summaryMentionsAgentIdentityBead(summary, id) {
				continue
			}
			if known != nil && known[id] {
				continue
			}
			if strings.HasPrefix(id, rigPrefix+"-") {
				continue
			}
			if !looksLikeForeignBeadIssueID(id) {
				continue
			}
			record(id, "(wrong prefix; rig uses "+rigPrefix+"-*)")
		}
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
	return isAgentIdentityBeadID(id)
}

// looksLikeBeadIssueID reports issue_prefix-id tokens (e.g. te-xoo), not delivery phase slugs (api-handlers).
func looksLikeBeadIssueID(id string) bool {
	parts := strings.Split(strings.ToLower(id), "-")
	if len(parts) != 2 {
		return false
	}
	if len(parts[0]) < 2 || len(parts[0]) > 4 {
		return false
	}
	suffix := parts[1]
	return len(suffix) >= 2 && len(suffix) <= 5
}

// summaryMentionsAgentIdentityBead returns true when the regex matched a prefix of a role bead (xx-<rig>-architect).
func summaryMentionsAgentIdentityBead(summary, matchedID string) bool {
	lower := strings.ToLower(summary)
	matchedID = strings.ToLower(matchedID)
	for _, role := range []string{"architect", "analyst", "qa", "witness", "refinery", "polecat"} {
		if strings.Contains(lower, matchedID+"-"+role) {
			return true
		}
	}
	return strings.Contains(lower, matchedID+"-crew-")
}

// isAgentIdentityBeadID reports rig patrol/role beads (e.g. xx-<rig>-architect), not implementation tasks.
func isAgentIdentityBeadID(id string) bool {
	for _, suf := range []string{"-architect", "-analyst", "-qa", "-witness", "-refinery", "-polecat"} {
		if strings.HasSuffix(id, suf) {
			return true
		}
	}
	return strings.Contains(id, "-crew-")
}
