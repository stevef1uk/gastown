package orchestrator

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// RestoreNotice summarizes workflow instances loaded from instances.json at startup.
type RestoreNotice struct {
	Count int
	Lines []string
}

// BuildRestoreNotice reads persisted instances and formats operator-facing lines.
// Empty when no instances file or no instances.
func BuildRestoreNotice(townRoot string) (RestoreNotice, error) {
	snap, err := LoadInstancesSnapshot(townRoot)
	if err != nil {
		return RestoreNotice{}, err
	}
	if snap == nil || len(snap.Instances) == 0 {
		return RestoreNotice{}, nil
	}
	var lines []string
	for _, inst := range snap.Instances {
		if inst == nil || inst.ID == "" {
			continue
		}
		rig := inst.Variables["rig"]
		rigPart := ""
		if rig != "" {
			rigPart = fmt.Sprintf(" rig=%s", rig)
		}
		status := inst.Status
		if status == "" {
			status = "running"
		}
		lines = append(lines, fmt.Sprintf("%s %s state=%s status=%s%s",
			inst.ID, inst.TemplateID, inst.CurrentState, status, rigPart))
	}
	return RestoreNotice{Count: len(lines), Lines: lines}, nil
}

// WriteStartupLog prints restore lines to w (orchestrator.log / stderr).
func (n RestoreNotice) WriteStartupLog(w io.Writer) {
	if n.Count == 0 || w == nil {
		return
	}
	fmt.Fprintf(w, "[Orchestrator] Restored %d workflow instance(s) from %s (resume, not new):\n",
		n.Count, instancesFileName)
	for _, line := range n.Lines {
		fmt.Fprintf(w, "[Orchestrator]   %s\n", line)
	}
}

// LogRestoreNotice writes restore summary to stdout when instances were persisted.
func LogRestoreNotice(townRoot string) {
	n, err := BuildRestoreNotice(townRoot)
	if err != nil || n.Count == 0 {
		return
	}
	n.WriteStartupLog(os.Stdout)
}

// WorkflowResumeSummary is a short status string for gt up service detail.
func WorkflowResumeSummary(townRoot string) string {
	n, err := BuildRestoreNotice(townRoot)
	if err != nil || n.Count == 0 {
		return ""
	}
	active := 0
	var states []string
	seen := make(map[string]struct{})
	for _, line := range n.Lines {
		if strings.Contains(line, "status=completed") || strings.Contains(line, "status=failed") {
			continue
		}
		active++
		// line format: wf-N template state=XXX ...
		if i := strings.Index(line, "state="); i >= 0 {
			rest := line[i+len("state="):]
			state := rest
			if j := strings.IndexByte(rest, ' '); j >= 0 {
				state = rest[:j]
			}
			if _, ok := seen[state]; !ok && state != "" {
				seen[state] = struct{}{}
				states = append(states, state)
			}
		}
	}
	if active == 0 {
		return fmt.Sprintf("resumed %d workflow(s) (terminal)", n.Count)
	}
	if len(states) == 1 {
		return fmt.Sprintf("resumed %d active at %s (see: gt mayor workflow status)", active, states[0])
	}
	return fmt.Sprintf("resumed %d active workflow(s) (see: gt mayor workflow status)", active)
}

// DuplicateActiveWarning returns a warning when multiple running workflows share template+rig.
func DuplicateActiveWarning(statuses []WorkflowStatus) string {
	type key struct{ templateID, rig string }
	counts := make(map[key]int)
	for _, s := range statuses {
		if s.Status == "completed" || s.Status == "failed" {
			continue
		}
		rig := s.Variables["rig"]
		counts[key{s.TemplateID, rig}]++
	}
	var parts []string
	for k, n := range counts {
		if n <= 1 {
			continue
		}
		label := k.templateID
		if k.rig != "" {
			label += "/" + k.rig
		}
		parts = append(parts, fmt.Sprintf("%d active for %s", n, label))
	}
	if len(parts) == 0 {
		return ""
	}
	return "multiple orchestrator workflows: " + strings.Join(parts, "; ") +
		" (gt mayor workflow status; fail extras or edit orchestrator/instances.json)"
}
