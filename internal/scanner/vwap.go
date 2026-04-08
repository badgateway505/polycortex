package scanner

import (
	"fmt"

	"github.com/badgateway/poly/internal/polymarket"
)

// VWAPResult contains the volume-weighted average price calculation
// for a given order size (stake)
type VWAPResult struct {
	StakeUSD        float64 // Target order size in USD
	VWAP            float64 // Volume-weighted average fill price
	EffectiveFill   float64 // Alias for VWAP (clearer naming)
	SharesFilled    float64 // Total shares acquired
	BestPrice       float64 // Best available price (ask[0] for buys)
	Slippage        float64 // VWAP - BestPrice (positive = worse than best)
	SlippagePercent float64 // Slippage as % of best price
	LevelsCrossed   int     // Number of price levels consumed
	Sufficient      bool    // Was there enough liquidity?
}

// CalculateVWAP walks the order book to calculate the volume-weighted average
// price for a given stake size (in USD).
//
// This answers: "If I want to buy $50 worth, what's my real entry price?"
//
// For BUY orders:
//   - Walks the ASK side of the book (we're taking liquidity from sellers)
//   - Starts at best ask, works up through higher prices
//   - Calculates weighted average of all fills
//
// For SELL orders:
//   - Walks the BID side of the book (we're taking liquidity from buyers)
//   - Starts at best bid, works down through lower prices
//
// Example:
//
//	Ask book: [$0.28 × 100 shares = $28], [$0.281 × 200 shares = $56.20]
//	Want to buy: $50 USD
//
//	Fill:
//	  - $28 at $0.28 = 100 shares
//	  - $22 at $0.281 = 78.29 shares
//	  Total: 178.29 shares for $50
//	  VWAP = $50 / 178.29 = $0.2805
//	  Slippage = $0.2805 - $0.28 = $0.0005 (+0.18%)
//
// Returns error if:
//   - Order book is nil or empty
//   - Insufficient liquidity (can't fill full stake)
func CalculateVWAP(book *polymarket.BookSnapshot, side string, stakeUSD float64) (*VWAPResult, error) {
	if book == nil {
		return nil, fmt.Errorf("nil order book")
	}

	if stakeUSD <= 0 {
		return nil, fmt.Errorf("stake must be positive, got: %.2f", stakeUSD)
	}

	result := &VWAPResult{
		StakeUSD: stakeUSD,
	}

	var levels []polymarket.PriceLevel
	var bestPrice float64

	// Choose which side of the book to walk
	switch side {
	case "BUY":
		// Buy orders consume asks (sell-side liquidity)
		levels = book.AskLevels
		bestPrice = book.BestAsk
	case "SELL":
		// Sell orders consume bids (buy-side liquidity)
		levels = book.BidLevels
		bestPrice = book.BestBid
	default:
		return nil, fmt.Errorf("invalid side: %s (must be BUY or SELL)", side)
	}

	if len(levels) == 0 {
		return nil, fmt.Errorf("empty %s side of book", side)
	}

	result.BestPrice = bestPrice

	// Walk the order book, consuming liquidity
	remainingUSD := stakeUSD
	totalSharesAcquired := 0.0
	totalCostUSD := 0.0

	for i, level := range levels {
		if remainingUSD <= 0 {
			break
		}

		// How much USD is available at this level?
		availableUSD := level.ValueUSD

		if availableUSD <= 0 {
			continue // Skip empty levels
		}

		// How much can we fill at this level?
		fillUSD := min(remainingUSD, availableUSD)
		fillShares := fillUSD / level.Price

		totalSharesAcquired += fillShares
		totalCostUSD += fillUSD
		remainingUSD -= fillUSD
		result.LevelsCrossed = i + 1

		// Debug trace for first few levels
		if i < 3 {
			// Could add verbose logging here if needed
		}
	}

	// Did we fill the entire order?
	result.Sufficient = (remainingUSD <= 0.01) // Allow $0.01 rounding tolerance

	if totalSharesAcquired == 0 {
		return nil, fmt.Errorf("no liquidity available (filled 0 shares)")
	}

	// Calculate VWAP
	result.VWAP = totalCostUSD / totalSharesAcquired
	result.EffectiveFill = result.VWAP
	result.SharesFilled = totalSharesAcquired

	// Calculate slippage
	result.Slippage = result.VWAP - result.BestPrice
	if result.BestPrice > 0 {
		result.SlippagePercent = (result.Slippage / result.BestPrice) * 100.0
	}

	return result, nil
}

// CalculateVWAPForMaxStake is a convenience function that calculates VWAP
// using the configured max stake percentage of the user's balance.
//
// For MVP: max stake = 5% of $1,000 = $50
//
// This is what L4 (Distribution Engine) uses to determine if a market's
// VWAP falls in the Golden Zone.
func CalculateVWAPForMaxStake(book *polymarket.BookSnapshot, side string, balance float64, maxStakePct float64) (*VWAPResult, error) {
	maxStakeUSD := balance * maxStakePct
	return CalculateVWAP(book, side, maxStakeUSD)
}

// min returns the smaller of two float64 values
func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// EstimateFillQuality provides a qualitative assessment of the VWAP result
// Returns: "excellent", "good", "acceptable", "poor", "insufficient"
func (v *VWAPResult) FillQuality() string {
	if !v.Sufficient {
		return "insufficient" // Not enough liquidity
	}

	switch {
	case v.SlippagePercent <= 0.1:
		return "excellent" // <0.1% slippage
	case v.SlippagePercent <= 0.5:
		return "good" // 0.1-0.5% slippage
	case v.SlippagePercent <= 1.0:
		return "acceptable" // 0.5-1% slippage
	case v.SlippagePercent <= 2.0:
		return "poor" // 1-2% slippage
	default:
		return "very poor" // >2% slippage
	}
}

// String returns a human-readable summary of the VWAP calculation
func (v *VWAPResult) String() string {
	return fmt.Sprintf(
		"VWAP: $%.4f | Best: $%.4f | Slippage: $%.4f (+%.2f%%) | Shares: %.1f | Levels: %d | Quality: %s",
		v.VWAP,
		v.BestPrice,
		v.Slippage,
		v.SlippagePercent,
		v.SharesFilled,
		v.LevelsCrossed,
		v.FillQuality(),
	)
}
