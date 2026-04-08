package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/badgateway/poly/internal/analysis"
	"github.com/badgateway/poly/internal/exa"
)

type newsResult struct {
	Queries *analysis.SearchQueries   `json:"queries"`
	Summary string                    `json:"summary,omitempty"`  // AI-distilled key facts
	Scored  []analysis.ScoredSnippet  `json:"scored,omitempty"`   // All results scored, filtered, sorted
	Tavily  *tavilySearchResult       `json:"tavily"`
	Exa     *exaSearchResult          `json:"exa"`
}

type tavilySearchResult struct {
	Answer  string                  `json:"answer,omitempty"`
	Results []analysis.TavilyResult `json:"results"`
	Error   string                  `json:"error,omitempty"`
}

type exaSearchResult struct {
	Results  []exa.Result              `json:"results"`
	Insights []analysis.ArticleInsight `json:"insights,omitempty"` // Per-article AI-extracted insights
	Error    string                    `json:"error,omitempty"`
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// POST /api/news/{id} — AI-generates optimal queries then runs Tavily + Exa in parallel.
// Single Haiku call determines queries; both searches fire concurrently.
func (s *Server) handleNews(w http.ResponseWriter, r *http.Request) {
	marketID := r.PathValue("id")
	sig, ok := s.session.GetSignalByMarketID(marketID)
	if !ok {
		writeError(w, http.StatusNotFound, "Signal not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Step 1: AI-generate search queries via Haiku (~$0.001)
	var queries *analysis.SearchQueries
	if s.queryGenerator != nil {
		var err error
		queries, err = s.queryGenerator.Generate(ctx, sig.Market.Question)
		if err != nil || queries == nil {
			queries = &analysis.SearchQueries{
				Tavily: sig.Market.Question,
				Exa:    sig.Market.Question,
			}
		}
	} else {
		queries = &analysis.SearchQueries{
			Tavily: sig.Market.Question,
			Exa:    sig.Market.Question,
		}
	}

	// Step 2: run Tavily + Exa in parallel
	type tavilyResult struct {
		data *tavilySearchResult
	}
	type exaRes struct {
		data *exaSearchResult
	}

	tavilyCh := make(chan tavilyResult, 1)
	exaCh := make(chan exaRes, 1)

	// Tavily goroutine
	go func() {
		res := &tavilySearchResult{}
		if s.tavily == nil {
			res.Error = "Tavily not configured (set TAVILY_API_KEY)"
			tavilyCh <- tavilyResult{res}
			return
		}
		cat := analysis.DetectCategory(sig.Market.Question)
		opts := analysis.CategorySearchOptions(cat)
		resp, err := s.tavily.Search(ctx, queries.Tavily, opts)
		if err != nil {
			res.Error = err.Error()
			tavilyCh <- tavilyResult{res}
			return
		}
		// Filter low-relevance results
		for _, r := range resp.Results {
			if r.Score >= 0.40 {
				res.Results = append(res.Results, r)
			}
		}
		res.Answer = resp.Answer
		s.session.SetTavily(marketID, &analysis.SearchContext{
			MarketID:  marketID,
			Query:     queries.Tavily,
			Answer:    resp.Answer,
			Results:   res.Results,
		})
		tavilyCh <- tavilyResult{res}
	}()

	// Exa goroutine
	go func() {
		res := &exaSearchResult{}
		if s.exa == nil {
			res.Error = "Exa not configured (set EXA_API_KEY)"
			exaCh <- exaRes{res}
			return
		}
		cat := analysis.DetectCategory(sig.Market.Question)
		domains := categoryExaDomains(cat)
		resp, err := s.exa.Search(ctx, queries.Exa, domains)
		if err != nil {
			res.Error = err.Error()
			exaCh <- exaRes{res}
			return
		}
		// Deduplicate by URL and by title — Exa often returns the same article
		// with slightly different URLs or date suffixes
		seenURL := make(map[string]bool)
		seenTitle := make(map[string]bool)
		for _, r := range resp.Results {
			normTitle := strings.ToLower(strings.TrimSpace(r.Title))
			// Strip trailing " - ESPN", " - BBC Sport" etc. for title comparison
			if idx := strings.LastIndex(normTitle, " - "); idx > 0 {
				normTitle = normTitle[:idx]
			}
			if !seenURL[r.URL] && !seenTitle[normTitle] {
				seenURL[r.URL] = true
				seenTitle[normTitle] = true
				res.Results = append(res.Results, r)
			}
		}
		resp.Results = res.Results
		s.session.SetExa(marketID, resp)
		exaCh <- exaRes{res}
	}()

	tavRes := <-tavilyCh
	exRes := <-exaCh

	cat := analysis.DetectCategory(sig.Market.Question)

	// Step 3: build unified scored snippets from both sources
	var allSnippets []analysis.ScoredSnippet
	var snippetTexts []string // for batch relevance scoring

	for _, r := range tavRes.data.Results {
		allSnippets = append(allSnippets, analysis.ScoredSnippet{
			Title:         r.Title,
			URL:           r.URL,
			Text:          r.Content,
			PublishedDate: r.PublishedDate,
			Source:        "tavily",
			RecencyWeight: analysis.RecencyMultiplier(r.PublishedDate),
			AuthorityBoost: analysis.AuthorityBoost(r.URL, cat),
		})
		snippetTexts = append(snippetTexts, r.Title+": "+r.Content)
	}
	for _, r := range exRes.data.Results {
		allSnippets = append(allSnippets, analysis.ScoredSnippet{
			Title:         r.Title,
			URL:           r.URL,
			Text:          r.Text,
			PublishedDate: r.PublishedDate,
			Source:        "exa",
			RecencyWeight: analysis.RecencyMultiplier(r.PublishedDate),
			AuthorityBoost: analysis.AuthorityBoost(r.URL, cat),
		})
		snippetTexts = append(snippetTexts, r.Title+": "+r.Text)
	}

	// Step 4: batch relevance scoring via Haiku + compute composite scores
	var scored []analysis.ScoredSnippet
	if len(allSnippets) > 0 && s.claudeAPIKey != "" {
		relevanceScores := analysis.BatchRelevanceScores(ctx, s.claudeAPIKey, sig.Market.Question, snippetTexts, s.logger)
		scored = analysis.ScoreResults(allSnippets, relevanceScores)
		s.logger.Info("content scoring complete",
			"market_id", marketID,
			"input", len(allSnippets),
			"scored", len(scored),
		)
	} else {
		scored = allSnippets
	}

	// Step 5: run digest and summary in parallel, using scored results
	summaryCh := make(chan string, 1)
	digestCh := make(chan []analysis.ArticleInsight, 1)

	// Sonnet summary — only scored (quality) content
	go func() {
		if s.queryGenerator == nil {
			summaryCh <- ""
			return
		}
		var snippets []string
		for _, r := range scored {
			if r.Text != "" {
				snippets = append(snippets, r.Title+": "+r.Text)
			}
		}
		summaryCh <- s.queryGenerator.Summarize(ctx, sig.Market.Question, snippets)
	}()

	// Haiku per-article digest — only Exa results that survived scoring
	go func() {
		if s.queryGenerator == nil {
			digestCh <- nil
			return
		}
		var articles []analysis.ArticleInput
		for _, r := range scored {
			if r.Source == "exa" {
				articles = append(articles, analysis.ArticleInput{Title: r.Title, Text: r.Text})
			}
		}
		if len(articles) == 0 {
			digestCh <- nil
			return
		}
		digestCh <- s.queryGenerator.DigestArticles(ctx, sig.Market.Question, articles)
	}()

	summary := <-summaryCh
	exRes.data.Insights = <-digestCh

	if summary != "" {
		s.session.SetNewsSummary(marketID, summary)
	}

	s.logger.Info("news search complete",
		"market_id", marketID,
		"tavily_results", len(tavRes.data.Results),
		"exa_results", len(exRes.data.Results),
		"scored_results", len(scored),
		"has_summary", summary != "",
	)

	writeJSON(w, newsResult{
		Queries: queries,
		Summary: summary,
		Scored:  scored,
		Tavily:  tavRes.data,
		Exa:     exRes.data,
	})
}
