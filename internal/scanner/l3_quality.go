package scanner

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/badgateway/poly/internal/config"
	"github.com/badgateway/poly/internal/polymarket"
)

// L3QualityGate filters markets for quality and analyzability
// Purpose: Is this market worth analyzing?
// Checks: horizon (3-30 days), resolution source, price parsing, Golden Zone
func L3QualityGate(markets []polymarket.Market, cfg *config.Config, logger *slog.Logger) ([]FilteredMarket, LayerResult) {
	result := LayerResult{
		LayerName:    "L3_QUALITY_GATE",
		RejectCounts: make(map[string]int),
		Rejects:      make([]Rejection, 0),
	}

	passed := make([]FilteredMarket, 0, len(markets))
	now := time.Now()

	for _, market := range markets {
		rejection := Rejection{
			MarketID:  market.ID,
			Question:  market.Question,
			Layer:     "L3",
			Timestamp: now,
			Category:  market.Category,
			Liquidity: market.LiquidityNum,
			Volume24h: market.VolumeNum,
		}

		// Parse outcome prices (stored as JSON string)
		var prices []string
		if err := json.Unmarshal([]byte(market.OutcomePrices), &prices); err != nil {
			rejection.Reason = "PARSE_ERROR"
			recordL3Reject(&result, rejection, logger)
			continue
		}

		if len(prices) != 2 {
			rejection.Reason = "INVALID_PRICES"
			recordL3Reject(&result, rejection, logger)
			continue
		}

		// Parse YES and NO prices
		var yesPrice, noPrice float64
		if _, err := fmt.Sscanf(prices[0], "%f", &yesPrice); err != nil {
			rejection.Reason = "PARSE_ERROR"
			recordL3Reject(&result, rejection, logger)
			continue
		}
		if _, err := fmt.Sscanf(prices[1], "%f", &noPrice); err != nil {
			rejection.Reason = "PARSE_ERROR"
			recordL3Reject(&result, rejection, logger)
			continue
		}

		rejection.YesPrice = yesPrice
		rejection.NoPrice = noPrice

		// Calculate days to resolution
		daysToResolve := int(time.Until(market.EndDate).Hours() / 24)
		rejection.DaysLeft = daysToResolve

		// Horizon filter: 3-30 days to resolution
		if daysToResolve < cfg.Quality.HorizonMinDays {
			rejection.Reason = fmt.Sprintf("HORIZON_TOO_SHORT (<%dd)", cfg.Quality.HorizonMinDays)
			recordL3Reject(&result, rejection, logger)
			continue
		}
		if daysToResolve > cfg.Quality.HorizonMaxDays {
			rejection.Reason = fmt.Sprintf("HORIZON_TOO_LONG (>%dd)", cfg.Quality.HorizonMaxDays)
			recordL3Reject(&result, rejection, logger)
			continue
		}

		// Price filter: check if YES or NO is in Golden Zone
		// Note: This uses last_trade_price for now. L4 will recalculate with VWAP.
		inGoldenZone := (yesPrice >= cfg.GoldenZone.Min && yesPrice <= cfg.GoldenZone.Max) ||
			(noPrice >= cfg.GoldenZone.Min && noPrice <= cfg.GoldenZone.Max)

		if !inGoldenZone {
			rejection.Reason = "OUTSIDE_GOLDEN_ZONE"
			recordL3Reject(&result, rejection, logger)
			continue
		}

		// Liquidity tiering (basic check — L4 will refine with depth analysis)
		tier := classifyLiquidity(market.LiquidityNum, cfg)
		if tier == TierSkip {
			rejection.Reason = fmt.Sprintf("LOW_LIQUIDITY (<$%.0f)", cfg.LiquidityTiers.TierBMin)
			recordL3Reject(&result, rejection, logger)
			continue
		}

		// Market passed L3
		filtered := FilteredMarket{
			Market:        market,
			YesPrice:      yesPrice,
			NoPrice:       noPrice,
			InGoldenZone:  true,
			Tier:          tier,
			URL:           fmt.Sprintf("https://polymarket.com/market/%s", market.Slug),
			DaysToResolve: daysToResolve,
		}

		passed = append(passed, filtered)

		logger.Debug("L3: passed",
			slog.String("market_id", market.ID),
			slog.Float64("yes_price", yesPrice),
			slog.Float64("no_price", noPrice),
			slog.Int("days_to_resolve", daysToResolve),
			slog.String("tier", string(tier)))
	}

	result.Passed = len(passed)

	logger.Info("L3 Quality Gate complete",
		slog.Int("total", len(markets)),
		slog.Int("passed", result.Passed),
		slog.Int("rejected", result.Rejected))

	return passed, result
}

// recordL3Reject logs a rejection for L3
func recordL3Reject(result *LayerResult, rejection Rejection, logger *slog.Logger) {
	result.RejectCounts[rejection.Reason]++
	result.Rejected++
	result.Rejects = append(result.Rejects, rejection)

	logger.Debug("L3: rejected",
		slog.String("market_id", rejection.MarketID),
		slog.String("reason", rejection.Reason))
}

// classifyLiquidity determines the liquidity tier for a market
func classifyLiquidity(liquidity float64, cfg *config.Config) LiquidityTier {
	if liquidity >= cfg.LiquidityTiers.TierAMin {
		return TierA
	}
	if liquidity >= cfg.LiquidityTiers.TierBMin {
		return TierB
	}
	return TierSkip
}
