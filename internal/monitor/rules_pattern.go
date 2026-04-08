package monitor

import (
	"fmt"
	"time"
)

func init() {
	allRules = append(allRules,
		&patternConsecutive{},
		&patternIceberg{},
		&patternBracket{},
		&patternStopHunt{},
		&patternStepLadder{},
	)
}

// ─── pattern_consecutive ──────────────────────────────────────────────────────

type patternConsecutive struct{}

func (r *patternConsecutive) ID() string             { return "pattern_consecutive" }
func (r *patternConsecutive) Name() string           { return "Consecutive Same-Direction Trades" }
func (r *patternConsecutive) Category() RuleCategory { return CategoryPattern }
func (r *patternConsecutive) Params() []RuleParam {
	return []RuleParam{
		{Key: "min_count", Label: "Min consecutive count", DefaultValue: 5, Unit: "count", Min: 3, Max: 20},
	}
}

func (r *patternConsecutive) Evaluate(state *MarketState, params map[string]float64) *Alert {
	minCount := int(params["min_count"])
	recent := state.TradesInWindow(30 * time.Minute)
	if len(recent) < minCount {
		return nil
	}

	// recent[0] = newest; check streak at the front
	streak := 1
	dir := recent[0].Side + recent[0].Outcome // e.g. "BUYYES"
	for i := 1; i < len(recent); i++ {
		if recent[i].Side+recent[i].Outcome == dir {
			streak++
		} else {
			break
		}
	}

	if streak >= minCount {
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityWarning,
			Side:     recent[0].Outcome,
			Message:  fmt.Sprintf("%d consecutive %s %s trades (coordinated buying?)", streak, recent[0].Side, recent[0].Outcome),
			Data:     map[string]any{"streak": streak, "side": recent[0].Side, "outcome": recent[0].Outcome},
		}
	}
	return nil
}

// ─── pattern_iceberg ──────────────────────────────────────────────────────────

type patternIceberg struct{}

func (r *patternIceberg) ID() string             { return "pattern_iceberg" }
func (r *patternIceberg) Name() string           { return "Iceberg Order (Bot)" }
func (r *patternIceberg) Category() RuleCategory { return CategoryPattern }
func (r *patternIceberg) Params() []RuleParam {
	return []RuleParam{
		{Key: "min_usd", Label: "Min per-trade value", DefaultValue: 50, Unit: "USD", Min: 10, Max: 500},
		{Key: "max_usd", Label: "Max per-trade value", DefaultValue: 200, Unit: "USD", Min: 50, Max: 2000},
		{Key: "count", Label: "Min trade count", DefaultValue: 8, Unit: "count", Min: 4, Max: 30},
		{Key: "window_sec", Label: "Window", DefaultValue: 30, Unit: "seconds", Min: 10, Max: 300},
	}
}

func (r *patternIceberg) Evaluate(state *MarketState, params map[string]float64) *Alert {
	minUSD := params["min_usd"]
	maxUSD := params["max_usd"]
	minCount := int(params["count"])
	window := time.Duration(params["window_sec"]) * time.Second

	trades := state.TradesInWindow(window)
	var matched []Trade
	for _, t := range trades {
		if t.ValueUSD >= minUSD && t.ValueUSD <= maxUSD {
			matched = append(matched, t)
		}
	}

	if len(matched) >= minCount {
		var total float64
		for _, t := range matched {
			total += t.ValueUSD
		}
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityWarning,
			Side:     "BOTH",
			Message:  fmt.Sprintf("Iceberg pattern: %d small trades ($%.0f-$%.0f each) = $%.0f total in %.0f sec", len(matched), minUSD, maxUSD, total, params["window_sec"]),
			Data:     map[string]any{"count": len(matched), "total_usd": total, "min_usd": minUSD, "max_usd": maxUSD},
		}
	}
	return nil
}

// ─── pattern_bracket ──────────────────────────────────────────────────────────

type patternBracket struct{}

func (r *patternBracket) ID() string             { return "pattern_bracket" }
func (r *patternBracket) Name() string           { return "Bracket Trade (Hedging/Arb)" }
func (r *patternBracket) Category() RuleCategory { return CategoryPattern }
func (r *patternBracket) Params() []RuleParam {
	return []RuleParam{
		{Key: "min_usd", Label: "Min side value", DefaultValue: 300, Unit: "USD", Min: 50, Max: 10000},
		{Key: "window_sec", Label: "Window", DefaultValue: 60, Unit: "seconds", Min: 10, Max: 300},
	}
}

func (r *patternBracket) Evaluate(state *MarketState, params map[string]float64) *Alert {
	minUSD := params["min_usd"]
	window := time.Duration(params["window_sec"]) * time.Second

	trades := state.TradesInWindow(window)
	var yesBuyVal, noBuyVal float64
	for _, t := range trades {
		if t.Side == "BUY" {
			if t.Outcome == "YES" {
				yesBuyVal += t.ValueUSD
			} else {
				noBuyVal += t.ValueUSD
			}
		}
	}

	if yesBuyVal >= minUSD && noBuyVal >= minUSD {
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityInfo,
			Side:     "BOTH",
			Message:  fmt.Sprintf("Bracket trade: YES buy $%.0f + NO buy $%.0f in %.0f sec (hedging or arb?)", yesBuyVal, noBuyVal, params["window_sec"]),
			Data:     map[string]any{"yes_buy_usd": yesBuyVal, "no_buy_usd": noBuyVal},
		}
	}
	return nil
}

// ─── pattern_stop_hunt ────────────────────────────────────────────────────────

type patternStopHunt struct{}

func (r *patternStopHunt) ID() string             { return "pattern_stop_hunt" }
func (r *patternStopHunt) Name() string           { return "Stop Hunt" }
func (r *patternStopHunt) Category() RuleCategory { return CategoryPattern }
func (r *patternStopHunt) Params() []RuleParam {
	return []RuleParam{
		{Key: "move_pct", Label: "Spike/dip size", DefaultValue: 2, Unit: "percent", Min: 0.5, Max: 15},
		{Key: "reversal_sec", Label: "Reversal window", DefaultValue: 120, Unit: "seconds", Min: 30, Max: 600},
	}
}

func (r *patternStopHunt) Evaluate(state *MarketState, params map[string]float64) *Alert {
	movePct := params["move_pct"] / 100.0
	reversalWindow := time.Duration(params["reversal_sec"]) * time.Second

	pts := state.PricePointsInWindow(reversalWindow)
	if len(pts) < 4 {
		return nil
	}

	curr := pts[0].MidPrice
	// Find extreme (high or low) within window
	var extreme float64
	extreme = pts[0].MidPrice
	for _, p := range pts {
		if p.MidPrice > extreme {
			extreme = p.MidPrice
		}
	}
	startPrice := pts[len(pts)-1].MidPrice
	if startPrice == 0 {
		return nil
	}

	spike := (extreme - startPrice) / startPrice
	reversal := (extreme - curr) / extreme

	if spike >= movePct && reversal >= movePct*0.8 {
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityWarning,
			Side:     "YES",
			Message:  fmt.Sprintf("Stop hunt pattern: %.1f%% spike to $%.3f then %.1f%% reversal in %.0f sec", spike*100, extreme, reversal*100, params["reversal_sec"]),
			Data:     map[string]any{"start_price": startPrice, "extreme": extreme, "current": curr, "spike_pct": spike * 100, "reversal_pct": reversal * 100},
		}
	}
	return nil
}

// ─── pattern_step_ladder ─────────────────────────────────────────────────────

type patternStepLadder struct{}

func (r *patternStepLadder) ID() string             { return "pattern_step_ladder" }
func (r *patternStepLadder) Name() string           { return "Step Ladder (Climbing Buys)" }
func (r *patternStepLadder) Category() RuleCategory { return CategoryPattern }
func (r *patternStepLadder) Params() []RuleParam {
	return []RuleParam{
		{Key: "min_count", Label: "Min trades in sequence", DefaultValue: 4, Unit: "count", Min: 3, Max: 15},
		{Key: "window_min", Label: "Window", DefaultValue: 5, Unit: "minutes", Min: 1, Max: 30},
	}
}

func (r *patternStepLadder) Evaluate(state *MarketState, params map[string]float64) *Alert {
	minCount := int(params["min_count"])
	window := time.Duration(params["window_min"]) * time.Minute

	trades := state.TradesInWindow(window)
	if len(trades) < minCount {
		return nil
	}

	// trades are newest-first; reverse to check ascending price sequence
	buys := make([]Trade, 0)
	for _, t := range trades {
		if t.Side == "BUY" {
			buys = append(buys, t)
		}
	}
	if len(buys) < minCount {
		return nil
	}

	// Check if the last minCount buys each have strictly increasing price (oldest→newest)
	// buys[0] = newest, so check from the end
	sequence := buys[:minCount]
	ascending := true
	for i := 0; i < len(sequence)-1; i++ {
		// sequence[i] is newer, sequence[i+1] is older → newer price should be higher
		if sequence[i].Price <= sequence[i+1].Price {
			ascending = false
			break
		}
	}

	if ascending {
		priceRange := sequence[0].Price - sequence[minCount-1].Price
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityInfo,
			Side:     buys[0].Outcome,
			Message:  fmt.Sprintf("Step ladder: %d buys each at higher price ($%.3f → $%.3f) in %.0f min", minCount, sequence[minCount-1].Price, sequence[0].Price, params["window_min"]),
			Data:     map[string]any{"count": minCount, "price_range": priceRange, "low": sequence[minCount-1].Price, "high": sequence[0].Price},
		}
	}
	return nil
}
