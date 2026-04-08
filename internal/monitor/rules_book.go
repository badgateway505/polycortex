package monitor

import (
	"fmt"

	"github.com/badgateway/poly/internal/polymarket"
)

func init() {
	allRules = append(allRules,
		&bookDepthDrop{},
		&bookWallAppeared{},
		&bookWallRemoved{},
		&bookImbalance{},
		&bookSpreadWiden{},
	)
}

// prevYesBook holds the YES book from the previous poll for comparison rules.
// State is stored per-market in MarketState; these rules read from YesBook and compare
// against a snapshot stored in MarketState.PrevYesBook (added below).

// ─── book_depth_drop ──────────────────────────────────────────────────────────

type bookDepthDrop struct{}

func (r *bookDepthDrop) ID() string             { return "book_depth_drop" }
func (r *bookDepthDrop) Name() string           { return "Book Depth Drop" }
func (r *bookDepthDrop) Category() RuleCategory { return CategoryBook }
func (r *bookDepthDrop) Params() []RuleParam {
	return []RuleParam{
		{Key: "pct", Label: "Depth drop threshold", DefaultValue: 30, Unit: "percent", Min: 10, Max: 90},
	}
}

func (r *bookDepthDrop) Evaluate(state *MarketState, params map[string]float64) *Alert {
	threshold := params["pct"] / 100.0
	if state.YesBook == nil || state.PrevYesBook == nil {
		return nil
	}
	prevDepth := bestBidDepth(state.PrevYesBook)
	currDepth := bestBidDepth(state.YesBook)
	if prevDepth == 0 {
		return nil
	}
	drop := (prevDepth - currDepth) / prevDepth
	if drop >= threshold {
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityWarning,
			Side:     "YES",
			Message:  fmt.Sprintf("Best bid depth dropped %.0f%% ($%.0f → $%.0f)", drop*100, prevDepth, currDepth),
			Data:     map[string]any{"prev_depth": prevDepth, "curr_depth": currDepth, "drop_pct": drop * 100},
		}
	}
	return nil
}

// ─── book_wall_appeared ───────────────────────────────────────────────────────

type bookWallAppeared struct{}

func (r *bookWallAppeared) ID() string             { return "book_wall_appeared" }
func (r *bookWallAppeared) Name() string           { return "Order Wall Appeared" }
func (r *bookWallAppeared) Category() RuleCategory { return CategoryBook }
func (r *bookWallAppeared) Params() []RuleParam {
	return []RuleParam{
		{Key: "min_usd", Label: "Min wall size", DefaultValue: 2000, Unit: "USD", Min: 500, Max: 50000},
	}
}

func (r *bookWallAppeared) Evaluate(state *MarketState, params map[string]float64) *Alert {
	minUSD := params["min_usd"]
	if state.YesBook == nil || state.PrevYesBook == nil {
		return nil
	}

	// Find large orders at best bid/ask that weren't there before
	currWalls := largeOrdersAt(state.YesBook, minUSD)
	prevWalls := largeOrdersAt(state.PrevYesBook, minUSD)

	for price, val := range currWalls {
		if _, existed := prevWalls[price]; !existed {
			return &Alert{
				MarketID: state.MarketID,
				MarketQ:  state.Signal.Market.Question,
				RuleID:   r.ID(),
				RuleName: r.Name(),
				Severity: SeverityWarning,
				Side:     "YES",
				Message:  fmt.Sprintf("Large order wall appeared: $%.0f at $%.3f", val, price),
				Data:     map[string]any{"price": price, "value_usd": val},
			}
		}
	}
	return nil
}

// ─── book_wall_removed ────────────────────────────────────────────────────────

type bookWallRemoved struct{}

func (r *bookWallRemoved) ID() string             { return "book_wall_removed" }
func (r *bookWallRemoved) Name() string           { return "Order Wall Removed" }
func (r *bookWallRemoved) Category() RuleCategory { return CategoryBook }
func (r *bookWallRemoved) Params() []RuleParam {
	return []RuleParam{
		{Key: "min_usd", Label: "Min wall size", DefaultValue: 2000, Unit: "USD", Min: 500, Max: 50000},
	}
}

func (r *bookWallRemoved) Evaluate(state *MarketState, params map[string]float64) *Alert {
	minUSD := params["min_usd"]
	if state.YesBook == nil || state.PrevYesBook == nil {
		return nil
	}

	prevWalls := largeOrdersAt(state.PrevYesBook, minUSD)
	currWalls := largeOrdersAt(state.YesBook, minUSD)

	for price, val := range prevWalls {
		if _, stillThere := currWalls[price]; !stillThere {
			return &Alert{
				MarketID: state.MarketID,
				MarketQ:  state.Signal.Market.Question,
				RuleID:   r.ID(),
				RuleName: r.Name(),
				Severity: SeverityWarning,
				Side:     "YES",
				Message:  fmt.Sprintf("Large order wall removed: $%.0f at $%.3f (spoofing or filled?)", val, price),
				Data:     map[string]any{"price": price, "value_usd": val},
			}
		}
	}
	return nil
}

// ─── book_imbalance ───────────────────────────────────────────────────────────

type bookImbalance struct{}

func (r *bookImbalance) ID() string             { return "book_imbalance" }
func (r *bookImbalance) Name() string           { return "Book Imbalance" }
func (r *bookImbalance) Category() RuleCategory { return CategoryBook }
func (r *bookImbalance) Params() []RuleParam {
	return []RuleParam{
		{Key: "ratio", Label: "Bid/ask depth ratio", DefaultValue: 3.0, Unit: "ratio", Min: 1.5, Max: 20},
	}
}

func (r *bookImbalance) Evaluate(state *MarketState, params map[string]float64) *Alert {
	ratioThresh := params["ratio"]
	if state.YesBook == nil {
		return nil
	}

	bidDepth := totalDepth(state.YesBook.BidLevels)
	askDepth := totalDepth(state.YesBook.AskLevels)
	if askDepth == 0 || bidDepth == 0 {
		return nil
	}

	ratio := bidDepth / askDepth
	if ratio >= ratioThresh {
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityInfo,
			Side:     "YES",
			Message:  fmt.Sprintf("Book imbalance: bid depth $%.0f is %.1f× ask depth $%.0f (buy pressure?)", bidDepth, ratio, askDepth),
			Data:     map[string]any{"bid_depth": bidDepth, "ask_depth": askDepth, "ratio": ratio},
		}
	}
	// Check inverse (ask >> bid = sell pressure)
	invRatio := askDepth / bidDepth
	if invRatio >= ratioThresh {
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityInfo,
			Side:     "YES",
			Message:  fmt.Sprintf("Book imbalance: ask depth $%.0f is %.1f× bid depth $%.0f (sell pressure?)", askDepth, invRatio, bidDepth),
			Data:     map[string]any{"bid_depth": bidDepth, "ask_depth": askDepth, "ratio": -invRatio},
		}
	}
	return nil
}

// ─── book_spread_widen ────────────────────────────────────────────────────────

type bookSpreadWiden struct{}

func (r *bookSpreadWiden) ID() string             { return "book_spread_widen" }
func (r *bookSpreadWiden) Name() string           { return "Spread Widening" }
func (r *bookSpreadWiden) Category() RuleCategory { return CategoryBook }
func (r *bookSpreadWiden) Params() []RuleParam {
	return []RuleParam{
		{Key: "pct", Label: "Spread increase threshold", DefaultValue: 50, Unit: "percent", Min: 10, Max: 200},
	}
}

func (r *bookSpreadWiden) Evaluate(state *MarketState, params map[string]float64) *Alert {
	threshold := params["pct"] / 100.0
	if state.YesBook == nil || state.PrevYesBook == nil {
		return nil
	}
	prevSpread := state.PrevYesBook.Spread
	currSpread := state.YesBook.Spread
	if prevSpread == 0 {
		return nil
	}
	increase := (currSpread - prevSpread) / prevSpread
	if increase >= threshold {
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityWarning,
			Side:     "YES",
			Message:  fmt.Sprintf("Spread widened %.0f%%: $%.3f → $%.3f (liquidity drying up?)", increase*100, prevSpread, currSpread),
			Data:     map[string]any{"prev_spread": prevSpread, "curr_spread": currSpread, "increase_pct": increase * 100},
		}
	}
	return nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func bestBidDepth(book *polymarket.BookSnapshot) float64 {
	if book == nil || len(book.BidLevels) == 0 {
		return 0
	}
	return book.BidLevels[0].ValueUSD
}

// largeOrdersAt returns a map of price→valueUSD for levels >= minUSD at best bid or ask.
func largeOrdersAt(book *polymarket.BookSnapshot, minUSD float64) map[float64]float64 {
	result := make(map[float64]float64)
	if book == nil {
		return result
	}
	for _, lvl := range book.BidLevels {
		if lvl.ValueUSD >= minUSD {
			result[lvl.Price] = lvl.ValueUSD
		}
	}
	for _, lvl := range book.AskLevels {
		if lvl.ValueUSD >= minUSD {
			result[lvl.Price] = lvl.ValueUSD
		}
	}
	return result
}

func totalDepth(levels []polymarket.PriceLevel) float64 {
	var total float64
	for _, l := range levels {
		total += l.ValueUSD
	}
	return total
}
