package scanner

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/badgateway/poly/internal/config"
	"github.com/badgateway/poly/internal/polymarket"
)

// L2LivenessCheck filters markets for basic liveness requirements
// Purpose: Is this market alive and breathing?
// Checks: active==true, closed==false, endDate > now, liquidity > $1K, volume_24h > $500
func L2LivenessCheck(markets []polymarket.Market, cfg *config.Config, logger *slog.Logger) ([]polymarket.Market, LayerResult) {
	result := LayerResult{
		LayerName:    "L2_LIVENESS_CHECK",
		RejectCounts: make(map[string]int),
		Rejects:      make([]Rejection, 0),
	}

	passed := make([]polymarket.Market, 0, len(markets))
	now := time.Now()

	for _, market := range markets {
		var reason string

		// Check: market must be active
		if !market.Active {
			reason = "INACTIVE"
		} else if market.Closed {
			// Check: market must not be closed
			reason = "CLOSED"
		} else if market.EndDate.Before(now) {
			// Check: endDate must be in the future
			reason = "EXPIRED"
		} else if market.LiquidityNum < cfg.Liveness.MinLiquidity {
			// Check: minimum liquidity threshold
			reason = fmt.Sprintf("LOW_LIQUIDITY (<$%.0f)", cfg.Liveness.MinLiquidity)
		} else if market.Volume24h < cfg.Liveness.MinVolume24h {
			// Check: minimum 24h volume (avoid ghost markets)
			// Note: VolumeNum is total cumulative volume — must use Volume24h here
			reason = fmt.Sprintf("LOW_VOLUME (<$%.0f)", cfg.Liveness.MinVolume24h)
		}

		if reason != "" {
			result.RejectCounts[reason]++
			result.Rejected++
			result.Rejects = append(result.Rejects, Rejection{
				MarketID:  market.ID,
				Question:  market.Question,
				Reason:    reason,
				Layer:     "L2",
				Timestamp: now,
				Category:  market.Category,
				Liquidity: market.LiquidityNum,
				Volume24h: market.Volume24h,
			})

			logger.Debug("L2: rejected",
				slog.String("market_id", market.ID),
				slog.String("reason", reason),
				slog.Float64("liquidity", market.LiquidityNum),
				slog.Float64("volume_24h", market.Volume24h))
			continue
		}

		// Market passed L2
		passed = append(passed, market)
	}

	result.Passed = len(passed)

	logger.Info("L2 Liveness Check complete",
		slog.Int("total", len(markets)),
		slog.Int("passed", result.Passed),
		slog.Int("rejected", result.Rejected))

	return passed, result
}
