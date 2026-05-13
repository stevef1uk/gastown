package cmd

import (
	"sort"
	"strings"

	"github.com/steveyegge/gastown/internal/mail"
)

// injectStripReFwd strips common reply prefixes for subject classification.
func injectStripReFwd(s string) string {
	t := strings.TrimSpace(s)
	for {
		lower := strings.ToLower(t)
		switch {
		case strings.HasPrefix(lower, "re:"):
			t = strings.TrimSpace(t[3:])
		case strings.HasPrefix(lower, "fwd:"):
			t = strings.TrimSpace(t[4:])
		default:
			return t
		}
	}
}

func injectSubjectIsBlocked(subject string) bool {
	t := strings.ToLower(injectStripReFwd(subject))
	return strings.HasPrefix(t, "blocked:")
}

// injectSubjectIsSupersedingSuccess is true when a newer mail from the same
// sender should obsolete earlier BLOCKED: noise (e.g. Plan Complete after a
// mistaken BLOCKED: architecture missing).
func injectSubjectIsSupersedingSuccess(subject string) bool {
	t := strings.ToLower(strings.TrimSpace(injectStripReFwd(subject)))
	if t == "" {
		return false
	}
	prefixes := []string{
		"plan complete",
		"plan ready",
		"architecture ready",
		"architecture complete",
		"qa complete",
		"qa ready",
		"polecat done",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// supersedeBlockedByNewerSuccessFromSameSender returns the unread slice with
// older BLOCKED: messages removed when the newest unread from that sender is a
// success signal. IDs removed are returned in markReadIDs so the caller can
// mark them read (clears --unread without deleting beads).
//
// Ordering of keep matches the original msgs order. Per-sender decisions use
// Timestamp (not slice order).
func supersedeBlockedByNewerSuccessFromSameSender(msgs []*mail.Message) (keep []*mail.Message, markReadIDs []string) {
	if len(msgs) < 2 {
		return msgs, nil
	}
	byFrom := make(map[string][]*mail.Message)
	var fromOrder []string
	for _, m := range msgs {
		if m == nil {
			continue
		}
		f := strings.TrimSpace(m.From)
		if _, ok := byFrom[f]; !ok {
			fromOrder = append(fromOrder, f)
		}
		byFrom[f] = append(byFrom[f], m)
	}
	markSet := make(map[string]struct{})
	for _, f := range fromOrder {
		group := byFrom[f]
		if len(group) < 2 {
			continue
		}
		sort.Slice(group, func(i, j int) bool {
			return group[i].Timestamp.Before(group[j].Timestamp)
		})
		newest := group[len(group)-1]
		if !injectSubjectIsSupersedingSuccess(newest.Subject) {
			continue
		}
		for i := 0; i < len(group)-1; i++ {
			if injectSubjectIsBlocked(group[i].Subject) {
				markSet[group[i].ID] = struct{}{}
			}
		}
	}
	if len(markSet) == 0 {
		return msgs, nil
	}
	ids := make([]string, 0, len(markSet))
	for id := range markSet {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, m := range msgs {
		if m == nil {
			continue
		}
		if _, drop := markSet[m.ID]; !drop {
			keep = append(keep, m)
		}
	}
	return keep, ids
}
