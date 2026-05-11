package cmd

import (
	"testing"
)

func TestIsDrainableMessage(t *testing.T) {
	tests := []struct {
		subject   string
		drainable bool
	}{
		// Drainable protocol messages
		{"CRASHED_POLECAT: furiosa", true},
		{"POLECAT_DONE furiosa", true},
		{"POLECAT_STARTED: furiosa", true},
		{"LIFECYCLE:Shutdown furiosa", true},
		{"LIFECYCLE:Restart furiosa", true},
		{"MERGED furiosa", true},
		{"MERGE_READY furiosa", true},
		{"MERGE_FAILED furiosa", true},
		{"SWARM_START", true},

		// Drainable handoff subjects (added in fix #75 — these were
		// accumulating in mayor's inbox to 78+ messages).
		{"Architecture Ready", true},
		{"Architecture Complete: design.md ready", true},
		{"Architecture location: /home/stevef/gt/.../architecture.md", true},
		{"Plan Complete", true},
		{"Plan Complete for hq-bbn", true},
		{"Plan Ready", true},
		{"QA Complete", true},
		{"Review Complete", true},
		{"BLOCKED: architecture missing", true},

		// Non-drainable messages (need attention)
		{"HELP: stuck on implementation", false},
		{"🤝 HANDOFF", false}, // emoji prefix — preserved
		{"Status check", false},
		{"Question about deployment", false},
		{"ALERT: something", false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.subject, func(t *testing.T) {
			got := isDrainableMessage(tc.subject)
			if got != tc.drainable {
				t.Errorf("isDrainableMessage(%q) = %v, want %v", tc.subject, got, tc.drainable)
			}
		})
	}
}

// TestClassifyDrainableSubject pins fix #75: the classifier must split
// "protocol" subjects (drainable on age alone) from "handoff" subjects
// (drainable only when ALSO read by the recipient). Mixing the two
// risks silently dropping a critical unread "Architecture Ready" mail
// before mayor has acted on it.
func TestClassifyDrainableSubject(t *testing.T) {
	tests := []struct {
		subject string
		want    string
	}{
		// Protocol — drainable on age alone.
		{"POLECAT_DONE furiosa", "protocol"},
		{"LIFECYCLE:Shutdown", "protocol"},
		{"MERGED foo", "protocol"},
		{"SWARM_START", "protocol"},

		// Handoff — drainable only if Read.
		{"Architecture Ready", "handoff"},
		{"Architecture Plan Submission for hq-bbn", "handoff"},
		{"Plan Complete", "handoff"},
		{"QA Complete", "handoff"},
		{"BLOCKED: architecture missing", "handoff"},

		// Not drainable by subject (still may be drainable as a read
		// wisp via the separate code path in runMailDrain).
		{"HELP: stuck", ""},
		{"🤝 HANDOFF", ""},
		{"Status check", ""},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.subject, func(t *testing.T) {
			got := classifyDrainableSubject(tc.subject)
			if got != tc.want {
				t.Errorf("classifyDrainableSubject(%q) = %q, want %q",
					tc.subject, got, tc.want)
			}
		})
	}
}
