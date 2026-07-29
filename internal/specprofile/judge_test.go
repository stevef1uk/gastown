package specprofile

import (
	"strings"
	"testing"
)

func TestExtractJSONObject(t *testing.T) {
	tests := []struct {
		input   string
		count   int
		wantErr bool
	}{
		{`{"a": "b", "c": "d"}`, 2, false},
		{`before {"x": "y"} after`, 1, false},
		// nested objects cannot unmarshal into map[string]string, skip

		{`{invalid`, 0, true},
		{`{"k": "v"} trailing`, 1, false},
	}

	for _, tc := range tests {
		var m map[string]string
		err := ExtractJSONObject(tc.input, &m)
		if tc.wantErr && err == nil {
			t.Errorf("expected error for %q", tc.input)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("unexpected error for %q: %v", tc.input, err)
		}
		if m != nil && len(m) != tc.count {
			t.Errorf("expected %d entries, got %d for %q", tc.count, len(m), tc.input)
		}
	}
}

func TestJudgeSystemPrompt(t *testing.T) {
	p := judgeSystemPrompt()
	if p == "" {
		t.Fatal("system prompt should not be empty")
	}
	if !strings.Contains(p, "qa_verify_command") {
		t.Error("system prompt should mention qa_verify_command")
	}
	if !strings.Contains(p, "placeholder") {
		t.Error("system prompt should mention placeholder")
	}
}
