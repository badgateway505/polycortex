package perplexity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	baseURL        = "https://api.perplexity.ai/chat/completions"
	defaultModel   = "sonar"     // $1/1000 searches — routine research
	proModel       = "sonar-pro" // $5/1000 searches — deep dives
	requestTimeout = 60 * time.Second
)

// Client wraps the Perplexity Sonar API.
type Client struct {
	apiKey string
	http   *http.Client
	logger *slog.Logger
}

// NewClient creates a Perplexity client. apiKey comes from PPLX_API_KEY env var.
func NewClient(apiKey string, logger *slog.Logger) *Client {
	return &Client{
		apiKey: apiKey,
		http:   &http.Client{Timeout: requestTimeout},
		logger: logger,
	}
}

// Response holds the Perplexity API response.
type Response struct {
	Text      string   `json:"text"`
	Citations []string `json:"citations"`
	Model     string   `json:"model"`
}

// chatRequest follows the OpenAI chat completions format that Perplexity uses.
type chatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponse is the raw Perplexity API response envelope.
type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Citations []string `json:"citations"`
	Error     *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Research sends a prompt to Perplexity Sonar with web search and returns the response.
// Set usePro=true for sonar-pro (higher quality, 5× cost).
func (c *Client) Research(ctx context.Context, prompt string, usePro bool) (*Response, error) {
	model := defaultModel
	if usePro {
		model = proModel
	}

	reqBody := chatRequest{
		Model: model,
		Messages: []message{
			{Role: "user", Content: prompt},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	c.logger.Info("perplexity sonar research", slog.String("model", model))

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("perplexity API error %d: %s", resp.StatusCode, string(respBytes))
	}

	var raw chatResponse
	if err := json.Unmarshal(respBytes, &raw); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if raw.Error != nil {
		return nil, fmt.Errorf("perplexity error: %s", raw.Error.Message)
	}
	if len(raw.Choices) == 0 {
		return nil, fmt.Errorf("empty choices in perplexity response")
	}

	return &Response{
		Text:      raw.Choices[0].Message.Content,
		Citations: raw.Citations,
		Model:     raw.Model,
	}, nil
}

// FormatForSession formats the response as a string for storing in the session
// and feeding into the Claude Auditor prompt's PERPLEXITY_OUTPUT section.
func (r *Response) FormatForSession() string {
	var sb strings.Builder
	sb.WriteString(r.Text)
	if len(r.Citations) > 0 {
		sb.WriteString("\n\n--- CITATIONS ---\n")
		for i, c := range r.Citations {
			sb.WriteString(fmt.Sprintf("[%d] %s\n", i+1, c))
		}
	}
	return sb.String()
}
