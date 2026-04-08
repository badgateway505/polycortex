package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"
)

// CLOBClient wraps the Polymarket CLOB (Central Limit Order Book) REST API
type CLOBClient struct {
	client  *RateLimitedClient
	baseURL string
}

// NewCLOBClient creates a new CLOB API client
// Rate limit: 100 public requests/min (GET /book)
func NewCLOBClient() *CLOBClient {
	// 100 req/min = 100/60 = 1.67 req/sec
	requestsPerSec := 100.0 / 60.0

	return &CLOBClient{
		client:  NewRateLimitedClient(requestsPerSec, DefaultRetryConfig(), nil),
		baseURL: "https://clob.polymarket.com",
	}
}

// OrderBookLevel represents a single price level in the order book
type OrderBookLevel struct {
	Price string `json:"price"` // Price as string to avoid float precision issues
	Size  string `json:"size"`  // Size (shares) as string
}

// OrderBook represents the full L2 order book for a market
type OrderBook struct {
	Market    string           `json:"market"`     // Market ID
	Asset     string           `json:"asset_id"`   // Asset/outcome ID (YES or NO token)
	Hash      string           `json:"hash"`       // Book hash (for integrity)
	Bids      []OrderBookLevel `json:"bids"`       // Buy orders (descending price)
	Asks      []OrderBookLevel `json:"asks"`       // Sell orders (ascending price)
	Timestamp string           `json:"timestamp"`  // Server timestamp (returned as string by API)
}

// OrderBookResponse wraps the API error response (returned when the request fails)
type OrderBookResponse struct {
	Error string `json:"error,omitempty"`
}

// GetOrderBook fetches the L2 order book for a specific token ID (outcome)
// tokenID is the CLOB token address for the outcome (e.g., YES or NO token)
//
// Example:
//   book, err := client.GetOrderBook("0x1234...abcd")
//
// Rate limit: 100 public requests/min
// Timeout: 10 seconds
func (c *CLOBClient) GetOrderBook(tokenID string) (*OrderBook, error) {
	url := fmt.Sprintf("%s/book?token_id=%s", c.baseURL, tokenID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	// RateLimitedClient handles rate limiting and retries automatically
	resp, err := c.client.Do(context.Background(), req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// The CLOB API returns the order book directly (not wrapped in a success/error envelope)
	var book OrderBook
	if err := json.NewDecoder(resp.Body).Decode(&book); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &book, nil
}

// GetMarketOrderBook fetches order books for all outcomes of a market
// This is a convenience function that fetches books for both YES and NO outcomes
// Returns a map: outcome -> order book
//
// Note: For binary markets (YES/NO), you typically need to fetch both books
// to understand the full market liquidity
func (c *CLOBClient) GetMarketOrderBooks(yesTokenID, noTokenID string) (map[string]*OrderBook, error) {
	books := make(map[string]*OrderBook)

	// Fetch YES book
	yesBook, err := c.GetOrderBook(yesTokenID)
	if err != nil {
		return nil, fmt.Errorf("fetch YES book: %w", err)
	}
	books["YES"] = yesBook

	// Fetch NO book
	noBook, err := c.GetOrderBook(noTokenID)
	if err != nil {
		return nil, fmt.Errorf("fetch NO book: %w", err)
	}
	books["NO"] = noBook

	return books, nil
}

// BookSnapshot represents a simplified view of the order book
// with parsed float values for easier calculation
type BookSnapshot struct {
	TokenID       string
	BestBid       float64
	BestAsk       float64
	BidLevels     []PriceLevel
	AskLevels     []PriceLevel
	Spread        float64  // Absolute spread (ask - bid)
	SpreadPercent float64  // Spread as % of ask
	MidPrice      float64  // (bid + ask) / 2
	LastUpdate    time.Time
}

// PriceLevel represents a parsed order book level with float values
type PriceLevel struct {
	Price    float64
	Size     float64
	ValueUSD float64 // Price * Size
}

// ParseBookSnapshot converts raw order book into usable snapshot with floats
// This handles string->float conversion and calculates derived metrics
func ParseBookSnapshot(book *OrderBook) (*BookSnapshot, error) {
	if book == nil {
		return nil, fmt.Errorf("nil order book")
	}

	if len(book.Bids) == 0 || len(book.Asks) == 0 {
		return nil, fmt.Errorf("empty order book (bids: %d, asks: %d)", len(book.Bids), len(book.Asks))
	}

	snapshot := &BookSnapshot{
		TokenID:    book.Asset,
		LastUpdate: time.Now(), // Timestamp is an opaque string from the API
	}

	// Parse bids (highest price first)
	snapshot.BidLevels = make([]PriceLevel, 0, len(book.Bids))
	for _, bid := range book.Bids {
		price, size, err := parseLevel(bid)
		if err != nil {
			continue // Skip malformed levels
		}
		snapshot.BidLevels = append(snapshot.BidLevels, PriceLevel{
			Price:    price,
			Size:     size,
			ValueUSD: price * size,
		})
	}

	// Parse asks (lowest price first)
	snapshot.AskLevels = make([]PriceLevel, 0, len(book.Asks))
	for _, ask := range book.Asks {
		price, size, err := parseLevel(ask)
		if err != nil {
			continue // Skip malformed levels
		}
		snapshot.AskLevels = append(snapshot.AskLevels, PriceLevel{
			Price:    price,
			Size:     size,
			ValueUSD: price * size,
		})
	}

	if len(snapshot.BidLevels) == 0 || len(snapshot.AskLevels) == 0 {
		return nil, fmt.Errorf("no valid price levels after parsing")
	}

	// The CLOB API returns bids ascending and asks descending — normalize to standard convention:
	//   bids descending (best bid = highest price first)
	//   asks ascending  (best ask = lowest price first)
	sort.Slice(snapshot.BidLevels, func(i, j int) bool {
		return snapshot.BidLevels[i].Price > snapshot.BidLevels[j].Price
	})
	sort.Slice(snapshot.AskLevels, func(i, j int) bool {
		return snapshot.AskLevels[i].Price < snapshot.AskLevels[j].Price
	})

	// Best prices
	snapshot.BestBid = snapshot.BidLevels[0].Price
	snapshot.BestAsk = snapshot.AskLevels[0].Price

	// Derived metrics
	snapshot.Spread = snapshot.BestAsk - snapshot.BestBid
	snapshot.SpreadPercent = (snapshot.Spread / snapshot.BestAsk) * 100.0
	snapshot.MidPrice = (snapshot.BestBid + snapshot.BestAsk) / 2.0

	return snapshot, nil
}

// TradeEvent represents a single executed trade from the CLOB trade history
type TradeEvent struct {
	ID        string    `json:"id"`
	Side      string    `json:"side"`    // "BUY" or "SELL" (taker perspective)
	Outcome   string    `json:"outcome"` // "YES" or "NO"
	Price     float64
	Size      float64   // shares traded
	ValueUSD  float64   // Price × Size
	Timestamp time.Time
}

// dataAPITradeRaw mirrors the JSON shape returned by the public Data API at
// https://data-api.polymarket.com/trades?market=<conditionID>
// Fields differ from the authenticated CLOB /trades endpoint:
//   - price and size are floats (not strings)
//   - timestamp is unix seconds (not milliseconds)
//   - outcome is "Yes"/"No" (not "YES"/"NO")
//   - no "id" field; we derive one from transactionHash + outcomeIndex
type dataAPITradeRaw struct {
	Side            string  `json:"side"`            // "BUY" or "SELL"
	Outcome         string  `json:"outcome"`         // "Yes" or "No"
	Price           float64 `json:"price"`
	Size            float64 `json:"size"`
	Timestamp       int64   `json:"timestamp"`       // Unix seconds
	TransactionHash string  `json:"transactionHash"`
	OutcomeIndex    int     `json:"outcomeIndex"`
}

const dataAPIBaseURL = "https://data-api.polymarket.com"

// GetMarketTradesEvents fetches recent executed trades for a market via the
// public Data API (no auth required). conditionID is the hex condition ID.
// limit controls how many trades to return (max 500 per request).
func (c *CLOBClient) GetMarketTradesEvents(conditionID string, limit int) ([]TradeEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	url := fmt.Sprintf("%s/trades?market=%s&limit=%d", dataAPIBaseURL, conditionID, limit)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(context.Background(), req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var raw []dataAPITradeRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	trades := make([]TradeEvent, 0, len(raw))
	for _, r := range raw {
		// Normalize outcome to uppercase to match internal convention.
		outcome := r.Outcome
		if outcome == "Yes" {
			outcome = "YES"
		} else if outcome == "No" {
			outcome = "NO"
		}

		// Synthesize a stable ID from transaction hash + outcome index.
		id := fmt.Sprintf("%s_%d", r.TransactionHash, r.OutcomeIndex)

		trades = append(trades, TradeEvent{
			ID:        id,
			Side:      r.Side,
			Outcome:   outcome,
			Price:     r.Price,
			Size:      r.Size,
			ValueUSD:  r.Price * r.Size,
			Timestamp: time.Unix(r.Timestamp, 0),
		})
	}

	return trades, nil
}

// parseLevel converts string price/size to floats
func parseLevel(level OrderBookLevel) (price, size float64, err error) {
	if _, err := fmt.Sscanf(level.Price, "%f", &price); err != nil {
		return 0, 0, fmt.Errorf("parse price: %w", err)
	}
	if _, err := fmt.Sscanf(level.Size, "%f", &size); err != nil {
		return 0, 0, fmt.Errorf("parse size: %w", err)
	}
	return price, size, nil
}
