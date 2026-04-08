package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// ClaudeOpusModel is the model used for the auditor (strongest reasoning).
	ClaudeOpusModel = "claude-opus-4-6"

	// auditorMaxTokens is enough for the structured JSON output + reasoning.
	auditorMaxTokens = 4096

	// AuditorTimeout allows for slow Opus responses — can take 2-5 minutes on complex analysis.
	AuditorTimeout = 5 * time.Minute
)

// CallClaude sends a single user message to Claude and returns the response text.
// Reuses the HTTP types and constants defined in condition.go (same package).
func CallClaude(ctx context.Context, apiKey, model, userMessage string, maxTokens int) (string, error) {
	req := claudeRequest{
		Model:     model,
		MaxTokens: maxTokens,
		Messages: []claudeMsg{
			{Role: "user", Content: userMessage},
		},
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", claudeBaseURL+claudeMessagesPath, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: AuditorTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("API request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr claudeResponse
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Error != nil {
			return "", fmt.Errorf("API error (%d): %s", resp.StatusCode, apiErr.Error.Message)
		}
		return "", fmt.Errorf("API error: status %d: %s", resp.StatusCode, string(respBody))
	}

	var claudeResp claudeResponse
	if err := json.Unmarshal(respBody, &claudeResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if len(claudeResp.Content) == 0 {
		return "", fmt.Errorf("no content in Claude response")
	}
	return claudeResp.Content[0].Text, nil
}
