package session

import (
	"testing"
)

func TestMayorSessionName(t *testing.T) {
	// Mayor session name is now fixed (one per machine), uses HQ prefix
	want := "hq-mayor"
	got := MayorSessionName()
	if got != want {
		t.Errorf("MayorSessionName() = %q, want %q", got, want)
	}
}

func TestDeaconSessionName(t *testing.T) {
	// Deacon session name is now fixed (one per machine), uses HQ prefix
	want := "hq-deacon"
	got := DeaconSessionName()
	if got != want {
		t.Errorf("DeaconSessionName() = %q, want %q", got, want)
	}
}

func TestPlannerSessionName(t *testing.T) {
	// Planner session name is now fixed (one per machine), uses HQ prefix
	want := "hq-planner"
	got := PlannerSessionName()
	if got != want {
		t.Errorf("PlannerSessionName() = %q, want %q", got, want)
	}
}

func TestSetupSessionName(t *testing.T) {
	want := "hq-setup"
	got := SetupSessionName()
	if got != want {
		t.Errorf("SetupSessionName() = %q, want %q", got, want)
	}
}

func TestOverseerSessionName(t *testing.T) {
	want := "hq-overseer"
	got := OverseerSessionName()
	if got != want {
		t.Errorf("OverseerSessionName() = %q, want %q", got, want)
	}
}

func TestWitnessSessionName(t *testing.T) {
	tests := []struct {
		rigPrefix string
		rigName   string
		want      string
	}{
		{"gt", "gastown", "gt-gastown-witness"},
		{"bd", "beads", "bd-beads-witness"},
		{"hop", "greenplace", "hop-greenplace-witness"},
		{"gt", "", "gt-witness"}, // compatibility case
	}
	for _, tt := range tests {
		t.Run(tt.rigPrefix+"/"+tt.rigName, func(t *testing.T) {
			got := WitnessSessionName(tt.rigPrefix, tt.rigName)
			if got != tt.want {
				t.Errorf("WitnessSessionName(%q, %q) = %q, want %q", tt.rigPrefix, tt.rigName, got, tt.want)
			}
		})
	}
}

func TestRefinerySessionName(t *testing.T) {
	tests := []struct {
		rigPrefix string
		rigName   string
		want      string
	}{
		{"gt", "gastown", "gt-gastown-refinery"},
		{"bd", "beads", "bd-beads-refinery"},
		{"hop", "greenplace", "hop-greenplace-refinery"},
	}
	for _, tt := range tests {
		t.Run(tt.rigPrefix+"/"+tt.rigName, func(t *testing.T) {
			got := RefinerySessionName(tt.rigPrefix, tt.rigName)
			if got != tt.want {
				t.Errorf("RefinerySessionName(%q, %q) = %q, want %q", tt.rigPrefix, tt.rigName, got, tt.want)
			}
		})
	}
}

func TestCrewSessionName(t *testing.T) {
	tests := []struct {
		rigPrefix string
		name      string
		want      string
	}{
		{"gt", "max", "gt-crew-max"},
		{"bd", "alice", "bd-crew-alice"},
		{"hop", "bar", "hop-crew-bar"},
	}
	for _, tt := range tests {
		t.Run(tt.rigPrefix+"/"+tt.name, func(t *testing.T) {
			got := CrewSessionName(tt.rigPrefix, tt.name)
			if got != tt.want {
				t.Errorf("CrewSessionName(%q, %q) = %q, want %q", tt.rigPrefix, tt.name, got, tt.want)
			}
		})
	}
}

func TestPolecatSessionName(t *testing.T) {
	tests := []struct {
		rigPrefix string
		name      string
		want      string
	}{
		{"gt", "Toast", "gt-Toast"},
		{"gt", "Furiosa", "gt-Furiosa"},
		{"bd", "worker1", "bd-worker1"},
		{"hop", "ostrom", "hop-ostrom"},
	}
	for _, tt := range tests {
		t.Run(tt.rigPrefix+"/"+tt.name, func(t *testing.T) {
			got := PolecatSessionName(tt.rigPrefix, tt.name)
			if got != tt.want {
				t.Errorf("PolecatSessionName(%q, %q) = %q, want %q", tt.rigPrefix, tt.name, got, tt.want)
			}
		})
	}
}

func TestArchitectSessionName(t *testing.T) {
	tests := []struct {
		rigPrefix string
		rigName   string
		want      string
	}{
		{"gt", "gastown", "gt-gastown-architect"},
		{"bd", "beads", "bd-beads-architect"},
		{"hop", "greenplace", "hop-greenplace-architect"},
	}
	for _, tt := range tests {
		t.Run(tt.rigPrefix+"/"+tt.rigName, func(t *testing.T) {
			got := ArchitectSessionName(tt.rigPrefix, tt.rigName)
			if got != tt.want {
				t.Errorf("ArchitectSessionName(%q, %q) = %q, want %q", tt.rigPrefix, tt.rigName, got, tt.want)
			}
		})
	}
}

func TestQASessionName(t *testing.T) {
	tests := []struct {
		rigPrefix string
		rigName   string
		want      string
	}{
		{"gt", "gastown", "gt-gastown-qa"},
		{"bd", "beads", "bd-beads-qa"},
		{"hop", "greenplace", "hop-greenplace-qa"},
	}
	for _, tt := range tests {
		t.Run(tt.rigPrefix+"/"+tt.rigName, func(t *testing.T) {
			got := QASessionName(tt.rigPrefix, tt.rigName)
			if got != tt.want {
				t.Errorf("QASessionName(%q, %q) = %q, want %q", tt.rigPrefix, tt.rigName, got, tt.want)
			}
		})
	}
}

func TestRigPolecatSessionName(t *testing.T) {
	tests := []struct {
		rigPrefix string
		rigName   string
		want      string
	}{
		{"gt", "mockrig", "gt-mockrig-polecat"},
		{"bd", "beads", "bd-beads-polecat"},
		{"hop", "greenplace", "hop-greenplace-polecat"},
	}
	for _, tt := range tests {
		t.Run(tt.rigPrefix+"/"+tt.rigName, func(t *testing.T) {
			got := RigPolecatSessionName(tt.rigPrefix, tt.rigName)
			if got != tt.want {
				t.Errorf("RigPolecatSessionName(%q, %q) = %q, want %q", tt.rigPrefix, tt.rigName, got, tt.want)
			}
		})
	}
}

func TestDefaultPrefix(t *testing.T) {
	want := "gt"
	if DefaultPrefix != want {
		t.Errorf("DefaultPrefix = %q, want %q", DefaultPrefix, want)
	}
}
