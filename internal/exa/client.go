package exa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

const (
	baseURL        = "https://api.exa.ai/search"
	numResults     = 10
	maxChars       = 10000 // Full article text — Haiku/Sonnet have 200K context, no reason to truncate
	requestTimeout = 15 * time.Second
)

// Client wraps the Exa semantic search API.
type Client struct {
	apiKey string
	http   *http.Client
	logger *slog.Logger
}

// NewClient creates an Exa client. apiKey comes from EXA_API_KEY env var.
func NewClient(apiKey string, logger *slog.Logger) *Client {
	return &Client{
		apiKey: apiKey,
		http:   &http.Client{Timeout: requestTimeout},
		logger: logger,
	}
}

// Result is a single Exa search result.
type Result struct {
	Title         string  `json:"title"`
	URL           string  `json:"url"`
	Text          string  `json:"text"`
	PublishedDate string  `json:"publishedDate"`
	Score         float64 `json:"score"`
}

// SearchResponse holds the Exa API response.
type SearchResponse struct {
	Results []Result `json:"results"`
}

// searchRequest follows the Exa search API format.
type searchRequest struct {
	Query          string         `json:"query"`
	Type           string         `json:"type"`           // "auto" = neural + keyword hybrid
	NumResults     int            `json:"numResults"`
	IncludeDomains []string       `json:"includeDomains,omitempty"`
	Contents       searchContents `json:"contents"`
}

type searchContents struct {
	Text textOptions `json:"text"`
}

type textOptions struct {
	MaxCharacters int `json:"maxCharacters"`
}

// Search executes a semantic search query. includeDomains are optional hints
// that steer results toward preferred sources without excluding others.
func (c *Client) Search(ctx context.Context, query string, includeDomains []string) (*SearchResponse, error) {
	reqBody := searchRequest{
		Query:          query,
		Type:           "auto", // neural + keyword hybrid for best results
		NumResults:     numResults,
		IncludeDomains: includeDomains,
		Contents: searchContents{
			Text: textOptions{MaxCharacters: maxChars},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal exa request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create exa request: %w", err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	c.logger.Debug("exa search", slog.String("query", query))

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exa request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read exa response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("exa API error %d: %s", resp.StatusCode, string(respBytes))
	}

	var result SearchResponse
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, fmt.Errorf("parse exa response: %w", err)
	}

	c.logger.Info("exa search complete",
		slog.Int("results", len(result.Results)),
	)

	return &result, nil
}
