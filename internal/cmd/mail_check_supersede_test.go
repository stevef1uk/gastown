package cmd

import (
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/mail"
)

func TestSupersedeBlockedByNewerSuccessFromSameSender(t *testing.T) {
	base := time.Date(2026, 5, 13, 17, 14, 0, 0, time.Local)
	tLater := base.Add(2 * time.Minute)

	t.Run("planner blocked then plan complete drops blocked", func(t *testing.T) {
		msgs := []*mail.Message{
			{ID: "hq-wisp-id6", From: "planner/", Subject: "BLOCKED: architecture missing", Timestamp: base},
			{ID: "hq-wisp-in2", From: "planner/", Subject: "Plan Complete", Timestamp: tLater},
		}
		keep, mark := supersedeBlockedByNewerSuccessFromSameSender(msgs)
		if len(mark) != 1 || mark[0] != "hq-wisp-id6" {
			t.Fatalf("markReadIDs = %v, want [hq-wisp-id6]", mark)
		}
		if len(keep) != 1 || keep[0].ID != "hq-wisp-in2" {
			t.Fatalf("keep = %v, want single Plan Complete id", idsOf(keep))
		}
	})

	t.Run("preserve order of remaining", func(t *testing.T) {
		msgs := []*mail.Message{
			{ID: "a", From: "planner/", Subject: "Plan Complete", Timestamp: tLater},
			{ID: "b", From: "mayor/", Subject: "FYI", Timestamp: base},
			{ID: "c", From: "planner/", Subject: "BLOCKED: x", Timestamp: base},
		}
		keep, mark := supersedeBlockedByNewerSuccessFromSameSender(msgs)
		if len(mark) != 1 || mark[0] != "c" {
			t.Fatalf("mark = %v", mark)
		}
		got := idsOf(keep)
		want := []string{"a", "b"}
		if len(got) != len(want) {
			t.Fatalf("keep ids %v want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("keep order %v want %v", got, want)
			}
		}
	})

	t.Run("no supersede when newest is not success", func(t *testing.T) {
		msgs := []*mail.Message{
			{ID: "a", From: "planner/", Subject: "Plan Complete", Timestamp: base},
			{ID: "b", From: "planner/", Subject: "BLOCKED: still bad", Timestamp: tLater},
		}
		keep, mark := supersedeBlockedByNewerSuccessFromSameSender(msgs)
		if len(mark) != 0 {
			t.Fatalf("expected no mark-read, got %v", mark)
		}
		if len(keep) != 2 {
			t.Fatalf("keep len %d", len(keep))
		}
	})

	t.Run("different senders no cross-supersede", func(t *testing.T) {
		msgs := []*mail.Message{
			{ID: "a", From: "planner/", Subject: "BLOCKED: x", Timestamp: base},
			{ID: "b", From: "witness/", Subject: "Plan Complete", Timestamp: tLater},
		}
		keep, mark := supersedeBlockedByNewerSuccessFromSameSender(msgs)
		if len(mark) != 0 {
			t.Fatalf("mark %v", mark)
		}
		if len(keep) != 2 {
			t.Fatalf("keep len %d", len(keep))
		}
	})

	t.Run("single message unchanged", func(t *testing.T) {
		msgs := []*mail.Message{
			{ID: "x", From: "planner/", Subject: "BLOCKED: only", Timestamp: base},
		}
		keep, mark := supersedeBlockedByNewerSuccessFromSameSender(msgs)
		if len(mark) != 0 || len(keep) != 1 {
			t.Fatalf("mark=%v keep=%d", mark, len(keep))
		}
	})
}

func idsOf(msgs []*mail.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if m != nil {
			out = append(out, m.ID)
		}
	}
	return out
}
