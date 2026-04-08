package scanner

import (
	"log/slog"
	"strings"
	"time"

	"github.com/badgateway/poly/internal/config"
	"github.com/badgateway/poly/internal/polymarket"
)

// L1CategoryGate filters markets by category
// Purpose: Kill noise at the door. Only allow categories with fundamental analytical basis.
// Allowed: Politics, Crypto (fundamental), Business, Science, Global Affairs, Sports (rule-based)
// Excluded: Pop culture, memes, celebrity gossip, manipulation-prone markets
func L1CategoryGate(markets []polymarket.Market, cfg *config.Config, logger *slog.Logger) ([]polymarket.Market, LayerResult) {
	result := LayerResult{
		LayerName:    "L1_CATEGORY_GATE",
		RejectCounts: make(map[string]int),
		Rejects:      make([]Rejection, 0),
	}

	passed := make([]polymarket.Market, 0, len(markets))
	now := time.Now()

	for _, market := range markets {
		// Skip markets with no category data — let them through to be filtered later
		// Some markets legitimately don't have category metadata from the API
		if market.Category == "" {
			passed = append(passed, market)
			continue
		}

		// Check if category is excluded (noise)
		if isExcludedCategory(market.Category, cfg.Scanner.ExcludedCategories) {
			reason := "EXCLUDED_CATEGORY"
			result.RejectCounts[reason]++
			result.Rejected++
			result.Rejects = append(result.Rejects, Rejection{
				MarketID:  market.ID,
				Question:  market.Question,
				Reason:    reason,
				Layer:     "L1",
				Timestamp: now,
				Category:  market.Category,
				Liquidity: market.LiquidityNum,
				Volume24h: market.VolumeNum,
			})

			logger.Debug("L1: rejected",
				slog.String("market_id", market.ID),
				slog.String("category", market.Category),
				slog.String("reason", reason))
			continue
		}

		// Market passed L1
		passed = append(passed, market)
	}

	result.Passed = len(passed)

	logger.Info("L1 Category Gate complete",
		slog.Int("total", len(markets)),
		slog.Int("passed", result.Passed),
		slog.Int("rejected", result.Rejected))

	return passed, result
}

// isExcludedCategory checks if a category is in the excluded list
// Case-insensitive partial matching (e.g., "Pop Culture" matches "pop")
func isExcludedCategory(category string, excludedCategories []string) bool {
	categoryLower := strings.ToLower(category)
	for _, excluded := range excludedCategories {
		if strings.Contains(categoryLower, strings.ToLower(excluded)) {
			return true
		}
	}
	return false
}
