package monitor

import (
	"fmt"
	"time"
)

func init() {
	allRules = append(allRules,
		&whaleSingleTrade{},
		&whaleAccumulation{},
		&whaleExit{},
		&whaleRelative{},
	)
}

// ─── whale_single_trade ───────────────────────────────────────────────────────

type whaleSingleTrade struct{}

func (r *whaleSingleTrade) ID() string           { return "whale_single_trade" }
func (r *whaleSingleTrade) Name() string         { return "Whale Single Trade" }
func (r *whaleSingleTrade) Category() RuleCategory { return CategoryWhale }
func (r *whaleSingleTrade) Params() []RuleParam {
	return []RuleParam{
		{Key: "min_usd", Label: "Min trade value", DefaultValue: 500, Unit: "USD", Min: 50, Max: 50000},
	}
}

func (r *whaleSingleTrade) Evaluate(state *MarketState, params map[string]float64) *Alert {
	minUSD := params["min_usd"]
	// Check the most recent trade only — cooldown prevents re-firing on same trade.
	recent := state.TradesInWindow(1 * time.Minute)
	for _, t := range recent {
		if t.ValueUSD >= minUSD {
			sev := SeverityWarning
			if t.ValueUSD >= minUSD*3 {
				sev = SeverityAlert
			}
			return &Alert{
				MarketID: state.MarketID,
				MarketQ:  state.Signal.Market.Question,
				RuleID:   r.ID(),
				RuleName: r.Name(),
				Severity: sev,
				Side:     t.Outcome,
				Message:  fmt.Sprintf("$%.0f whale %s on %s @ $%.3f", t.ValueUSD, t.Side, t.Outcome, t.Price),
				Data: map[string]any{
					"trade_id":  t.ID,
					"value_usd": t.ValueUSD,
					"side":      t.Side,
					"outcome":   t.Outcome,
					"price":     t.Price,
					"size":      t.Size,
				},
			}
		}
	}
	return nil
}

// ─── whale_accumulation ───────────────────────────────────────────────────────

type whaleAccumulation struct{}

func (r *whaleAccumulation) ID() string           { return "whale_accumulation" }
func (r *whaleAccumulation) Name() string         { return "Whale Accumulation" }
func (r *whaleAccumulation) Category() RuleCategory { return CategoryWhale }
func (r *whaleAccumulation) Params() []RuleParam {
	return []RuleParam{
		{Key: "min_usd", Label: "Min per-trade value", DefaultValue: 200, Unit: "USD", Min: 50, Max: 10000},
		{Key: "min_count", Label: "Min trade count", DefaultValue: 3, Unit: "count", Min: 2, Max: 20},
		{Key: "window_min", Label: "Lookback window", DefaultValue: 10, Unit: "minutes", Min: 1, Max: 60},
	}
}

func (r *whaleAccumulation) Evaluate(state *MarketState, params map[string]float64) *Alert {
	minUSD := params["min_usd"]
	minCount := int(params["min_count"])
	window := time.Duration(params["window_min"]) * time.Minute

	trades := state.TradesInWindow(window)

	// Count large buys per outcome
	yesBuys, noBuys := 0, 0
	var yesVal, noVal float64
	for _, t := range trades {
		if t.Side == "BUY" && t.ValueUSD >= minUSD {
			if t.Outcome == "YES" {
				yesBuys++
				yesVal += t.ValueUSD
			} else {
				noBuys++
				noVal += t.ValueUSD
			}
		}
	}

	if yesBuys >= minCount {
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityWarning,
			Side:     "YES",
			Message:  fmt.Sprintf("%d whale buys on YES totalling $%.0f in %.0f min", yesBuys, yesVal, params["window_min"]),
			Data:     map[string]any{"count": yesBuys, "total_usd": yesVal, "outcome": "YES"},
		}
	}
	if noBuys >= minCount {
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityWarning,
			Side:     "NO",
			Message:  fmt.Sprintf("%d whale buys on NO totalling $%.0f in %.0f min", noBuys, noVal, params["window_min"]),
			Data:     map[string]any{"count": noBuys, "total_usd": noVal, "outcome": "NO"},
		}
	}
	return nil
}

// ─── whale_exit ───────────────────────────────────────────────────────────────

type whaleExit struct{}

func (r *whaleExit) ID() string           { return "whale_exit" }
func (r *whaleExit) Name() string         { return "Whale Exit" }
func (r *whaleExit) Category() RuleCategory { return CategoryWhale }
func (r *whaleExit) Params() []RuleParam {
	return []RuleParam{
		{Key: "min_usd", Label: "Min sell value", DefaultValue: 500, Unit: "USD", Min: 50, Max: 50000},
	}
}

func (r *whaleExit) Evaluate(state *MarketState, params map[string]float64) *Alert {
	minUSD := params["min_usd"]
	recent := state.TradesInWindow(1 * time.Minute)
	for _, t := range recent {
		if t.Side == "SELL" && t.ValueUSD >= minUSD {
			return &Alert{
				MarketID: state.MarketID,
				MarketQ:  state.Signal.Market.Question,
				RuleID:   r.ID(),
				RuleName: r.Name(),
				Severity: SeverityWarning,
				Side:     t.Outcome,
				Message:  fmt.Sprintf("$%.0f whale EXIT on %s @ $%.3f (position unwinding?)", t.ValueUSD, t.Outcome, t.Price),
				Data: map[string]any{
					"value_usd": t.ValueUSD,
					"outcome":   t.Outcome,
					"price":     t.Price,
				},
			}
		}
	}
	return nil
}

// ─── whale_relative ───────────────────────────────────────────────────────────

type whaleRelative struct{}

func (r *whaleRelative) ID() string           { return "whale_relative" }
func (r *whaleRelative) Name() string         { return "Whale Relative to Volume" }
func (r *whaleRelative) Category() RuleCategory { return CategoryWhale }
func (r *whaleRelative) Params() []RuleParam {
	return []RuleParam{
		{Key: "pct_of_hourly", Label: "% of hourly avg volume", DefaultValue: 200, Unit: "percent", Min: 50, Max: 1000},
	}
}

func (r *whaleRelative) Evaluate(state *MarketState, params map[string]float64) *Alert {
	pct := params["pct_of_hourly"] / 100.0

	// Hourly average = total volume last hour / 60 (per minute baseline)
	hourlyTrades := state.TradesInWindow(60 * time.Minute)
	if len(hourlyTrades) == 0 {
		return nil
	}
	var hourlyTotal float64
	for _, t := range hourlyTrades {
		hourlyTotal += t.ValueUSD
	}
	perMinuteAvg := hourlyTotal / 60.0
	threshold := perMinuteAvg * pct

	// Check if any single recent trade exceeds that threshold
	recent := state.TradesInWindow(1 * time.Minute)
	for _, t := range recent {
		if t.ValueUSD >= threshold && threshold > 0 {
			multiplier := t.ValueUSD / perMinuteAvg
			return &Alert{
				MarketID: state.MarketID,
				MarketQ:  state.Signal.Market.Question,
				RuleID:   r.ID(),
				RuleName: r.Name(),
				Severity: SeverityWarning,
				Side:     t.Outcome,
				Message:  fmt.Sprintf("$%.0f trade = %.1f× hourly avg/min on %s", t.ValueUSD, multiplier, t.Outcome),
				Data: map[string]any{
					"value_usd":       t.ValueUSD,
					"per_minute_avg":  perMinuteAvg,
					"multiplier":      multiplier,
					"outcome":         t.Outcome,
				},
			}
		}
	}
	return nil
}
