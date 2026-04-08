package analysis

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

	"github.com/badgateway/poly/internal/scanner"
)

const (
	tavilyBaseURL       = "https://api.tavily.com"
	tavilySearchPath    = "/search"
	tavilyDefaultTopic  = "news"
	tavilyDefaultDepth  = "advanced" // Better relevance filtering, ~$0.016/search
	tavilyMaxResults    = 10        // Fetch more, filter by relevance score
	tavilyRequestTimeout = 10 * time.Second
)

// TavilyClient wraps the Tavily search API for real-time news fetching.
// Used to enrich market signals with fresh context before human review
// or Claude Auditor analysis.
type TavilyClient struct {
	apiKey string
	client *http.Client
	logger *slog.Logger
}

// NewTavilyClient creates a Tavily client with the given API key.
func NewTavilyClient(apiKey string, logger *slog.Logger) *TavilyClient {
	return &TavilyClient{
		apiKey: apiKey,
		client: &http.Client{Timeout: tavilyRequestTimeout},
		logger: logger,
	}
}

// tavilyRequest is the POST body for the Tavily search API
type tavilyRequest struct {
	APIKey         string   `json:"api_key"`
	Query          string   `json:"query"`
	Topic          string   `json:"topic"`
	SearchDepth    string   `json:"search_depth"`
	MaxResults     int      `json:"max_results"`
	Days           int      `json:"days,omitempty"`           // Limit to articles from last N days
	IncludeAnswer  bool     `json:"include_answer"`
	IncludeDomains []string `json:"include_domains,omitempty"` // Prefer these domains
	ExcludeDomains []string `json:"exclude_domains,omitempty"`
}

// SearchOptions holds search parameters, exported for use by the research handler.
type SearchOptions struct {
	IncludeDomains []string
	ExcludeDomains []string
	Days           int
}

// TavilyResponse is the parsed response from Tavily search
type TavilyResponse struct {
	Answer  string         `json:"answer"`
	Results []TavilyResult `json:"results"`
}

// TavilyResult is a single search result
type TavilyResult struct {
	Title         string  `json:"title"`
	URL           string  `json:"url"`
	Content       string  `json:"content"`
	PublishedDate string  `json:"published_date"`
	Score         float64 `json:"score"`
}

// SearchContext holds Tavily results for a market signal, formatted for human review
// or as input to Claude Auditor
type SearchContext struct {
	MarketID    string         `json:"market_id"`
	Query       string         `json:"query"`
	Answer      string         `json:"answer,omitempty"`
	Results     []TavilyResult `json:"results"`
	SearchedAt  time.Time      `json:"searched_at"`
}

// Search executes a single Tavily search query with optional category-specific parameters.
func (tc *TavilyClient) Search(ctx context.Context, query string, opts *SearchOptions) (*TavilyResponse, error) {
	excludes := []string{"polymarket.com", "metaculus.com", "manifold.markets", "predictit.org"}
	var includes []string
	days := 0

	if opts != nil {
		excludes = append(excludes, opts.ExcludeDomains...)
		includes = opts.IncludeDomains
		days = opts.Days
	}

	reqBody := tavilyRequest{
		APIKey:         tc.apiKey,
		Query:          query,
		Topic:          tavilyDefaultTopic,
		SearchDepth:    tavilyDefaultDepth,
		MaxResults:     tavilyMaxResults,
		Days:           days,
		IncludeAnswer:  true,
		IncludeDomains: includes,
		ExcludeDomains: excludes,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal tavily request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tavilyBaseURL+tavilySearchPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create tavily request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	tc.logger.Debug("tavily search", slog.String("query", query))

	resp, err := tc.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tavily request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read tavily response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tavily API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result TavilyResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse tavily response: %w", err)
	}

	tc.logger.Info("tavily search complete",
		slog.String("query", truncateStr(query, 60)),
		slog.Int("results", len(result.Results)),
	)

	return &result, nil
}

// SearchForSignal builds a category-aware search query for a market signal
// and returns enriched context.
func (tc *TavilyClient) SearchForSignal(ctx context.Context, sig scanner.Signal) (*SearchContext, error) {
	category := DetectCategory(sig.Market.Question)
	query := buildSearchQuery(sig)
	opts := CategorySearchOptions(category)

	resp, err := tc.Search(ctx, query, opts)
	if err != nil {
		return nil, err
	}

	// Filter out low-relevance results (Tavily score < 0.5 is usually noise)
	var relevant []TavilyResult
	for _, r := range resp.Results {
		if r.Score >= 0.40 {
			relevant = append(relevant, r)
		}
	}

	return &SearchContext{
		MarketID:   sig.Market.ID,
		Query:      query,
		Answer:     resp.Answer,
		Results:    relevant,
		SearchedAt: time.Now().UTC(),
	}, nil
}

// SearchForSignals searches for multiple signals, returning results for each.
// Searches sequentially to stay within rate limits (~$0.008/search).
func (tc *TavilyClient) SearchForSignals(ctx context.Context, signals []scanner.Signal) ([]SearchContext, error) {
	var results []SearchContext

	for _, sig := range signals {
		if !sig.IsAlpha() {
			continue
		}

		sc, err := tc.SearchForSignal(ctx, sig)
		if err != nil {
			tc.logger.Warn("tavily search failed for signal, skipping",
				slog.String("market_id", sig.Market.ID),
				slog.String("error", err.Error()),
			)
			continue
		}
		results = append(results, *sc)
	}

	return results, nil
}

// buildSearchQuery creates a focused search query from a market signal.
// Uses the full market question as base — it contains the most context
// (country, names, specific outcome). Adds category-specific search terms.
func buildSearchQuery(sig scanner.Signal) string {
	category := DetectCategory(sig.Market.Question)
	question := sig.Market.Question

	// Strip "Will " prefix — search engines work better with declarative queries
	q := strings.TrimPrefix(question, "Will ")
	q = strings.TrimPrefix(q, "will ")
	q = strings.TrimSuffix(q, "?")

	switch category {
	case CategoryPolitics:
		return q + " latest polls news odds"
	case CategorySportsLeague:
		return q + " latest match results points table"
	case CategorySportsQualify:
		return q + " latest match results playoff schedule"
	case CategoryMacro:
		return q + " forecast data latest"
	case CategoryCrypto:
		return q + " price prediction news"
	case CategoryCorporate:
		return q + " news announcement filing"
	default:
		return q + " latest news"
	}
}

// CategorySearchOptions returns Tavily parameters tuned for each market category.
// include_domains steers results toward relevant sources; exclude_domains removes noise.
func CategorySearchOptions(cat MarketCategory) *SearchOptions {
	switch cat {
	case CategorySportsLeague, CategorySportsQualify:
		// No domain excludes — the full market question in the query is specific enough
		// to the sport. Excluding nba.com would break basketball markets.
		return &SearchOptions{
			Days: 7,
		}
	case CategoryPolitics:
		return &SearchOptions{
			Days: 14,
		}
	case CategoryMacro:
		return &SearchOptions{
			Days: 7,
		}
	case CategoryCrypto:
		return &SearchOptions{
			Days: 7,
		}
	case CategoryCorporate:
		return &SearchOptions{
			Days: 14,
		}
	default:
		return &SearchOptions{
			Days: 7,
		}
	}
}

// FormatForClaude formats search context as text suitable for pasting
// into the Claude Auditor prompt's TAVILY_CONTEXT section.
func (sc *SearchContext) FormatForClaude() string {
	var sb strings.Builder

	sb.WriteString("Query: " + sc.Query + "\n")
	sb.WriteString("Searched: " + sc.SearchedAt.Format("2006-01-02 15:04 UTC") + "\n\n")

	if sc.Answer != "" {
		sb.WriteString("Summary: " + sc.Answer + "\n\n")
	}

	for i, r := range sc.Results {
		sb.WriteString(fmt.Sprintf("[%d] %s\n", i+1, r.Title))
		sb.WriteString("    URL: " + r.URL + "\n")
		if r.PublishedDate != "" {
			sb.WriteString("    Date: " + r.PublishedDate + "\n")
		}
		sb.WriteString("    " + truncateStr(r.Content, 300) + "\n\n")
	}

	return sb.String()
}

// FormatAllForClaude formats multiple search contexts into a single text block.
func FormatSearchContextsForClaude(contexts []SearchContext) string {
	var sb strings.Builder

	for _, sc := range contexts {
		sb.WriteString("=== TAVILY: " + sc.MarketID + " ===\n")
		sb.WriteString(sc.FormatForClaude())
		sb.WriteString("\n")
	}

	return sb.String()
}
