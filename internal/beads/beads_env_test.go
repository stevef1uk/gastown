package beads

import "testing"

func TestStripExportedBashFunctions(t *testing.T) {
	t.Parallel()
	in := []string{
		"PATH=/bin",
		"BASH_FUNC__comp_complete_longopt%%=() { :; }",
		"HOME=/tmp",
	}
	got := stripExportedBashFunctions(in)
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	for _, e := range got {
		if len(e) >= 10 && e[:10] == "BASH_FUNC_" {
			t.Fatalf("BASH_FUNC_ leaked: %q", e)
		}
	}
}
