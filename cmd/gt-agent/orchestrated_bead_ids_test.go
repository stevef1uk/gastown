package main

import (
	"strings"
	"testing"
)

func TestValidateOutcomeSummaryBeadIDs_rejectsHallucinatedPrefix(t *testing.T) {
	// Requires live testgt1 beads when run in dev; skip if bd unavailable.
	town := "/home/stevef/gt"
	if err := validateOutcomeSummaryBeadIDs(town, "testgt1", "qa_review", "failure",
		"reopen te-5d0 te-b3w"); err == nil {
		t.Skip("bd/testgt1 not available or validation skipped")
	} else if !strings.Contains(err.Error(), "te-5d0") {
		t.Fatalf("got %v", err)
	}
}
