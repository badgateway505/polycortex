package scanner

import (
	"fmt"
	"time"

	"github.com/badgateway/poly/internal/polymarket"
)

// SignalRoute indicates whether a market cleared L4 checks (Alpha) or only passed L1-L3 (Shadow).
// Alpha = ready for execution approval. Shadow = monitor only.
type SignalRoute string

const (
	Alpha  SignalRoute = "alpha"
	Shadow SignalRoute = "shadow"
)

// Signal is the final output of the 4-layer pipeline.
// Every L3 survivor becomes a Signal — either Alpha or Shadow.
type Signal struct {
	FilteredMarket
	Route         SignalRoute    `json:"route"`
	TargetSide    string         `json:"target_side"`    // "YES" or "NO" — which outcome to buy
	ShadowReasons []string       `json:"shadow_reasons"` // Why it was shadowed (empty for Alpha)
	VWAPResult    *VWAPResult    `json:"vwap_result,omitempty"`
	DepthResult   *TrueDepth     `json:"depth_result,omitempty"`

	// M1.4: Scoring fields
	ThetaMultiplier float64        `json:"theta_multiplier"` // 0-1 based on days to resolution
	Activity        ActivityStatus `json:"activity"`         // ACTIVE/NORMAL/SLOW/DYING
	DVRatio         float64        `json:"dv_ratio"`         // TrueDepth / Volume24h
	Score           float64        `json:"score"`            // Composite 0-100; Alpha signals sorted by this
}

// IsAlpha returns true if this signal is cleared for execution approval.
func (s Signal) IsAlpha() bool { return s.Route == Alpha }

// LiquidityTier represents the liquidity classification of a market
type LiquidityTier string

const (
	TierA    LiquidityTier = "A"    // High liquidity (>$50K)
	TierB    LiquidityTier = "B"    // Moderate liquidity ($5K-$50K)
	TierSkip LiquidityTier = "SKIP" // Too low liquidity (<$5K)
)

// MarketWithPrices extends polymarket.Market with parsed price data
type MarketWithPrices struct {
	Market   polymarket.Market
	YesPrice float64
	NoPrice  float64
}

// FilteredMarket represents a market that passed all L1-L3 filters
type FilteredMarket struct {
	Market        polymarket.Market
	YesPrice      float64
	NoPrice       float64
	InGoldenZone  bool
	Tier          LiquidityTier
	URL           string
	DaysToResolve int

	// Order book data (populated after L3, used by L4)
	YesTokenID    string  // CLOB token address for YES outcome
	NoTokenID     string  // CLOB token address for NO outcome
	BestBid       float64 // Best bid price
	BestAsk       float64 // Best ask price
	Spread        float64 // Absolute spread (ask - bid)
	SpreadPercent float64 // Spread as % of ask
	TrueDepthUSD  float64 // Actionable liquidity within ±2% of mid
	VWAP          float64 // Volume-weighted avg price for max stake
	SlippageUSD   float64 // VWAP - BestAsk (slippage cost)
}

// MarketURL returns the Polymarket URL for a market
func (fm FilteredMarket) MarketURL() string {
	return fmt.Sprintf("https://polymarket.com/market/%s", fm.Market.Slug)
}

// Rejection represents a market that was filtered out
type Rejection struct {
	MarketID   string    `json:"market_id"`
	Question   string    `json:"question"`
	Reason     string    `json:"reason"`
	Layer      string    `json:"layer"` // "L1", "L2", "L3"
	Timestamp  time.Time `json:"timestamp"`
	Category   string    `json:"category,omitempty"`
	YesPrice   float64   `json:"yes_price,omitempty"`
	NoPrice    float64   `json:"no_price,omitempty"`
	Liquidity  float64   `json:"liquidity,omitempty"`
	Volume24h  float64   `json:"volume_24h,omitempty"`
	DaysLeft   int       `json:"days_left,omitempty"`
}

// LayerResult tracks the output of a single filter layer
type LayerResult struct {
	LayerName    string
	Passed       int
	Rejected     int
	RejectCounts map[string]int // Reason -> Count
	Rejects      []Rejection
}

// PipelineResult contains the results of the full L1→L2→L3→L4 pipeline
type PipelineResult struct {
	TotalScanned int
	L1Result     LayerResult
	L2Result     LayerResult
	L3Result     LayerResult
	L4Result     LayerResult
	Passed       []FilteredMarket // L3 survivors (before L4 routing)
	Signals      []Signal         // L4 output: all L3 survivors as Alpha or Shadow
	AllRejects   []Rejection      // Combined rejects from L1-L3 (hard rejects)
}
