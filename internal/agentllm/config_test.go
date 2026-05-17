package agentllm

import "testing"

func TestIsLocalProxy(t *testing.T) {
	for _, ep := range []string{
		"http://localhost:11434/v1/chat/completions",
		"http://127.0.0.1:11434/v1/chat/completions",
	} {
		if !IsLocalProxy(ep) {
			t.Fatalf("expected local: %s", ep)
		}
	}
	if IsLocalProxy("https://api.openai.com/v1/chat/completions") {
		t.Fatal("openai should not be local")
	}
}

func TestRequiresAuthToken(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	if !RequiresAuthToken("https://api.openai.com/v1/chat/completions") {
		t.Fatal("remote without key should require auth")
	}
	if RequiresAuthToken(DefaultEndpoint) {
		t.Fatal("local proxy should not require key")
	}
}
