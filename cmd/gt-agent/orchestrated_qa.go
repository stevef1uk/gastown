package main

import (
	"strings"
	"time"
)

// orchestratedCommandTimeoutForTrack applies track-specific defaults when hooks omit cmd_timeout_seconds.
func orchestratedCommandTimeoutForTrack(track, cmd string) time.Duration {
	if strings.EqualFold(strings.TrimSpace(track), "qa") {
		lower := strings.ToLower(cmd)
		if strings.Contains(lower, "go run") && strings.Contains(lower, "cmd/server") {
			return 45 * time.Second
		}
		if strings.Contains(lower, "go build ./...") || strings.Contains(lower, "go test ./...") {
			return 2 * time.Minute
		}
	}
	return orchestratedCommandTimeout(cmd)
}

func qaCommandFailureNeedsCleanup(cmd string) bool {
	if commandStartsDevServer(cmd) {
		return true
	}
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "go run") || strings.Contains(lower, "go build") || strings.Contains(lower, "go test")
}

func appendQAFailureReportNudge(b *strings.Builder, cmd string, cmdErr error) {
	if b == nil {
		return
	}
	b.WriteString("\n---\n")
	if cmdErr != nil && strings.Contains(cmdErr.Error(), "command exceeded") {
		b.WriteString("QA command timed out; dev-server ports were released. ")
	} else {
		b.WriteString("QA command failed; dev-server ports were released. ")
	}
	b.WriteString("Do **not** repeat the same long `go run`+`curl` smoke CMD.\n")
	b.WriteString("Do **not** `sed`, `cat >`, or otherwise edit files under the implementation layout — send **failure** JSON so the polecat reopens handler/web beads.\n")
	b.WriteString("In your **next** message reply with **JSON only** (no CMD lines), e.g.\n")
	b.WriteString(`{"outcome":"failure","summary":"<HTTP status, curl error, route/path mismatches from output above>"}`)
	b.WriteString("\n")
	if commandStartsDevServer(cmd) {
		b.WriteString("If the failure is an implementation bug (wrong API path, null list, 405 on POST), name routes and statuses so the polecat can fix them.\n")
	}
}
