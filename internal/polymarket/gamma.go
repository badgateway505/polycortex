package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

const (
	gammaBaseURL    = "https://gamma-api.polymarket.com"
	marketsEndpoint = "/markets"
	pageSize        = 100
	// Gamma API rate limit: 100 req/min = ~1.67 req/sec
	gammaRateLimit  = 100.0 / 60.0
	// Minimum liquidity to consider a market worth scanning (in USD)
	minLiquidity    = 1000.0
)

// Market represents a single market from the Gamma API
// Note: API returns some fields as strings (liquidity, volume, outcomePrices)
// and numeric versions (liquidityNum, volumeNum). We use the numeric ones.
type Market struct {
	ID            string    `json:"id"`
	EventID       int64     `json:"eventId"`     // Numeric event ID — used for comments API (parent_entity_type=Event)
	ConditionID   string    `json:"conditionId"` // Hex condition ID for CLOB trade queries
	Question      string    `json:"question"`
	Slug          string    `json:"slug"` // URL slug for polymarket.com link
	Category      string    `json:"category"`
	EndDate       time.Time `json:"endDate"`
	LiquidityNum  float64   `json:"liquidityNum"`
	VolumeNum     float64   `json:"volumeNum"`     // Total cumulative volume (all time)
	Volume24h     float64   `json:"volume24hr"`    // Last 24h volume — use this for liveness/activity checks
	Volume1Wk     float64   `json:"volume1wk"`     // Last 7-day volume — used for activity ratio
	OutcomePrices string    `json:"outcomePrices"` // JSON string: "[\"yes_price\", \"no_price\"]"
	Description   string    `json:"description"`   // Resolution criteria and market context
	Active        bool      `json:"active"`
	Closed        bool      `json:"closed"`
	CreatedAt     time.Time `json:"createdAt"`
	Tokens        []Token   `json:"tokens,omitempty"` // CLOB token addresses (YES/NO outcomes) — may be absent
	ClobTokenIds  string    `json:"clobTokenIds"`     // JSON string: "[\"yes_token_id\", \"no_token_id\"]"
}

// Token represents a market outcome token with its CLOB address
type Token struct {
	TokenID  string `json:"token_id"`  // CLOB token address (contract address)
	Outcome  string `json:"outcome"`   // "Yes" or "No"
	Winner   bool   `json:"winner"`    // True if this outcome won (after resolution)
}

// Note: Gamma API returns markets as a direct array, not wrapped in a response object

// GammaClient wraps the Gamma API
type GammaClient struct {
	baseURL string
	client  *RateLimitedClient
	logger  *slog.Logger
}

// NewGammaClient creates a new Gamma API client with rate limiting (100 req/min)
func NewGammaClient(logger *slog.Logger) *GammaClient {
	if logger == nil {
		logger = slog.Default()
	}

	return &GammaClient{
		baseURL: gammaBaseURL,
		client:  NewRateLimitedClient(gammaRateLimit, DefaultRetryConfig(), logger),
		logger:  logger,
	}
}

// FetchMarkets fetches markets from Gamma API with pagination
// Fetches up to 500 markets (5 pages of 100 each)
// Returns a slice of markets and any error encountered
func (gc *GammaClient) FetchMarkets() ([]Market, error) {
	return gc.FetchMarketsLimit(500)
}

// FetchMarketsLimit fetches up to `limit` filtered markets from Gamma API with pagination.
// Keeps paginating until we collect enough qualifying markets or the API runs dry.
func (gc *GammaClient) FetchMarketsLimit(limit int) ([]Market, error) {
	var allMarkets []Market
	totalRaw := 0
	emptyFilterPages := 0

	for page := 0; len(allMarkets) < limit; page++ {
		pr, err := gc.fetchMarketsPage(page)
		if err != nil {
			return nil, err
		}
		if pr.apiEmpty {
			gc.logger.Info("Gamma API exhausted — no more markets",
				slog.Int("pages_fetched", page),
				slog.Int("raw_markets_seen", totalRaw),
				slog.Int("filtered_collected", len(allMarkets)),
				slog.Int("empty_filter_pages", emptyFilterPages))
			break
		}

		totalRaw += pr.rawCount
		if len(pr.markets) == 0 {
			emptyFilterPages++
		}
		allMarkets = append(allMarkets, pr.markets...)

		if page > 0 && page%50 == 0 {
			gc.logger.Info("Fetch progress",
				slog.Int("pages", page),
				slog.Int("raw", totalRaw),
				slog.Int("collected", len(allMarkets)),
				slog.Int("empty_filter_pages", emptyFilterPages))
		}
	}

	gc.logger.Info("Gamma fetch complete",
		slog.Int("requested", limit),
		slog.Int("raw_markets_seen", totalRaw),
		slog.Int("collected", len(allMarkets)),
		slog.Int("empty_filter_pages", emptyFilterPages))

	if len(allMarkets) > limit {
		allMarkets = allMarkets[:limit]
	}

	return allMarkets, nil
}

// pageResult holds both filtered markets and whether the API had more data
type pageResult struct {
	markets  []Market
	rawCount int  // how many the API returned before filtering
	apiEmpty bool // true when the API itself returned 0 results (exhausted)
}

// fetchMarketsPage fetches a single page of markets from the Gamma API endpoint
// Requests only non-closed markets (closed=false filters to active CLOB markets)
// The API returns an array directly, limited to pageSize results
func (gc *GammaClient) fetchMarketsPage(page int) (pageResult, error) {
	offset := page * pageSize
	url := fmt.Sprintf("%s%s?limit=%d&offset=%d&closed=false", gc.baseURL, marketsEndpoint, pageSize, offset)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return pageResult{}, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := gc.client.Do(context.Background(), req)
	if err != nil {
		return pageResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body := make([]byte, 256)
		n, _ := resp.Body.Read(body)
		return pageResult{}, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body[:n]))
	}

	var markets []Market
	if err := json.NewDecoder(resp.Body).Decode(&markets); err != nil {
		return pageResult{}, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(markets) == 0 {
		return pageResult{apiEmpty: true}, nil
	}

	// Filter for markets with real liquidity and future endDate
	var filtered []Market
	now := time.Now()
	for _, m := range markets {
		if m.LiquidityNum >= minLiquidity && m.EndDate.After(now) {
			filtered = append(filtered, m)
		}
	}

	return pageResult{markets: filtered, rawCount: len(markets)}, nil
}
