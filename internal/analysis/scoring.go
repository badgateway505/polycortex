package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	authorityBoostValue = 1.5
	relevanceCutoff     = 3 // Raw relevance < 3 = drop (lenient — snippets are often noisy site chrome)
	maxScoredResults    = 10
)

// ScoredSnippet is a search result with a composite quality score.
type ScoredSnippet struct {
	Title         string  `json:"title"`
	URL           string  `json:"url"`
	Text          string  `json:"text"`
	PublishedDate string  `json:"published_date"`
	Source        string  `json:"source"` // "tavily" or "exa"

	Relevance      int     `json:"relevance"`       // 1-10 from Haiku
	RecencyWeight  float64 `json:"recency_weight"`  // 0.1-1.0
	AuthorityBoost float64 `json:"authority_boost"`  // 0 or 1.5
	CompositeScore float64 `json:"composite_score"` // (relevance + authority) * recency
}

// authorityDomains maps categories to high-authority domains.
var authorityDomains = map[MarketCategory][]string{
	CategoryPolitics: {
		"538.com", "fivethirtyeight.com", "silverbulletin.com",
		"electionbettingodds.com", "reuters.com", "apnews.com", "bloomberg.com",
	},
	CategoryCrypto: {
		"coindesk.com", "theblock.co", "messari.io",
		"glassnode.com", "delphi.xyz", "coingecko.com",
	},
	CategoryMacro: {
		"federalreserve.gov", "bls.gov", "bloomberg.com",
		"reuters.com", "wsj.com",
	},
	CategorySportsLeague: {
		"espn.com", "bbc.com", "bbc.co.uk", "transfermarkt.com",
		"fbref.com", "understat.com",
	},
	CategorySportsQualify: {
		"espn.com", "bbc.com", "bbc.co.uk", "transfermarkt.com",
		"fbref.com", "fifa.com",
	},
	CategoryCorporate: {
		"sec.gov", "reuters.com", "bloomberg.com", "ft.com",
	},
}

// RecencyMultiplier returns a soft decay weight based on article age.
// Never cuts off except >30 days (returns 0).
func RecencyMultiplier(publishedDate string) float64 {
	if publishedDate == "" {
		return 0.5 // Unknown date — treat as ~3 days old
	}

	// Try common date formats
	var t time.Time
	var err error
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z",
		"2006-01-02",
	}
	for _, f := range formats {
		t, err = time.Parse(f, publishedDate)
		if err == nil {
			break
		}
	}
	if err != nil {
		return 0.5 // Unparseable — treat as ~3 days old
	}

	hours := time.Since(t).Hours()
	switch {
	case hours < 6:
		return 1.0
	case hours < 24:
		return 0.9
	case hours < 72: // 3 days
		return 0.7
	case hours < 168: // 7 days
		return 0.5
	case hours < 336: // 14 days
		return 0.3
	case hours < 720: // 30 days
		return 0.1
	default:
		return 0.0 // Hard cutoff >30 days
	}
}

// AuthorityBoost returns +1.5 if the URL's domain is in the authority list
// for this category, 0 otherwise. Never penalises unknown domains.
func AuthorityBoost(rawURL string, category MarketCategory) float64 {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return 0
	}
	host := strings.ToLower(parsed.Hostname())
	// Strip "www." prefix
	host = strings.TrimPrefix(host, "www.")

	domains, ok := authorityDomains[category]
	if !ok {
		return 0
	}
	for _, d := range domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return authorityBoostValue
		}
	}
	return 0
}

// batchRelevanceResponse is the JSON response from the Haiku scoring call.
type batchRelevanceResponse struct {
	Scores []int `json:"scores"`
}

// BatchRelevanceScores sends all snippets to Haiku in one call and returns
// a 1-10 relevance score per snippet. Returns nil on failure (caller uses default).
func BatchRelevanceScores(ctx context.Context, apiKey string, question string, snippets []string, logger *slog.Logger) []int {
	if len(snippets) == 0 {
		return nil
	}

	var sb strings.Builder
	for i, s := range snippets {
		text := s
		if len(text) > 1000 {
			text = text[:1000] // Relevance scoring only needs enough to judge topic — not full article
		}
		sb.WriteString(fmt.Sprintf("%d. \"%s\"\n", i+1, text))
	}

	prompt := fmt.Sprintf(`Rate how relevant each snippet is to this prediction market question.

Market question: "%s"

Snippets (may contain site navigation noise — judge by the actual content, not formatting):
%s
Scoring guide:
- 8-10: Contains specific facts (numbers, dates, outcomes) directly about the market question
- 5-7: Related to the topic but lacks specific actionable data
- 3-4: Tangentially related, mentions the entity but not the specific question
- 1-2: Completely unrelated

Return JSON only: {"scores": [7, 3, 9, ...]}`, question, sb.String())

	req := claudeRequest{
		Model:     claudeModel, // Haiku
		MaxTokens: 128,
		System:    "You score search result relevance for prediction markets. Return only valid JSON with a scores array. No explanation.",
		Messages:  []claudeMsg{{Role: "user", Content: prompt}},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", claudeBaseURL+claudeMessagesPath, bytes.NewReader(body))
	if err != nil {
		return nil
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		logger.Warn("batch relevance API call failed", slog.String("err", err.Error()))
		return nil
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		logger.Warn("batch relevance API error", slog.Int("status", resp.StatusCode))
		return nil
	}

	var claudeResp claudeResponse
	if err := json.Unmarshal(respBytes, &claudeResp); err != nil || len(claudeResp.Content) == 0 {
		return nil
	}

	text := strings.TrimSpace(claudeResp.Content[0].Text)
	if strings.HasPrefix(text, "```") {
		if i := strings.Index(text, "\n"); i != -1 {
			text = text[i+1:]
		}
		if i := strings.LastIndex(text, "```"); i != -1 {
			text = text[:i]
		}
		text = strings.TrimSpace(text)
	}

	var result batchRelevanceResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		logger.Warn("batch relevance parse failed", slog.String("raw", text))
		return nil
	}

	logger.Info("batch relevance complete", slog.Int("scores", len(result.Scores)))
	return result.Scores
}

// ScoreResults computes composite scores, filters, and sorts results.
// Returns up to maxScoredResults, sorted by composite score descending.
// Results with relevance < 5 are dropped.
func ScoreResults(snippets []ScoredSnippet, scores []int) []ScoredSnippet {
	// Apply relevance scores if available
	if len(scores) == len(snippets) {
		for i := range snippets {
			snippets[i].Relevance = scores[i]
		}
	} else {
		// Fallback: assign default relevance of 7 (assume relevant)
		for i := range snippets {
			snippets[i].Relevance = 7
		}
	}

	// Compute composite scores and filter
	var scored []ScoredSnippet
	for _, s := range snippets {
		if s.Relevance < relevanceCutoff {
			continue // Hard gate: only LLM relevance can eliminate
		}
		if s.RecencyWeight == 0 {
			continue // >30 days — hard cutoff
		}
		s.CompositeScore = (float64(s.Relevance) + s.AuthorityBoost) * s.RecencyWeight
		scored = append(scored, s)
	}

	// Sort by composite score descending (simple insertion sort, <=20 items)
	for i := 1; i < len(scored); i++ {
		for j := i; j > 0 && scored[j].CompositeScore > scored[j-1].CompositeScore; j-- {
			scored[j], scored[j-1] = scored[j-1], scored[j]
		}
	}

	// Cap at max results
	if len(scored) > maxScoredResults {
		scored = scored[:maxScoredResults]
	}

	return scored
}
