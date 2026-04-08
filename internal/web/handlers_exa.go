package web

import (
	"context"
	"net/http"
	"time"

	"github.com/badgateway/poly/internal/analysis"
)

// categoryExaDomains returns specialist domain hints for Exa per market category.
// These are hints that steer results toward high-authority sources — not exclusive filters.
// Unknown domains still surface if they're semantically relevant.
func categoryExaDomains(cat analysis.MarketCategory) []string {
	switch cat {
	case analysis.CategoryPolitics:
		return []string{"538.com", "silverbulletin.com", "electionbettingodds.com", "reuters.com", "apnews.com"}
	case analysis.CategoryCrypto:
		return []string{"theblock.co", "messari.io", "glassnode.com", "coindesk.com", "delphi.xyz"}
	case analysis.CategorySportsLeague, analysis.CategorySportsQualify:
		return []string{"fbref.com", "transfermarkt.com", "understat.com", "espn.com", "bbc.com/sport"}
	case analysis.CategoryMacro:
		return []string{"federalreserve.gov", "bls.gov", "reuters.com", "bloomberg.com"}
	case analysis.CategoryCorporate:
		return []string{"sec.gov", "reuters.com", "ft.com", "bloomberg.com"}
	default:
		return nil
	}
}

// POST /api/exa/{id} — run Exa semantic search for a market signal.
// Exa uses neural+keyword hybrid search to surface expert analysis, research
// reports, and niche content that keyword-based Tavily misses.
func (s *Server) handleExa(w http.ResponseWriter, r *http.Request) {
	if s.exa == nil {
		writeError(w, http.StatusServiceUnavailable, "Exa not configured — set EXA_API_KEY")
		return
	}

	marketID := r.PathValue("id")
	sig, ok := s.session.GetSignalByMarketID(marketID)
	if !ok {
		writeError(w, http.StatusNotFound, "Signal not found")
		return
	}

	cat := analysis.DetectCategory(sig.Market.Question)
	domains := categoryExaDomains(cat)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	result, err := s.exa.Search(ctx, sig.Market.Question, domains)
	if err != nil {
		s.logger.Warn("exa search failed", "market_id", marketID, "err", err)
		writeError(w, http.StatusBadGateway, "Exa search failed: "+err.Error())
		return
	}

	s.session.SetExa(marketID, result)

	s.logger.Info("exa search complete",
		"market_id", marketID,
		"results", len(result.Results),
	)

	writeJSON(w, result)
}
