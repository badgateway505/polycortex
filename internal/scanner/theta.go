package scanner

// ActivityStatus represents the current trading activity level of a market.
type ActivityStatus string

const (
	ActivityActive ActivityStatus = "ACTIVE"  // >1.5× daily average volume today
	ActivityNormal ActivityStatus = "NORMAL"  // 0.75×-1.5×
	ActivitySlow   ActivityStatus = "SLOW"    // 0.25×-0.75× (fading)
	ActivityDying  ActivityStatus = "DYING"   // <0.25× (almost no activity)
)

// CalculateThetaDecay returns the time-value multiplier (0-1) based on days until resolution.
//
// Near-term markets score 1.0 (full weight), long-term markets score lower because:
//   - Capital is locked longer
//   - Forecasting accuracy degrades over time
//   - Less opportunity to rebalance / exit
//
// Uses the configured theta_decay schedule from config.yaml:
//   ≤7d:   1.00  — maximum urgency
//   ≤14d:  0.90
//   ≤30d:  0.75
//   ≤60d:  0.50
//   ≤180d: 0.25
//   >180d: 0.10  (default)
func CalculateThetaDecay(daysToResolve int, schedule map[string]float64) float64 {
	// Evaluate thresholds from tightest to loosest
	thresholds := []struct {
		key  string
		days int
	}{
		{"7d", 7},
		{"14d", 14},
		{"30d", 30},
		{"60d", 60},
		{"180d", 180},
	}

	for _, t := range thresholds {
		if daysToResolve <= t.days {
			if v, ok := schedule[t.key]; ok {
				return v
			}
		}
	}

	// Beyond all thresholds
	if v, ok := schedule["default"]; ok {
		return v
	}
	return 0.10
}

// CalculateActivityScore returns an activity status and score (0-1) based on trading volume.
//
// Since Polymarket's Gamma API doesn't expose a lastTradeTimestamp, we use the
// 24h-vs-weekly-average ratio as a proxy:
//
//	activityRatio = volume24h / (volume1wk / 7)
//
// Interpretation:
//   ≥1.5× daily average → ACTIVE (1.00)  — unusually busy today
//   ≥0.75×              → NORMAL (0.75)  — roughly on pace
//   ≥0.25×              → SLOW   (0.40)  — fading interest
//   <0.25×              → DYING  (0.10)  — nearly dormant today
func CalculateActivityScore(volume24h, volume1wk float64) (ActivityStatus, float64) {
	if volume1wk <= 0 {
		if volume24h > 0 {
			return ActivityActive, 1.0 // New market with no week history
		}
		return ActivityDying, 0.10
	}

	dailyAvg := volume1wk / 7.0
	if dailyAvg <= 0 {
		return ActivityDying, 0.10
	}

	ratio := volume24h / dailyAvg

	switch {
	case ratio >= 1.5:
		return ActivityActive, 1.00
	case ratio >= 0.75:
		return ActivityNormal, 0.75
	case ratio >= 0.25:
		return ActivitySlow, 0.40
	default:
		return ActivityDying, 0.10
	}
}

// CalculateCompositeScore produces a 0-100 ranking score for an Alpha signal.
//
// Formula: theta × dvScore × spreadScore × 100
//
// Components and rationale:
//   theta      — Time value multiplier (0-1). Near-term markets get full weight.
//   dvScore    — D/V ratio normalized to [0,1]. At D/V=5%+ (healthy threshold) = 1.0.
//                Penalizes ghost liquidity and hype-fade markets.
//   spreadScore — Cost penalty (0-1). Tighter spread = higher score.
//                 At max spread (3%) = 0.0. At zero spread = 1.0.
//
// Example:
//   7-day market, D/V=8%, spread=1%, maxSpread=3% → 1.0 × 1.0 × 0.67 × 100 = 67
//   30-day market, D/V=3%, spread=2%, maxSpread=3% → 0.75 × 0.6 × 0.33 × 100 = 15
func CalculateCompositeScore(theta, dvRatio, spreadPct, maxSpreadPct float64) float64 {
	// D/V score: 0 at D/V=0, 1.0 at D/V≥5% (healthy threshold)
	dvScore := dvRatio / 0.05
	if dvScore > 1.0 {
		dvScore = 1.0
	}
	if dvScore < 0 {
		dvScore = 0
	}

	// Spread score: 1.0 at zero spread, 0.0 at max spread
	var spreadScore float64
	if maxSpreadPct > 0 {
		spreadScore = 1.0 - (spreadPct / maxSpreadPct)
	}
	if spreadScore < 0 {
		spreadScore = 0
	}

	return theta * dvScore * spreadScore * 100.0
}
