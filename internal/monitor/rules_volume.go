package monitor

import (
	"fmt"
	"time"
)

func init() {
	allRules = append(allRules,
		&volumeSpike{},
		&volumeDirectional{},
		&volumeTotal{},
		&volumeSilence{},
		&volumeAcceleration{},
	)
}

// ─── volume_spike ─────────────────────────────────────────────────────────────

type volumeSpike struct{}

func (r *volumeSpike) ID() string             { return "volume_spike" }
func (r *volumeSpike) Name() string           { return "Volume Spike" }
func (r *volumeSpike) Category() RuleCategory { return CategoryVolume }
func (r *volumeSpike) Params() []RuleParam {
	return []RuleParam{
		{Key: "window_min", Label: "Window size", DefaultValue: 5, Unit: "minutes", Min: 1, Max: 60},
		{Key: "multiplier", Label: "Spike multiplier", DefaultValue: 3, Unit: "ratio", Min: 1.5, Max: 20},
	}
}

func (r *volumeSpike) Evaluate(state *MarketState, params map[string]float64) *Alert {
	window := time.Duration(params["window_min"]) * time.Minute
	mult := params["multiplier"]

	current := volumeInWindow(state, window)
	previous := volumeInWindow2(state, window, 2*window)

	if previous == 0 || current < 1 {
		return nil
	}
	ratio := current / previous
	if ratio >= mult {
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityWarning,
			Side:     "BOTH",
			Message:  fmt.Sprintf("Volume spike: $%.0f now vs $%.0f prev %.0f-min window (%.1f×)", current, previous, params["window_min"], ratio),
			Data:     map[string]any{"current_usd": current, "previous_usd": previous, "ratio": ratio},
		}
	}
	return nil
}

// ─── volume_directional ───────────────────────────────────────────────────────

type volumeDirectional struct{}

func (r *volumeDirectional) ID() string             { return "volume_directional" }
func (r *volumeDirectional) Name() string           { return "Directional Volume" }
func (r *volumeDirectional) Category() RuleCategory { return CategoryVolume }
func (r *volumeDirectional) Params() []RuleParam {
	return []RuleParam{
		{Key: "window_min", Label: "Lookback window", DefaultValue: 10, Unit: "minutes", Min: 1, Max: 60},
		{Key: "pct", Label: "Directional threshold", DefaultValue: 75, Unit: "percent", Min: 51, Max: 99},
	}
}

func (r *volumeDirectional) Evaluate(state *MarketState, params map[string]float64) *Alert {
	window := time.Duration(params["window_min"]) * time.Minute
	threshold := params["pct"] / 100.0

	trades := state.TradesInWindow(window)
	var yesVol, noVol float64
	for _, t := range trades {
		if t.Outcome == "YES" {
			yesVol += t.ValueUSD
		} else {
			noVol += t.ValueUSD
		}
	}
	total := yesVol + noVol
	if total < 50 {
		return nil // not enough volume to be meaningful
	}

	yesPct := yesVol / total
	noPct := noVol / total

	if yesPct >= threshold {
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityInfo,
			Side:     "YES",
			Message:  fmt.Sprintf("%.0f%% of volume in last %.0f min is YES ($%.0f/$%.0f)", yesPct*100, params["window_min"], yesVol, total),
			Data:     map[string]any{"yes_pct": yesPct, "yes_usd": yesVol, "total_usd": total},
		}
	}
	if noPct >= threshold {
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityInfo,
			Side:     "NO",
			Message:  fmt.Sprintf("%.0f%% of volume in last %.0f min is NO ($%.0f/$%.0f)", noPct*100, params["window_min"], noVol, total),
			Data:     map[string]any{"no_pct": noPct, "no_usd": noVol, "total_usd": total},
		}
	}
	return nil
}

// ─── volume_total ─────────────────────────────────────────────────────────────

type volumeTotal struct{}

func (r *volumeTotal) ID() string             { return "volume_total" }
func (r *volumeTotal) Name() string           { return "High Total Volume" }
func (r *volumeTotal) Category() RuleCategory { return CategoryVolume }
func (r *volumeTotal) Params() []RuleParam {
	return []RuleParam{
		{Key: "window_min", Label: "Lookback window", DefaultValue: 5, Unit: "minutes", Min: 1, Max: 60},
		{Key: "min_usd", Label: "Min total volume", DefaultValue: 1000, Unit: "USD", Min: 100, Max: 100000},
	}
}

func (r *volumeTotal) Evaluate(state *MarketState, params map[string]float64) *Alert {
	window := time.Duration(params["window_min"]) * time.Minute
	minUSD := params["min_usd"]

	vol := volumeInWindow(state, window)
	if vol >= minUSD {
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityInfo,
			Side:     "BOTH",
			Message:  fmt.Sprintf("$%.0f total volume in last %.0f min", vol, params["window_min"]),
			Data:     map[string]any{"volume_usd": vol},
		}
	}
	return nil
}

// ─── volume_silence ───────────────────────────────────────────────────────────

type volumeSilence struct{}

func (r *volumeSilence) ID() string             { return "volume_silence" }
func (r *volumeSilence) Name() string           { return "Volume Silence" }
func (r *volumeSilence) Category() RuleCategory { return CategoryVolume }
func (r *volumeSilence) Params() []RuleParam {
	return []RuleParam{
		{Key: "window_min", Label: "Silence window", DefaultValue: 15, Unit: "minutes", Min: 5, Max: 120},
		{Key: "max_usd", Label: "Max volume (silence threshold)", DefaultValue: 50, Unit: "USD", Min: 0, Max: 500},
	}
}

func (r *volumeSilence) Evaluate(state *MarketState, params map[string]float64) *Alert {
	window := time.Duration(params["window_min"]) * time.Minute
	maxUSD := params["max_usd"]

	// Only meaningful if we have history to compare against
	if len(state.Trades) < 5 {
		return nil
	}

	vol := volumeInWindow(state, window)
	if vol <= maxUSD {
		return &Alert{
			MarketID: state.MarketID,
			MarketQ:  state.Signal.Market.Question,
			RuleID:   r.ID(),
			RuleName: r.Name(),
			Severity: SeverityInfo,
			Side:     "BOTH",
			Message:  fmt.Sprintf("Market gone quiet: only $%.0f volume in last %.0f min", vol, params["window_min"]),
			Data:     map[string]any{"volume_usd": vol},
		}
	}
	return nil
}

// ─── volume_acceleration ──────────────────────────────────────────────────────

type volumeAcceleration struct{}

func (r *volumeAcceleration) ID() string             { return "volume_acceleration" }
func (r *volumeAcceleration) Name() string           { return "Volume Acceleration" }
func (r *volumeAcceleration) Category() RuleCategory { return CategoryVolume }
func (r *volumeAcceleration) Params() []RuleParam {
	return []RuleParam{
		{Key: "window_min", Label: "Window size", DefaultValue: 5, Unit: "minutes", Min: 1, Max: 30},
		{Key: "windows", Label: "Consecutive windows", DefaultValue: 3, Unit: "count", Min: 2, Max: 6},
	}
}

func (r *volumeAcceleration) Evaluate(state *MarketState, params map[string]float64) *Alert {
	winDur := time.Duration(params["window_min"]) * time.Minute
	numWindows := int(params["windows"])

	// Compute volume for each successive window going backward in time
	vols := make([]float64, numWindows)
	for i := 0; i < numWindows; i++ {
		start := time.Duration(i) * winDur
		end := time.Duration(i+1) * winDur
		vols[i] = volumeInWindow2(state, start, end)
	}

	// vols[0] = most recent window; check that each is larger than the next
	// i.e. vols[0] > vols[1] > vols[2]
	accelerating := true
	for i := 0; i < numWindows-1; i++ {
		if vols[i] <= vols[i+1] {
			accelerating = false
			break
		}
	}
	if !accelerating || vols[numWindows-1] == 0 {
		return nil
	}

	growthRate := vols[0] / vols[numWindows-1]
	return &Alert{
		MarketID: state.MarketID,
		MarketQ:  state.Signal.Market.Question,
		RuleID:   r.ID(),
		RuleName: r.Name(),
		Severity: SeverityWarning,
		Side:     "BOTH",
		Message:  fmt.Sprintf("Volume accelerating: %.1f× over %d consecutive %.0f-min windows", growthRate, numWindows, params["window_min"]),
		Data:     map[string]any{"windows": vols, "growth_rate": growthRate},
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// volumeInWindow sums ValueUSD for trades in the last d duration.
func volumeInWindow(state *MarketState, d time.Duration) float64 {
	var total float64
	for _, t := range state.TradesInWindow(d) {
		total += t.ValueUSD
	}
	return total
}

// volumeInWindow2 sums ValueUSD for trades between ago-end and ago-start from now.
// e.g. volumeInWindow2(state, 5*time.Minute, 10*time.Minute) = volume in the 5-10 min ago window.
func volumeInWindow2(state *MarketState, agoStart, agoEnd time.Duration) float64 {
	now := time.Now()
	start := now.Add(-agoEnd)
	end := now.Add(-agoStart)
	var total float64
	for _, t := range state.Trades {
		if t.Timestamp.After(start) && t.Timestamp.Before(end) {
			total += t.ValueUSD
		}
	}
	return total
}
