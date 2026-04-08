package web

import (
	"context"
	"net/http"
	"time"

	"github.com/badgateway/poly/internal/analysis"
)

// POST /api/grok/{id} — search X/Twitter for sentiment about a market
func (s *Server) handleGrok(w http.ResponseWriter, r *http.Request) {
	if s.grok == nil {
		http.Error(w, "Grok not configured (set GROK_API_KEY)", http.StatusServiceUnavailable)
		return
	}

	marketID := r.PathValue("id")
	sig, ok := s.session.GetSignalByMarketID(marketID)
	if !ok {
		http.Error(w, "market not found in last scan", http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	// Use the detected category label (e.g. "Crypto", "Politics") rather than the raw
	// Gamma category string — the detected label drives expert handle selection in the prompt.
	detectedCategory := analysis.DetectCategory(sig.Market.Question).Label()
	insight, err := s.grok.SearchXForMarket(ctx, sig.Market.Question, detectedCategory)
	if err != nil {
		s.logger.Warn("grok x_search failed", "market_id", marketID, "err", err)
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, insight)
}
