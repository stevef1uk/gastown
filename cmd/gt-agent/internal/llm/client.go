package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxResponseBodyBytes limits LLM HTTP response bodies to prevent OOM crashes
// from large model outputs (e.g. when the agent embeds store.go content).
const maxResponseBodyBytes = 10 << 20 // 10 MB

// Client is a simple HTTP client for OpenAI-compatible LLM APIs.
type Client struct {
	endpoint string
	model    string
	role     string
	client   *http.Client
}

// Endpoint returns the configured chat-completions URL.
func (c *Client) Endpoint() string {
	if c == nil {
		return ""
	}
	return c.endpoint
}

// HealthCheckURL derives a lightweight GET URL from an OpenAI-compatible chat endpoint.
func HealthCheckURL(chatEndpoint string) string {
	chatEndpoint = strings.TrimSpace(chatEndpoint)
	if chatEndpoint == "" {
		return ""
	}
	u, err := url.Parse(chatEndpoint)
	if err != nil {
		return ""
	}
	path := u.Path
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		u.Path = strings.TrimSuffix(path, "/chat/completions") + "/models"
	case path == "" || path == "/":
		u.Path = "/v1/models"
	default:
		u.Path = strings.TrimRight(path, "/") + "/models"
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// Ping checks that the LLM HTTP server accepts connections before a long agent loop.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || strings.TrimSpace(c.endpoint) == "" {
		return fmt.Errorf("LLM endpoint not configured")
	}
	checkURL := HealthCheckURL(c.endpoint)
	if checkURL == "" {
		return fmt.Errorf("invalid LLM endpoint %q", c.endpoint)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w (GET %s)", err, checkURL)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 500 {
		return nil
	}
	return fmt.Errorf("status %d from GET %s", resp.StatusCode, checkURL)
}

// NewClient creates a new LLM client.
func NewClient(endpoint, model, role string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	return &Client{
		endpoint: endpoint,
		model:    model,
		role:     role,
		client:   &http.Client{Timeout: timeout},
	}
}

// Message is a chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CompletionResponse is an OpenAI-compatible chat completion response.
type CompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Complete sends a prompt to the LLM and returns the response.
func (c *Client) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}
	return c.CompleteMessages(ctx, messages)
}

// CompleteMessages sends a sequence of messages to the LLM.
func (c *Client) CompleteMessages(ctx context.Context, messages []Message) (string, error) {
	reqBody := map[string]interface{}{
		"model":    c.model,
		"messages": messages,
		"stream":   false,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(jsonData))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.role != "" {
		req.Header.Set("X-GasTown-Role", c.role)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Limit response body to prevent OOM crashes from large LLM output.
	resp.Body = http.MaxBytesReader(nil, resp.Body, maxResponseBodyBytes)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("LLM API error (status %d): %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	var result CompletionResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return result.Choices[0].Message.Content, nil
}
