package web

import (
	"context"
	"net/http"
	"time"

	"github.com/badgateway/poly/internal/analysis"
)

// POST /api/perplexity/{id} — run Perplexity Sonar research for a market signal.
// Sends the already-built ResearchPrompt to Sonar, stores the result in session
// so the Claude Auditor prompt can reference it automatically.
func (s *Server) handlePerplexityResearch(w http.ResponseWriter, r *http.Request) {
	if s.perplexity == nil {
		writeError(w, http.StatusServiceUnavailable, "Perplexity not configured — set PPLX_API_KEY")
		return
	}

	marketID := r.PathValue("id")
	sig, ok := s.session.GetSignalByMarketID(marketID)
	if !ok {
		writeError(w, http.StatusNotFound, "Signal not found")
		return
	}

	prompt := analysis.ResearchPrompt(sig)

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	result, err := s.perplexity.Research(ctx, prompt, false)
	if err != nil {
		s.logger.Warn("perplexity research failed", "market_id", marketID, "err", err)
		writeError(w, http.StatusBadGateway, "Perplexity research failed: "+err.Error())
		return
	}

	// Store in session so the Claude Auditor prompt auto-fills its PERPLEXITY_OUTPUT section
	s.session.SetPaste("perplexity", marketID, result.FormatForSession())

	s.logger.Info("perplexity research complete",
		"market_id", marketID,
		"model", result.Model,
		"citations", len(result.Citations),
	)

	writeJSON(w, map[string]any{
		"text":      result.Text,
		"citations": result.Citations,
		"model":     result.Model,
	})
}
