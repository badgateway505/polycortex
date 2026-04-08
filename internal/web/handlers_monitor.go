package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/badgateway/poly/internal/monitor"
	"github.com/badgateway/poly/internal/polymarket"
	"github.com/badgateway/poly/internal/scanner"
)

// POST /api/watch/{id} — add a market to the watchlist
func (s *Server) handleWatch(w http.ResponseWriter, r *http.Request) {
	marketID := r.PathValue("id")
	sig, ok := s.session.GetSignalByMarketID(marketID)
	if !ok {
		http.Error(w, "market not found in last scan", http.StatusNotFound)
		return
	}
	s.monitor.Watch(sig)
	writeJSON(w, map[string]any{"ok": true, "market_id": marketID})
}

// DELETE /api/watch/{id} — remove a market from the watchlist
func (s *Server) handleUnwatch(w http.ResponseWriter, r *http.Request) {
	marketID := r.PathValue("id")
	s.monitor.Unwatch(marketID)
	writeJSON(w, map[string]any{"ok": true, "market_id": marketID})
}

// GET /api/watch — list all watched markets
func (s *Server) handleWatchlist(w http.ResponseWriter, r *http.Request) {
	wms := s.monitor.Watchlist()

	type item struct {
		MarketID     string   `json:"market_id"`
		Question     string   `json:"question"`
		EnabledRules []string `json:"enabled_rules"`
		AddedAt      string   `json:"added_at"`
		LastPollAt   string   `json:"last_poll_at,omitempty"`
		CurrentPrice float64  `json:"current_price,omitempty"`
		TradeCount   int      `json:"trade_count,omitempty"`
	}
	out := make([]item, 0, len(wms))
	for _, wm := range wms {
		it := item{
			MarketID:     wm.MarketID,
			Question:     wm.Signal.Market.Question,
			EnabledRules: wm.EnabledRules,
			AddedAt:      wm.AddedAt.Format("2006-01-02T15:04:05Z"),
		}
		if state := s.monitor.State(wm.MarketID); state != nil {
			if !state.LastPollAt.IsZero() {
				it.LastPollAt = state.LastPollAt.Format("2006-01-02T15:04:05Z")
			}
			it.CurrentPrice = state.CurrentMidPrice()
			it.TradeCount = len(state.Trades)
		}
		out = append(out, it)
	}
	writeJSON(w, out)
}

// PATCH /api/watch/{id}/rules — enable/disable rules and set params
func (s *Server) handleSetRules(w http.ResponseWriter, r *http.Request) {
	marketID := r.PathValue("id")

	var body struct {
		EnabledRules []string                      `json:"enabled_rules"`
		Params       map[string]map[string]float64 `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if err := s.monitor.SetRules(marketID, body.EnabledRules, body.Params); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// GET /api/alerts — all recent alerts across all markets
func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.monitor.Alerts())
}

// GET /api/alerts/{id} — alerts for a specific market
func (s *Server) handleAlertsForMarket(w http.ResponseWriter, r *http.Request) {
	marketID := r.PathValue("id")
	writeJSON(w, s.monitor.AlertsForMarket(marketID))
}

// GET /api/rules — list all available rules with their parameters
func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	type paramResp struct {
		Key          string  `json:"key"`
		Label        string  `json:"label"`
		DefaultValue float64 `json:"default_value"`
		Unit         string  `json:"unit"`
		Min          float64 `json:"min"`
		Max          float64 `json:"max"`
	}
	type ruleResp struct {
		ID       string      `json:"id"`
		Name     string      `json:"name"`
		Category string      `json:"category"`
		Params   []paramResp `json:"params"`
	}

	rules := monitor.AllRules()
	out := make([]ruleResp, 0, len(rules))
	for _, rule := range rules {
		params := make([]paramResp, 0, len(rule.Params()))
		for _, p := range rule.Params() {
			params = append(params, paramResp{
				Key:          p.Key,
				Label:        p.Label,
				DefaultValue: p.DefaultValue,
				Unit:         p.Unit,
				Min:          p.Min,
				Max:          p.Max,
			})
		}
		out = append(out, ruleResp{
			ID:       rule.ID(),
			Name:     rule.Name(),
			Category: string(rule.Category()),
			Params:   params,
		})
	}
	writeJSON(w, out)
}

// ─── Live Metrics ─────────────────────────────────────────────────────────────

type liveMetric struct {
	Value     float64 `json:"value"`
	Passes    bool    `json:"passes"`
	Threshold string  `json:"threshold,omitempty"` // e.g. "<=3%", ">=$250"
}

type liveMetricsResp struct {
	Status       string     `json:"status"` // "ok" or "pending"
	FullDepthUSD liveMetric `json:"full_depth_usd"`
	TrueDepthUSD liveMetric `json:"true_depth_usd"`
	DVRatio      liveMetric `json:"dv_ratio"`
	SpreadPct    liveMetric `json:"spread_pct"`
	VWAP         liveMetric `json:"vwap"`
	BestBid      liveMetric `json:"best_bid"`
	BestAsk      liveMetric `json:"best_ask"`
	MidPrice     liveMetric `json:"mid_price"`
	UpdatedAt    string     `json:"updated_at"`
}

// GET /api/watch/{id}/live — live derived metrics from cached book snapshot
func (s *Server) handleLiveMetrics(w http.ResponseWriter, r *http.Request) {
	marketID := r.PathValue("id")
	state := s.monitor.State(marketID)
	if state == nil {
		http.Error(w, "market not watched", http.StatusNotFound)
		return
	}

	book := state.YesBook
	if book == nil {
		writeJSON(w, liveMetricsResp{Status: "pending"})
		return
	}

	cfg := s.cfg.Distribution

	fullDepth := sumDepth(book.BidLevels) + sumDepth(book.AskLevels)

	var trueDepthUSD float64
	if td, err := scanner.CalculateTrueDepthDefault(book); err == nil {
		trueDepthUSD = td.TotalDepthUSD
	}

	var dvRatio float64
	vol24h := state.Signal.Market.Volume24h
	if vol24h > 0 {
		dvRatio = trueDepthUSD / vol24h
	}

	stakeUSD := cfg.DefaultBalance * cfg.DefaultStakePct
	if stakeUSD <= 0 {
		stakeUSD = 50.0
	}
	var vwap float64
	if vr, err := scanner.CalculateVWAP(book, "BUY", stakeUSD); err == nil {
		vwap = vr.VWAP
	}

	resp := liveMetricsResp{
		Status:       "ok",
		FullDepthUSD: liveMetric{Value: fullDepth, Passes: fullDepth >= cfg.MinLiquidity, Threshold: fmt.Sprintf(">=$%.0f", cfg.MinLiquidity)},
		TrueDepthUSD: liveMetric{Value: trueDepthUSD, Passes: trueDepthUSD >= cfg.MinTrueDepthUSD, Threshold: fmt.Sprintf(">=$%.0f", cfg.MinTrueDepthUSD)},
		DVRatio:      liveMetric{Value: dvRatio, Passes: dvRatio >= 0.05, Threshold: ">=0.05"},
		SpreadPct:    liveMetric{Value: book.SpreadPercent, Passes: book.SpreadPercent <= cfg.MaxSpreadPct, Threshold: fmt.Sprintf("<=%.0f%%", cfg.MaxSpreadPct)},
		VWAP:         liveMetric{Value: vwap, Passes: vwap >= cfg.GoldenZoneMin && vwap <= cfg.GoldenZoneMax, Threshold: fmt.Sprintf("$%.2f-$%.2f", cfg.GoldenZoneMin, cfg.GoldenZoneMax)},
		BestBid:      liveMetric{Value: book.BestBid, Passes: true},
		BestAsk:      liveMetric{Value: book.BestAsk, Passes: true},
		MidPrice:     liveMetric{Value: book.MidPrice, Passes: true},
		UpdatedAt:    time.Now().Format(time.RFC3339),
	}
	writeJSON(w, resp)
}

func sumDepth(levels []polymarket.PriceLevel) float64 {
	var total float64
	for _, l := range levels {
		total += l.ValueUSD
	}
	return total
}

