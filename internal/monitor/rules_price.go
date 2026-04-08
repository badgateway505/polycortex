package monitor

import (
	"fmt"
	"math"
	"time"
)

func init() {
	allRules = append(allRules,
		&priceMoveUp{},
		&priceMoveDown{},
		&priceCross{},
		&priceReversal{},
		&priceStall{},
	)
}

// ─── price_move_up ────────────────────────────────────────────────────────────

type priceMoveUp struct{}

func (r *priceMoveUp) ID() string             { return "price_move_up" }
func (r *priceMoveUp) Name() string           { return "Price Move Up" }
func (r *priceMoveUp) Category() RuleCategory { return CategoryPrice }
func (r *priceMoveUp) Params() []RuleParam {
	return []RuleParam{
		{Key: "pct", Label: "Move threshold", DefaultValue: 3, Unit: "percent", Min: 0.5, Max: 50},
		{Key: "window_min", Label: "Lookback window", DefaultValue: 10, Unit: "minutes", Min: 1, Max: 120},
	}
}

func (r *priceMoveUp) Evaluate(state *MarketState, params map[string]float64) *Alert {
	threshold := params["pct"] / 100.0
	window := time.Duration(params["window_min"]) * time.Minute

	old, current, ok := priceChange(state, window)
	if !ok || old == 0 {
		return nil
	}
	change := (current - old) / old
	if change >= threshold {
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityWarning,
			Side:     "YES",
			Message:  fmt.Sprintf("Price up %.1f%% in %.0f min ($%.3f → $%.3f)", change*100, params["window_min"], old, current),
			Data:     map[string]any{"old_price": old, "current_price": current, "change_pct": change * 100},
		}
	}
	return nil
}

// ─── price_move_down ──────────────────────────────────────────────────────────

type priceMoveDown struct{}

func (r *priceMoveDown) ID() string             { return "price_move_down" }
func (r *priceMoveDown) Name() string           { return "Price Move Down" }
func (r *priceMoveDown) Category() RuleCategory { return CategoryPrice }
func (r *priceMoveDown) Params() []RuleParam {
	return []RuleParam{
		{Key: "pct", Label: "Drop threshold", DefaultValue: 3, Unit: "percent", Min: 0.5, Max: 50},
		{Key: "window_min", Label: "Lookback window", DefaultValue: 10, Unit: "minutes", Min: 1, Max: 120},
	}
}

func (r *priceMoveDown) Evaluate(state *MarketState, params map[string]float64) *Alert {
	threshold := params["pct"] / 100.0
	window := time.Duration(params["window_min"]) * time.Minute

	old, current, ok := priceChange(state, window)
	if !ok || old == 0 {
		return nil
	}
	change := (old - current) / old // positive when price fell
	if change >= threshold {
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityWarning,
			Side:     "YES",
			Message:  fmt.Sprintf("Price down %.1f%% in %.0f min ($%.3f → $%.3f)", change*100, params["window_min"], old, current),
			Data:     map[string]any{"old_price": old, "current_price": current, "change_pct": -change * 100},
		}
	}
	return nil
}

// ─── price_cross ──────────────────────────────────────────────────────────────

type priceCross struct{}

func (r *priceCross) ID() string             { return "price_cross" }
func (r *priceCross) Name() string           { return "Price Threshold Cross" }
func (r *priceCross) Category() RuleCategory { return CategoryPrice }
func (r *priceCross) Params() []RuleParam {
	return []RuleParam{
		{Key: "threshold", Label: "Price level", DefaultValue: 0.40, Unit: "USD", Min: 0.01, Max: 0.99},
	}
}

func (r *priceCross) Evaluate(state *MarketState, params map[string]float64) *Alert {
	threshold := params["threshold"]
	if len(state.PriceHistory) < 2 {
		return nil
	}
	prev := state.PriceHistory[len(state.PriceHistory)-2].MidPrice
	curr := state.PriceHistory[len(state.PriceHistory)-1].MidPrice

	crossed := (prev < threshold && curr >= threshold) || (prev > threshold && curr <= threshold)
	if !crossed {
		return nil
	}

	direction := "above"
	if curr < threshold {
		direction = "below"
	}
	return &Alert{
		MarketID: state.MarketID,
		MarketQ:  state.Signal.Market.Question,
		RuleID:   r.ID(),
		RuleName: r.Name(),
		Severity: SeverityAlert,
		Side:     "YES",
		Message:  fmt.Sprintf("Price crossed %s $%.2f threshold ($%.3f → $%.3f)", direction, threshold, prev, curr),
		Data:     map[string]any{"threshold": threshold, "prev_price": prev, "curr_price": curr, "direction": direction},
	}
}

// ─── price_reversal ───────────────────────────────────────────────────────────

type priceReversal struct{}

func (r *priceReversal) ID() string             { return "price_reversal" }
func (r *priceReversal) Name() string           { return "Price Reversal" }
func (r *priceReversal) Category() RuleCategory { return CategoryPrice }
func (r *priceReversal) Params() []RuleParam {
	return []RuleParam{
		{Key: "move_pct", Label: "Initial move", DefaultValue: 3, Unit: "percent", Min: 1, Max: 20},
		{Key: "reversal_pct", Label: "Reversal size", DefaultValue: 2, Unit: "percent", Min: 0.5, Max: 15},
		{Key: "window_min", Label: "Window", DefaultValue: 15, Unit: "minutes", Min: 5, Max: 60},
	}
}

func (r *priceReversal) Evaluate(state *MarketState, params map[string]float64) *Alert {
	movePct := params["move_pct"] / 100.0
	reversalPct := params["reversal_pct"] / 100.0
	window := time.Duration(params["window_min"]) * time.Minute

	pts := state.PricePointsInWindow(window)
	if len(pts) < 3 {
		return nil
	}

	// Find peak and trough within window
	var high, low float64
	high = pts[0].MidPrice
	low = pts[0].MidPrice
	for _, p := range pts {
		if p.MidPrice > high {
			high = p.MidPrice
		}
		if p.MidPrice < low {
			low = p.MidPrice
		}
	}
	curr := pts[len(pts)-1].MidPrice
	first := pts[0].MidPrice
	if first == 0 {
		return nil
	}

	totalSwing := (high - low) / first
	reversal := math.Abs(curr-high) / high

	if totalSwing >= movePct && reversal >= reversalPct {
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityWarning,
			Side:     "YES",
			Message:  fmt.Sprintf("Price reversal: %.1f%% swing, reversed %.1f%% from high in %.0f min", totalSwing*100, reversal*100, params["window_min"]),
			Data:     map[string]any{"high": high, "low": low, "current": curr, "swing_pct": totalSwing * 100, "reversal_pct": reversal * 100},
		}
	}
	return nil
}

// ─── price_stall ──────────────────────────────────────────────────────────────

type priceStall struct{}

func (r *priceStall) ID() string             { return "price_stall" }
func (r *priceStall) Name() string           { return "Price Stall" }
func (r *priceStall) Category() RuleCategory { return CategoryPrice }
func (r *priceStall) Params() []RuleParam {
	return []RuleParam{
		{Key: "max_change_pct", Label: "Max allowed change", DefaultValue: 0.5, Unit: "percent", Min: 0.1, Max: 5},
		{Key: "window_min", Label: "Stall window", DefaultValue: 20, Unit: "minutes", Min: 5, Max: 120},
	}
}

func (r *priceStall) Evaluate(state *MarketState, params map[string]float64) *Alert {
	maxChange := params["max_change_pct"] / 100.0
	window := time.Duration(params["window_min"]) * time.Minute

	// Need enough history to have data covering the full window
	if len(state.PriceHistory) < 5 {
		return nil
	}

	old, current, ok := priceChange(state, window)
	if !ok || old == 0 {
		return nil
	}
	change := math.Abs(current-old) / old
	if change <= maxChange {
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityInfo,
			Side:     "YES",
			Message:  fmt.Sprintf("Price stalling: only %.2f%% change in %.0f min (coiling for move?)", change*100, params["window_min"]),
			Data:     map[string]any{"price": current, "change_pct": change * 100},
		}
	}
	return nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// priceChange returns (oldestPrice, currentPrice, ok) for the given window.
func priceChange(state *MarketState, window time.Duration) (old, current float64, ok bool) {
	pts := state.PricePointsInWindow(window)
	if len(pts) < 2 {
		return 0, 0, false
	}
	// pts is newest-first from PricePointsInWindow
	current = pts[0].MidPrice
	old = pts[len(pts)-1].MidPrice
	return old, current, true
}
