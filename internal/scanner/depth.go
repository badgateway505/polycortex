package scanner

import (
	"fmt"

	"github.com/badgateway/poly/internal/polymarket"
)

// TrueDepth represents the "actionable liquidity" in an order book.
//
// Unlike total liquidity (which can include orders far from mid-price),
// true depth measures only orders within a tight range (±2%) of the
// mid-price — the liquidity you can actually access without heavy slippage.
//
// Why this matters:
//   - High total liquidity + low true depth = "ghost liquidity" (orders are fake or too far away)
//   - High true depth = can execute at reasonable prices
//   - Depth imbalance shows buying/selling pressure
type TrueDepth struct {
	MidPrice       float64 // (BestBid + BestAsk) / 2
	Tolerance      float64 // ±2% by default (0.02)
	LowerBound     float64 // MidPrice × (1 - tolerance)
	UpperBound     float64 // MidPrice × (1 + tolerance)

	// Bid-side depth (buy orders within range)
	BidDepthUSD    float64 // Total USD value of bids within ±2%
	BidLevelsCount int     // Number of bid levels within range

	// Ask-side depth (sell orders within range)
	AskDepthUSD    float64 // Total USD value of asks within ±2%
	AskLevelsCount int     // Number of ask levels within range

	// Aggregate metrics
	TotalDepthUSD  float64 // BidDepth + AskDepth
	DepthImbalance float64 // (BidDepth - AskDepth) / TotalDepth
	                        // >0.2 = strong buy pressure
	                        // <-0.2 = strong sell pressure
	                        // -0.2 to 0.2 = balanced
}

// CalculateTrueDepth analyzes order book depth within a tolerance band
// around the mid-price.
//
// Default tolerance: ±2% (0.02)
//   - For a $0.30 mid-price: counts orders from $0.294 to $0.306
//   - Orders outside this range are ignored (too far to be useful)
//
// Returns:
//   - True depth in USD for both bid and ask sides
//   - Depth imbalance: positive = buy pressure, negative = sell pressure
//   - Level counts (how many price levels have liquidity in range)
//
// Example:
//
//	Mid: $0.30, Tolerance: 2%
//	Range: $0.294 - $0.306
//
//	Bids in range:
//	  $0.299 × 100 shares = $29.90
//	  $0.298 × 50 shares  = $14.90
//	  Total bid depth: $44.80
//
//	Asks in range:
//	  $0.301 × 80 shares  = $24.08
//	  $0.302 × 40 shares  = $12.08
//	  Total ask depth: $36.16
//
//	Total depth: $80.96
//	Imbalance: ($44.80 - $36.16) / $80.96 = +0.107 (slight buy pressure)
//
// Pro-tip: Compare `TrueDepth.TotalDepthUSD` vs `totalLiquidity` from Gamma API
// to detect "ghost liquidity" (high reported liquidity but thin execution depth).
func CalculateTrueDepth(book *polymarket.BookSnapshot, tolerance float64) (*TrueDepth, error) {
	if book == nil {
		return nil, fmt.Errorf("nil order book")
	}

	if len(book.BidLevels) == 0 || len(book.AskLevels) == 0 {
		return nil, fmt.Errorf("empty order book")
	}

	if tolerance <= 0 || tolerance > 0.5 {
		return nil, fmt.Errorf("tolerance must be between 0 and 0.5, got: %.4f", tolerance)
	}

	mid := book.MidPrice
	if mid <= 0 {
		return nil, fmt.Errorf("invalid mid-price: %.4f", mid)
	}

	result := &TrueDepth{
		MidPrice:   mid,
		Tolerance:  tolerance,
		LowerBound: mid * (1.0 - tolerance),
		UpperBound: mid * (1.0 + tolerance),
	}

	// Walk bid side (buy orders) — count liquidity near mid
	for _, level := range book.BidLevels {
		if level.Price >= result.LowerBound && level.Price <= result.UpperBound {
			result.BidDepthUSD += level.ValueUSD
			result.BidLevelsCount++
		}
	}

	// Walk ask side (sell orders) — count liquidity near mid
	for _, level := range book.AskLevels {
		if level.Price >= result.LowerBound && level.Price <= result.UpperBound {
			result.AskDepthUSD += level.ValueUSD
			result.AskLevelsCount++
		}
	}

	// Aggregate metrics
	result.TotalDepthUSD = result.BidDepthUSD + result.AskDepthUSD

	// Calculate imbalance
	if result.TotalDepthUSD > 0 {
		result.DepthImbalance = (result.BidDepthUSD - result.AskDepthUSD) / result.TotalDepthUSD
	}

	return result, nil
}

// CalculateTrueDepthDefault uses the standard ±2% tolerance
func CalculateTrueDepthDefault(book *polymarket.BookSnapshot) (*TrueDepth, error) {
	return CalculateTrueDepth(book, 0.02) // ±2% tolerance
}

// ImbalanceDirection returns a human-readable description of depth imbalance
func (td *TrueDepth) ImbalanceDirection() string {
	switch {
	case td.DepthImbalance > 0.2:
		return "strong buy pressure"
	case td.DepthImbalance > 0.05:
		return "slight buy pressure"
	case td.DepthImbalance < -0.2:
		return "strong sell pressure"
	case td.DepthImbalance < -0.05:
		return "slight sell pressure"
	default:
		return "balanced"
	}
}

// String returns a human-readable summary
func (td *TrueDepth) String() string {
	return fmt.Sprintf(
		"True Depth: $%.2f (bid: $%.2f, ask: $%.2f) | Imbalance: %+.1f%% (%s) | Range: $%.3f-$%.3f",
		td.TotalDepthUSD,
		td.BidDepthUSD,
		td.AskDepthUSD,
		td.DepthImbalance*100.0,
		td.ImbalanceDirection(),
		td.LowerBound,
		td.UpperBound,
	)
}

// LiquidityQuality represents the overall quality of order book liquidity
type LiquidityQuality struct {
	TotalLiquidity  float64 // From Gamma API (reported)
	TrueDepth       float64 // From CLOB book (actionable)
	Volume24h       float64 // From Gamma API

	// Key ratios
	DepthVolumeRatio float64 // TrueDepth / Volume24h
	DepthTotalRatio  float64 // TrueDepth / TotalLiquidity (ghost liquidity detector)

	// Quality flags
	IsHealthy        bool   // D/V > 0.05, Depth/Total > 0.1
	IsHypeFade       bool   // High volume, low depth (was hot, now thin)
	IsGhostLiquidity bool   // High total, low true depth (fake liquidity)

	Assessment       string // "excellent", "good", "acceptable", "poor", "dead"
}

// AssessLiquidityQuality combines Gamma (total liquidity, volume) and CLOB (true depth)
// to provide a comprehensive liquidity assessment.
//
// Key ratios:
//
//	Depth/Volume (D/V):
//	  >0.05 = healthy (depth = 5%+ of daily volume)
//	  0.01-0.05 = acceptable (can execute but watch slippage)
//	  <0.01 = illiquid (volume is there but depth isn't — hype fade)
//
//	Depth/Total (D/T):
//	  >0.1 = real liquidity (10%+ of reported liquidity is actionable)
//	  <0.05 = ghost liquidity (orders are fake or too far from price)
//
// Patterns:
//   - High volume, low D/V = "hype fade" (was hot yesterday, dead now)
//   - High total, low D/T = "ghost liquidity" (fake or far-away orders)
//   - Both ratios healthy = "excellent execution environment"
func AssessLiquidityQuality(totalLiquidity, volume24h float64, trueDepth *TrueDepth) *LiquidityQuality {
	lq := &LiquidityQuality{
		TotalLiquidity: totalLiquidity,
		TrueDepth:      trueDepth.TotalDepthUSD,
		Volume24h:      volume24h,
	}

	// Calculate ratios (handle division by zero)
	if volume24h > 0 {
		lq.DepthVolumeRatio = lq.TrueDepth / volume24h
	}
	if totalLiquidity > 0 {
		lq.DepthTotalRatio = lq.TrueDepth / totalLiquidity
	}

	// Detect patterns
	lq.IsHypeFade = (volume24h > 1000) && (lq.DepthVolumeRatio < 0.01)
	lq.IsGhostLiquidity = (totalLiquidity > 5000) && (lq.DepthTotalRatio < 0.05)
	lq.IsHealthy = (lq.DepthVolumeRatio > 0.05) && (lq.DepthTotalRatio > 0.1)

	// Overall assessment
	switch {
	case lq.IsHealthy && lq.DepthVolumeRatio > 0.1:
		lq.Assessment = "excellent"
	case lq.IsHealthy:
		lq.Assessment = "good"
	case lq.DepthVolumeRatio > 0.02 && lq.DepthTotalRatio > 0.05:
		lq.Assessment = "acceptable"
	case lq.IsHypeFade || lq.IsGhostLiquidity:
		lq.Assessment = "poor"
	default:
		lq.Assessment = "dead"
	}

	return lq
}

// String returns a human-readable summary
func (lq *LiquidityQuality) String() string {
	flags := ""
	if lq.IsHypeFade {
		flags += " [HYPE FADE]"
	}
	if lq.IsGhostLiquidity {
		flags += " [GHOST LIQUIDITY]"
	}
	if lq.IsHealthy {
		flags += " [HEALTHY]"
	}

	return fmt.Sprintf(
		"Liquidity: %s | D/V: %.3f | D/T: %.3f | Total: $%.0f | True: $%.0f | Vol24h: $%.0f%s",
		lq.Assessment,
		lq.DepthVolumeRatio,
		lq.DepthTotalRatio,
		lq.TotalLiquidity,
		lq.TrueDepth,
		lq.Volume24h,
		flags,
	)
}
