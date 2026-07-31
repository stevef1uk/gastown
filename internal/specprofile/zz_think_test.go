package specprofile

import "testing"

func TestExtractJSONObjectThinkTags(t *testing.T) {
	raw := `<think>
Here's a thinking process:
1. Analyze the request.
   - Input: a list of phases.
   - Braces like { } and apostrophes like 'appear' inside reasoning.
2. Plan the output.
</think>
{
  "scaffold": "cd pkmanager && go test ./...",
  "auth-core-db": "cd pkmanager && go test ./backend/internal/auth"
}'
`
	var out map[string]string
	if err := ExtractJSONObject(raw, &out); err != nil {
		t.Fatalf("ExtractJSONObject failed: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 phases, got %d: %v", len(out), out)
	}
	if out["scaffold"] != "cd pkmanager && go test ./..." {
		t.Errorf("scaffold command wrong: %q", out["scaffold"])
	}
}

func TestExtractJSONObjectFencedWithThink(t *testing.T) {
	raw := "<thinking>\nSome reasoning with {nested} braces and \"quotes\".\n</thinking>\n```json\n{\"phase\": \"cd x && go test\"}\n```\n"
	var out map[string]string
	if err := ExtractJSONObject(raw, &out); err != nil {
		t.Fatalf("ExtractJSONObject failed: %v", err)
	}
	if out["phase"] != "cd x && go test" {
		t.Errorf("phase wrong: %q", out["phase"])
	}
}

func TestExtractJSONObjectTrailingApostrophe(t *testing.T) {
	raw := "<think>\nHere's a process: { } braces 'apostrophes'.\n</think>\n{\"a\": \"cd pkmanager && go test\"}'\n\nThat's all."
	var out map[string]string
	if err := ExtractJSONObject(raw, &out); err != nil {
		t.Fatalf("ExtractJSONObject failed: %v", err)
	}
	if out["a"] != "cd pkmanager && go test" {
		t.Errorf("a wrong: %q", out["a"])
	}
}

func TestExtractJSONObjectMultipleObjects(t *testing.T) {
	raw := `{"first": "value1"}{"second": "value2"}`
	var out map[string]string
	if err := ExtractJSONObject(raw, &out); err != nil {
		t.Fatalf("ExtractJSONObject failed: %v", err)
	}
	// Prefer the last complete object.
	if out["second"] != "value2" {
		t.Errorf("expected last object to win, got %v", out)
	}
}

func TestExtractJSONObjectBracesInStrings(t *testing.T) {
	raw := `{"cmd": "test -f {x} && echo '}'"} trailing prose with } braces`
	var out map[string]string
	if err := ExtractJSONObject(raw, &out); err != nil {
		t.Fatalf("ExtractJSONObject failed: %v", err)
	}
	if out["cmd"] != "test -f {x} && echo '}'" {
		t.Errorf("cmd wrong: %q", out["cmd"])
	}
}
