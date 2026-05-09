package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a simple HTTP client for OpenAI-compatible LLM APIs.
type Client struct {
	endpoint string
	model    string
	role     string
	client   *http.Client
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
	reqBody := map[string]interface{}{
		"model": c.model,
		"messages": []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		"stream": false,
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
