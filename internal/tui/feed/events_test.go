package feed

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGtEventsSourceFollowsAfterEOF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".events.jsonl")
	history := `{"ts":"2026-11-04T12:00:00Z","type":"workflow_start","visibility":"feed","payload":{"workflow_id":"wf-1","template_id":"rig-flow","to_state":"design"}}` + "\n"
	if err := os.WriteFile(path, []byte(history), 0644); err != nil {
		t.Fatal(err)
	}
	source, err := NewGtEventsSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()

	expectEvent := func(eventType, message string) {
		t.Helper()
		select {
		case event := <-source.Events():
			if event.Type != eventType || event.Message != message {
				t.Fatalf("unexpected event: %+v", event)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for %s", eventType)
		}
	}
	expectEvent("workflow_start", "rig-flow wf-1 → design")
	// Let the tail reader reach EOF before appending anything.
	time.Sleep(250 * time.Millisecond)

	writer, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	appendLog := func(text string) {
		t.Helper()
		if _, err := writer.WriteString(text); err != nil {
			t.Fatal(err)
		}
	}
	appendLog(`{"ts":"2026-11-04T12:01:00Z","type":"workflow_transition","visibility":"feed","payload":{"workflow_id":"wf-1","template_id":"rig-flow","to_state":"planning"}}` + "\n")
	expectEvent("workflow_transition", "rig-flow wf-1 → planning")

	// A line split across writes must be retained until it is complete.
	appendLog(`{"ts":"2026-11-04T12:02:00Z","type":"workflow_transition","visibility":"feed",`)
	select {
	case event := <-source.Events():
		t.Fatalf("event emitted before the line was complete: %+v", event)
	case <-time.After(250 * time.Millisecond):
	}
	appendLog(`"payload":{"workflow_id":"wf-1","template_id":"rig-flow","to_state":"implementation"}}` + "\n")
	expectEvent("workflow_transition", "rig-flow wf-1 → implementation")
}

func TestGtEventsSourceFollowsReplacementAndTruncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".events.jsonl")
	line := func(kind string) string {
		return `{"ts":"2026-11-06T20:00:00Z","type":"` + kind + `","visibility":"feed","payload":{}}` + "\n"
	}
	first := line("boot")
	if err := os.WriteFile(path, []byte(first), 0644); err != nil {
		t.Fatal(err)
	}
	source, err := NewGtEventsSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	expect := func(kind string) {
		t.Helper()
		select {
		case e := <-source.Events():
			if e.Type != kind {
				t.Fatalf("got %s want %s", e.Type, kind)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("no %s after file change", kind)
		}
	}
	expect("boot")
	time.Sleep(150 * time.Millisecond)
	replacement := filepath.Join(dir, "replacement")
	if err := os.WriteFile(replacement, []byte(first+line("halt")), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	expect("halt") // The retained boot line must not replay.
	// Rewrite to a smaller file on the same inode, then keep following appends.
	if err := os.WriteFile(path, []byte(line("boot")), 0644); err != nil {
		t.Fatal(err)
	}
	expect("boot")
	writer, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString(line("halt")); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	expect("halt")
}
