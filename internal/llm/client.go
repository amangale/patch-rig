// Package llm is a thin Ollama HTTP client for patch-rig.
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

// Client talks to a local Ollama instance. Model must be pinned; phase 1
// uses qwen2.5:14b. Transport errors are retried exactly once per spec.
type Client struct {
	BaseURL string
	Model   string
	http    *http.Client
}

// New returns a client for the given Ollama base URL (e.g. http://localhost:11434)
// and pinned model tag.
func New(baseURL, model string) *Client {
	return &Client{
		BaseURL: baseURL,
		Model:   model,
		http:    &http.Client{Timeout: 600 * time.Second},
	}
}

type generateRequest struct {
	Model   string         `json:"model"`
	Prompt  string         `json:"prompt"`
	Stream  bool           `json:"stream"`
	Options map[string]any `json:"options,omitempty"`
}

type generateResponse struct {
	Response string `json:"response"`
}

// Generate sends a single prompt and returns the model's response.
// Low temperature keeps diff output deterministic-ish.
func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
	payload, err := json.Marshal(generateRequest{
		Model:  c.Model,
		Prompt: prompt,
		Stream: false,
		Options: map[string]any{
			"temperature": 0.2,
			"num_ctx":     16384,
		},
	})
	if err != nil {
		return "", fmt.Errorf("llm: marshal request: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		out, err := c.doPost(ctx, payload)
		if err == nil {
			return out, nil
		}
		lastErr = err
	}
	return "", fmt.Errorf("llm: generate failed after retry: %w", lastErr)
}

func (c *Client) doPost(ctx context.Context, payload []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/api/generate", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("llm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: transport: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("llm: status %d: %s", resp.StatusCode, string(b))
	}

	var out generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("llm: decode response: %w", err)
	}
	if out.Response == "" {
		return "", fmt.Errorf("llm: empty response")
	}
	return out.Response, nil
}
