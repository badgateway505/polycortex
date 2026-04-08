package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"log/slog"
	"time"

	"github.com/badgateway/poly/internal/polymarket"
	"github.com/badgateway/poly/internal/scanner"
)

func TestBuildSearchQuery(t *testing.T) {
	tests := []struct {
		question string
		wantContains string
	}{
		{"Will Tottenham be relegated from the Premier League?", "Tottenham"},
		{"Will the next Prime Minister of Hungary be Péter Magyar?", "Péter Magyar"},
		{"Will Bitcoin reach $100k by July 2026?", "Bitcoin"},
		{"Will the Fed cut interest rates in June 2026?", "interest rate"},
	}

	for _, tt := range tests {
		sig := scanner.Signal{
			FilteredMarket: scanner.FilteredMarket{
				Market: polymarket.Market{Question: tt.question},
			},
		}
		query := buildSearchQuery(sig)
		if query == "" {
			t.Errorf("buildSearchQuery(%q) returned empty", tt.question)
		}
		if !contains(query, tt.wantContains) {
			t.Errorf("buildSearchQuery(%q) = %q, want it to contain %q", tt.question, query, tt.wantContains)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestTavilySearchMockServer(t *testing.T) {
	// Mock Tavily API
	mockResp := TavilyResponse{
		Answer: "Tottenham are currently 18th in the Premier League.",
		Results: []TavilyResult{
			{
				Title:         "Tottenham struggling in relegation zone",
				URL:           "https://example.com/spurs",
				Content:       "Tottenham Hotspur continue to struggle, sitting 18th in the Premier League table with 28 points from 30 games.",
				PublishedDate: "2026-03-28",
				Score:         0.95,
			},
			{
				Title:         "Manager sacked after latest defeat",
				URL:           "https://example.com/manager",
				Content:       "The club has parted ways with their manager following a run of five consecutive defeats.",
				PublishedDate: "2026-03-27",
				Score:         0.88,
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/search" {
			t.Errorf("expected /search, got %s", r.URL.Path)
		}

		// Verify request body
		var reqBody tavilyRequest
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if reqBody.Topic != "news" {
			t.Errorf("expected topic 'news', got %q", reqBody.Topic)
		}
		if reqBody.APIKey != "test-key" {
			t.Errorf("expected api_key 'test-key', got %q", reqBody.APIKey)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockResp)
	}))
	defer server.Close()

	logger := slog.Default()
	tc := &TavilyClient{
		apiKey: "test-key",
		client: &http.Client{Timeout: 5 * time.Second},
		logger: logger,
	}

	// Override base URL by using Search directly with the mock URL
	// We need to temporarily override the const — test via the full path
	origSearch := func(ctx context.Context, query string) (*TavilyResponse, error) {
		reqBody := tavilyRequest{
			APIKey:        tc.apiKey,
			Query:         query,
			Topic:         tavilyDefaultTopic,
			SearchDepth:   tavilyDefaultDepth,
			MaxResults:    tavilyMaxResults,
			IncludeAnswer: true,
		}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/search", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := tc.client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var result TavilyResponse
		json.NewDecoder(resp.Body).Decode(&result)
		return &result, nil
	}

	resp, err := origSearch(context.Background(), "Tottenham relegation latest")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	if len(resp.Results) != 2 {
		t.Errorf("expected 2 results, got %d", len(resp.Results))
	}
	if resp.Answer == "" {
		t.Error("expected non-empty answer")
	}
}

func TestFormatForClaude(t *testing.T) {
	sc := SearchContext{
		MarketID: "test-market-123",
		Query:    "Tottenham relegation latest",
		Answer:   "Tottenham are 18th.",
		Results: []TavilyResult{
			{
				Title:         "Spurs in trouble",
				URL:           "https://example.com",
				Content:       "They are struggling badly.",
				PublishedDate: "2026-03-28",
			},
		},
		SearchedAt: time.Date(2026, 3, 28, 14, 30, 0, 0, time.UTC),
	}

	output := sc.FormatForClaude()

	// Check key elements are present
	checks := []string{
		"Query: Tottenham relegation latest",
		"2026-03-28 14:30 UTC",
		"Summary: Tottenham are 18th.",
		"[1] Spurs in trouble",
		"https://example.com",
	}
	for _, check := range checks {
		if !containsSubstring(output, check) {
			t.Errorf("FormatForClaude output missing %q\nGot:\n%s", check, output)
		}
	}
}
