package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/badgateway/poly/internal/analysis"
	"github.com/badgateway/poly/internal/exa"
	"github.com/badgateway/poly/internal/polymarket"
	"github.com/badgateway/poly/internal/scanner"
)

const (
	promptTemplatePath  = "templates/pillarlab_prompt.md"
	auditorTemplatePath = "templates/claude_auditor_prompt.md"
	maxScanLimit        = 40000
)

// --- Scan ---

type scanRequest struct {
	Limit int `json:"limit"`
}

type scanResponse struct {
	TotalScanned int              `json:"total_scanned"`
	Alphas       []signalResponse `json:"alphas"`
	Shadows      []signalResponse `json:"shadows"`
	Duration     string           `json:"duration"`
}

type signalResponse struct {
	MarketID      string  `json:"market_id"`
	Question      string  `json:"question"`
	TargetSide    string  `json:"target_side"`
	Price         float64 `json:"price"`
	YesPrice      float64 `json:"yes_price"`
	NoPrice       float64 `json:"no_price"`
	Score         float64 `json:"score"`
	Theta         float64 `json:"theta"`
	DaysToResolve int     `json:"days_to_resolve"`
	Tier          string  `json:"tier"`
	Activity      string  `json:"activity"`
	DVRatio       float64 `json:"dv_ratio"`
	SpreadPct     float64 `json:"spread_pct"`
	VWAP          float64 `json:"vwap"`
	TrueDepthUSD  float64 `json:"true_depth_usd"`
	Liquidity     float64 `json:"liquidity"`
	URL           string  `json:"url"`
	Category      string  `json:"category"`
	IsAlpha       bool    `json:"is_alpha"`
	ShadowReasons []string `json:"shadow_reasons,omitempty"`
	// State indicators for the UI
	HasTavily          bool `json:"has_tavily"`
	HasExa             bool `json:"has_exa"`
	HasPerplexity      bool `json:"has_perplexity"`
	HasPillarlab       bool `json:"has_pillarlab"`
	HasConditionParser  bool `json:"has_condition_parser"`
	PillarlabEnabled    bool `json:"pillarlab_enabled"`
}

func signalToResponse(sig scanner.Signal, s *Session, pillarlabEnabled bool) signalResponse {
	price := sig.YesPrice
	if sig.TargetSide == "NO" {
		price = sig.NoPrice
	}
	tier := "B"
	if sig.Tier == scanner.TierA {
		tier = "A"
	}
	cat := analysis.DetectCategory(sig.Market.Question)

	return signalResponse{
		MarketID:      sig.Market.ID,
		Question:      sig.Market.Question,
		TargetSide:    sig.TargetSide,
		Price:         price,
		YesPrice:      sig.YesPrice,
		NoPrice:       sig.NoPrice,
		Score:         sig.Score,
		Theta:         sig.ThetaMultiplier,
		DaysToResolve: sig.DaysToResolve,
		Tier:          tier,
		Activity:      string(sig.Activity),
		DVRatio:       sig.DVRatio,
		SpreadPct:     sig.SpreadPercent,
		VWAP:          sig.FilteredMarket.VWAP,
		TrueDepthUSD:  sig.FilteredMarket.TrueDepthUSD,
		Liquidity:     sig.Market.LiquidityNum,
		URL:           sig.URL,
		Category:      cat.Label(),
		IsAlpha:       sig.IsAlpha(),
		ShadowReasons: sig.ShadowReasons,
		HasTavily:          s.GetTavily(sig.Market.ID) != nil,
		HasExa:             s.GetExa(sig.Market.ID) != nil,
		HasPerplexity:      s.GetPaste("perplexity", sig.Market.ID) != "",
		HasPillarlab:       s.GetPaste("pillarlab", sig.Market.ID) != "",
		HasConditionParser:  s.GetCondition(sig.Market.ID) != nil,
		PillarlabEnabled:    pillarlabEnabled,
	}
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	var req scanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.Limit = 500
	}
	if req.Limit <= 0 {
		req.Limit = 500
	}
	if req.Limit > maxScanLimit {
		req.Limit = maxScanLimit
	}

	s.logger.Info("Web scan starting", slog.Int("limit", req.Limit))
	start := time.Now()

	gammaClient := polymarket.NewGammaClient(s.logger)
	clobClient := polymarket.NewCLOBClient()

	markets, err := gammaClient.FetchMarketsLimit(req.Limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Failed to fetch markets: "+err.Error())
		return
	}

	result := scanner.RunPipeline(markets, s.cfg, clobClient, s.logger)
	duration := time.Since(start)

	s.session.SetResult(&result)

	resp := scanResponse{
		TotalScanned: result.TotalScanned,
		Duration:     fmt.Sprintf("%.1fs", duration.Seconds()),
	}

	for _, sig := range result.Signals {
		sr := signalToResponse(sig, s.session, s.cfg.Analysis.PillarlabEnabled)
		if sig.IsAlpha() {
			resp.Alphas = append(resp.Alphas, sr)
		} else {
			resp.Shadows = append(resp.Shadows, sr)
		}
	}

	s.logger.Info("Web scan complete",
		slog.Int("alphas", len(resp.Alphas)),
		slog.Int("shadows", len(resp.Shadows)),
		slog.String("duration", resp.Duration))

	writeJSON(w, resp)
}

// --- Signals ---

func (s *Server) handleGetSignals(w http.ResponseWriter, r *http.Request) {
	result := s.session.GetResult()
	if result == nil {
		writeJSON(w, scanResponse{})
		return
	}

	var resp scanResponse
	resp.TotalScanned = result.TotalScanned
	for _, sig := range result.Signals {
		sr := signalToResponse(sig, s.session, s.cfg.Analysis.PillarlabEnabled)
		if sig.IsAlpha() {
			resp.Alphas = append(resp.Alphas, sr)
		} else {
			resp.Shadows = append(resp.Shadows, sr)
		}
	}
	writeJSON(w, resp)
}

// --- Tavily ---

func (s *Server) handleTavily(w http.ResponseWriter, r *http.Request) {
	marketID := r.PathValue("id")
	sig, ok := s.session.GetSignalByMarketID(marketID)
	if !ok {
		writeError(w, http.StatusNotFound, "Signal not found")
		return
	}

	if s.tavily == nil {
		writeError(w, http.StatusServiceUnavailable, "Tavily not configured — set TAVILY_API_KEY")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	sc, err := s.tavily.SearchForSignal(ctx, sig)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Tavily search failed: "+err.Error())
		return
	}

	s.session.SetTavily(marketID, sc)
	writeJSON(w, sc)
}

// --- Condition Parser ---

func (s *Server) handleConditionParser(w http.ResponseWriter, r *http.Request) {
	marketID := r.PathValue("id")
	sig, ok := s.session.GetSignalByMarketID(marketID)
	if !ok {
		writeError(w, http.StatusNotFound, "Signal not found")
		return
	}

	if s.conditionParser == nil {
		writeError(w, http.StatusServiceUnavailable, "Condition parser not configured — set CLAUDE_API_KEY")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	condition, err := s.conditionParser.ParseConditions(ctx, sig)
	if err != nil {
		s.logger.Warn("Condition parsing failed",
			slog.String("market_id", marketID),
			slog.String("error", err.Error()))
		writeError(w, http.StatusBadGateway, "Condition parsing failed: "+err.Error())
		return
	}

	s.session.SetCondition(marketID, condition)
	writeJSON(w, condition)
}

// --- Prompts ---

func (s *Server) handlePerplexityPrompt(w http.ResponseWriter, r *http.Request) {
	sig, ok := s.session.GetSignalByMarketID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Signal not found")
		return
	}

	cond := s.session.GetCondition(sig.Market.ID)
	news := s.session.GetNewsSummary(sig.Market.ID)
	prompt := analysis.ResearchPromptWithConditions(sig, cond, news)
	s.session.SetPrompt(sig.Market.ID, "perplexity", prompt)
	writeJSON(w, map[string]string{"prompt": prompt})
}

func (s *Server) handlePillarlabPrompt(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Analysis.PillarlabEnabled {
		writeError(w, http.StatusGone, "PillarLab is disabled in config (analysis.pillarlab_enabled: false)")
		return
	}

	sig, ok := s.session.GetSignalByMarketID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Signal not found")
		return
	}

	tmpl, err := os.ReadFile(promptTemplatePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load PillarLab template")
		return
	}

	cond := s.session.GetCondition(sig.Market.ID)
	news := s.session.GetNewsSummary(sig.Market.ID)
	prompt := analysis.GenerateSinglePromptWithConditions(string(tmpl), sig, cond, news)
	s.session.SetPrompt(sig.Market.ID, "pillarlab", prompt)
	writeJSON(w, map[string]string{"prompt": prompt})
}

func (s *Server) handleAuditorPrompt(w http.ResponseWriter, r *http.Request) {
	marketID := r.PathValue("id")
	sig, ok := s.session.GetSignalByMarketID(marketID)
	if !ok {
		writeError(w, http.StatusNotFound, "Signal not found")
		return
	}

	tmpl, err := os.ReadFile(auditorTemplatePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load auditor template")
		return
	}

	// Auto-fill from session state
	var tavilyContext string
	if sc := s.session.GetTavily(marketID); sc != nil {
		tavilyContext = sc.FormatForClaude()
	}
	// Prepend news summary to tavily context so auditor gets the AI-distilled facts
	if news := s.session.GetNewsSummary(marketID); news != "" {
		tavilyContext = "=== KEY FACTS (AI-summarised from Tavily + Exa) ===\n" + news + "\n\n" + tavilyContext
	}
	perplexity := s.session.GetPaste("perplexity", marketID)
	pillarlab := ""
	if s.cfg.Analysis.PillarlabEnabled {
		pillarlab = s.session.GetPaste("pillarlab", marketID)
	}

	cond := s.session.GetCondition(marketID)
	prompt := analysis.GenerateAuditorPromptWithConditions(string(tmpl), sig, tavilyContext, perplexity, pillarlab, cond)
	s.session.SetPrompt(marketID, "auditor", prompt)
	writeJSON(w, map[string]string{"prompt": prompt})
}

// --- Paste ---

func (s *Server) handleTogglePillarlab(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	s.cfg.Analysis.PillarlabEnabled = req.Enabled
	s.logger.Info("PillarLab toggled", slog.Bool("enabled", req.Enabled))
	writeJSON(w, map[string]bool{"pillarlab_enabled": req.Enabled})
}

func (s *Server) handlePaste(w http.ResponseWriter, r *http.Request) {
	source := r.PathValue("source")
	marketID := r.PathValue("id")

	if source != "perplexity" && source != "pillarlab" {
		writeError(w, http.StatusBadRequest, "Source must be 'perplexity' or 'pillarlab'")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to read body")
		return
	}

	// Accept both raw text and JSON {"data": "..."}
	data := string(body)
	var jsonBody struct {
		Data string `json:"data"`
	}
	if json.Unmarshal(body, &jsonBody) == nil && jsonBody.Data != "" {
		data = jsonBody.Data
	}

	s.session.SetPaste(source, marketID, data)
	writeJSON(w, map[string]string{"status": "saved", "source": source, "market_id": marketID})
}

func (s *Server) handleGetPaste(w http.ResponseWriter, r *http.Request) {
	source := r.PathValue("source")
	marketID := r.PathValue("id")
	data := s.session.GetPaste(source, marketID)
	writeJSON(w, map[string]string{"data": data})
}

// --- Import ---

type importResponse struct {
	Matched   []matchedResponse `json:"matched"`
	Unmatched []string          `json:"unmatched,omitempty"`
	Warnings  []string          `json:"warnings,omitempty"`
}

type matchedResponse struct {
	MarketID            string                       `json:"market_id"`
	Question            string                       `json:"question"`
	OurSide             string                       `json:"our_side"`
	OurPrice            float64                      `json:"our_price"`
	TrueProb            float64                      `json:"true_prob"`
	Edge                float64                      `json:"edge"`
	EdgePct             float64                      `json:"edge_pct"`
	Confidence          string                       `json:"confidence"`
	Reasoning           string                       `json:"reasoning"`
	UncertaintySources  []analysis.UncertaintySource `json:"uncertainty_sources,omitempty"`
}

// --- Deep Research ---

type researchResponse struct {
	MarketID string           `json:"market_id"`
	Results  []ResearchResult `json:"results"`
	Cost     string           `json:"cost"` // Estimated Tavily cost
}

// handleResearch runs Tavily searches for each uncertainty source from the auditor.
// Takes the uncertainty_sources from the last import and searches for answers.
func (s *Server) handleResearch(w http.ResponseWriter, r *http.Request) {
	marketID := r.PathValue("id")

	if s.tavily == nil {
		writeError(w, http.StatusServiceUnavailable, "Tavily not configured — set TAVILY_API_KEY")
		return
	}

	pred := s.session.GetImport(marketID)
	if pred == nil {
		writeError(w, http.StatusBadRequest, "No import data — run edge calculation first")
		return
	}

	if len(pred.UncertaintySources) == 0 {
		writeError(w, http.StatusBadRequest, "No uncertainty sources in auditor output")
		return
	}

	// Run Tavily search for each uncertainty source's queries
	var results []ResearchResult
	totalSearches := 0

	for _, us := range pred.UncertaintySources {
		rr := ResearchResult{
			Question:       us.Question,
			WhyItMatters:   us.WhyItMatters,
			ExpectedImpact: us.ExpectedImpact,
			Domain:         us.Domain,
		}

		// Search using the first query (most targeted), fall back to second if no results
		for _, query := range us.SearchQueries {
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			resp, err := s.tavily.Search(ctx, query, domainSearchOptions(us.Domain))
			cancel()

			if err != nil {
				s.logger.Warn("research search failed",
					slog.String("market_id", marketID),
					slog.String("question", us.Question),
					slog.String("error", err.Error()),
				)
				continue
			}

			totalSearches++

			// Collect relevant results (score > 0.4)
			for _, tr := range resp.Results {
				if tr.Score >= 0.40 {
					rr.SearchResults = append(rr.SearchResults, tr)
				}
			}
			if resp.Answer != "" {
				rr.Answer = resp.Answer
			}

			// If we got good results from first query, skip the alternative
			if len(rr.SearchResults) >= 3 {
				break
			}
		}

		results = append(results, rr)
	}

	s.session.SetResearch(marketID, results)

	writeJSON(w, researchResponse{
		MarketID: marketID,
		Results:  results,
		Cost:     fmt.Sprintf("~$%.3f (%d searches)", float64(totalSearches)*0.016, totalSearches),
	})
}

// domainSearchOptions returns Tavily search options tuned by uncertainty domain.
func domainSearchOptions(domain string) *analysis.SearchOptions {
	switch domain {
	case "polls":
		return &analysis.SearchOptions{Days: 30}
	case "legal", "regulatory":
		return &analysis.SearchOptions{Days: 60}
	case "sports_stats":
		return &analysis.SearchOptions{Days: 7}
	case "financial":
		return &analysis.SearchOptions{Days: 14}
	case "medical":
		return &analysis.SearchOptions{Days: 30}
	case "geopolitical":
		return &analysis.SearchOptions{Days: 30}
	default:
		return &analysis.SearchOptions{Days: 14}
	}
}

// handleReauditPrompt generates a re-audit prompt that includes the original analysis
// plus deep research findings, so the auditor can update its probability estimate.
func (s *Server) handleReauditPrompt(w http.ResponseWriter, r *http.Request) {
	marketID := r.PathValue("id")
	sig, ok := s.session.GetSignalByMarketID(marketID)
	if !ok {
		writeError(w, http.StatusNotFound, "Signal not found")
		return
	}

	pred := s.session.GetImport(marketID)
	if pred == nil {
		writeError(w, http.StatusBadRequest, "No import data — run edge calculation first")
		return
	}

	research := s.session.GetResearch(marketID)
	if len(research) == 0 {
		writeError(w, http.StatusBadRequest, "No research data — run deep research first")
		return
	}

	// Pull all available session context
	cond := s.session.GetCondition(marketID)
	perplexityOutput := s.session.GetPaste("perplexity", marketID)
	pillarlabOutput := ""
	if s.cfg.Analysis.PillarlabEnabled {
		pillarlabOutput = s.session.GetPaste("pillarlab", marketID)
	}

	prompt := generateReauditPrompt(sig, pred, research, cond, perplexityOutput, pillarlabOutput)
	writeJSON(w, map[string]string{"prompt": prompt})
}

// generateReauditPrompt builds a re-audit prompt with full context:
// market info, resolution conditions, original LLM outputs, auditor results, and deep research findings.
func generateReauditPrompt(sig scanner.Signal, pred *analysis.Prediction, research []ResearchResult, cond *analysis.ParsedCondition, perplexityOutput string, pillarlabOutput string) string {
	var sb strings.Builder

	sb.WriteString("You are a prediction market auditor performing a RE-AUDIT. You have the full analysis chain below: resolution conditions, original LLM research outputs, your previous audit, and new targeted research findings. Your job is to UPDATE your probability estimate with all of this context in mind.\n\n")

	// Market context
	price := sig.YesPrice
	if sig.TargetSide == "NO" {
		price = sig.NoPrice
	}
	sb.WriteString(fmt.Sprintf("**Market:** %s\n", sig.Market.Question))
	sb.WriteString(fmt.Sprintf("**Market ID:** %s\n", sig.Market.ID))
	sb.WriteString(fmt.Sprintf("**Current Price:** %s @ $%.3f\n", sig.TargetSide, price))
	sb.WriteString(fmt.Sprintf("**End Date:** %s (%d days)\n\n", sig.Market.EndDate.Format("2006-01-02"), sig.DaysToResolve))

	// Resolution conditions — defines what we're actually betting on
	if cond != nil && cond.Error == "" {
		sb.WriteString("--- RESOLUTION CONDITIONS ---\n")
		if cond.TriggerConditions != "" {
			sb.WriteString("Trigger: " + cond.TriggerConditions + "\n")
		}
		if cond.ResolutionSource != "" {
			sb.WriteString("Authority: " + cond.ResolutionSource + "\n")
		}
		if cond.EdgeCases != "" {
			sb.WriteString("Traps: " + cond.EdgeCases + "\n")
		}
		if cond.KeyDates != "" {
			sb.WriteString("Key dates: " + cond.KeyDates + "\n")
		}
		if cond.AmbiguityRisk != "" {
			sb.WriteString("Ambiguity risk: " + cond.AmbiguityRisk + "\n")
		}
		sb.WriteString("\n")
	}

	// Perplexity output — the facts found
	if perplexityOutput != "" {
		sb.WriteString("--- PERPLEXITY RESEARCH OUTPUT ---\n")
		sb.WriteString(perplexityOutput + "\n\n")
	}

	// PillarLab output — factor weights and base probability
	if pillarlabOutput != "" {
		sb.WriteString("--- PILLARLAB OUTPUT ---\n")
		sb.WriteString(pillarlabOutput + "\n\n")
	}

	// Original audit — full reasoning chain
	sb.WriteString("--- ORIGINAL AUDIT RESULTS ---\n")
	sb.WriteString(fmt.Sprintf("Previous probability: %.1f%%\n", pred.Probability*100))
	sb.WriteString(fmt.Sprintf("Previous confidence: %s\n", pred.Confidence))
	sb.WriteString(fmt.Sprintf("Previous reasoning: %s\n", pred.Reasoning))
	if pred.Adversarial != "" {
		sb.WriteString(fmt.Sprintf("Adversarial case: %s\n", pred.Adversarial))
	}
	if pred.ResolutionRisk != "" {
		sb.WriteString(fmt.Sprintf("Resolution risk (from research): %s\n", pred.ResolutionRisk))
	}
	if len(pred.KeyFindings) > 0 {
		sb.WriteString("Key findings from original research:\n")
		for i, f := range pred.KeyFindings {
			sb.WriteString(fmt.Sprintf("  [%d] %s (source: %s, date: %s)\n", i+1, f.Fact, f.Source, f.Date))
		}
	}
	sb.WriteString("\n")

	// Research findings
	sb.WriteString("--- DEEP RESEARCH FINDINGS ---\n\n")
	for i, rr := range research {
		sb.WriteString(fmt.Sprintf("### Uncertainty %d: %s\n", i+1, rr.Question))
		sb.WriteString(fmt.Sprintf("Why it matters: %s\n", rr.WhyItMatters))
		sb.WriteString(fmt.Sprintf("Expected impact: %s\n\n", rr.ExpectedImpact))

		if rr.Answer != "" {
			sb.WriteString(fmt.Sprintf("**Summary:** %s\n\n", rr.Answer))
		}

		if len(rr.SearchResults) > 0 {
			sb.WriteString("**Sources found:**\n")
			for j, sr := range rr.SearchResults {
				sb.WriteString(fmt.Sprintf("[%d] %s\n", j+1, sr.Title))
				sb.WriteString(fmt.Sprintf("    URL: %s\n", sr.URL))
				if sr.PublishedDate != "" {
					sb.WriteString(fmt.Sprintf("    Date: %s\n", sr.PublishedDate))
				}
				content := sr.Content
				if len(content) > 400 {
					content = content[:400] + "..."
				}
				sb.WriteString(fmt.Sprintf("    %s\n\n", content))
			}
		} else {
			sb.WriteString("**No relevant sources found for this question.**\n\n")
		}
	}

	// Instructions
	sb.WriteString("--- YOUR TASK ---\n\n")
	sb.WriteString("1. For each uncertainty source above, assess whether the research findings RESOLVE the uncertainty, PARTIALLY resolve it, or leave it UNRESOLVED.\n")
	sb.WriteString("2. Update your Bayesian estimate: start from your previous probability and adjust based on new evidence.\n")
	sb.WriteString("3. If any research finding CONTRADICTS your previous analysis, explain what changed.\n")
	sb.WriteString("4. Output your updated assessment.\n\n")

	sb.WriteString("**Output format (Strict JSON only, no markdown, no commentary):**\n\n")
	sb.WriteString("```json\n")
	sb.WriteString(`{
  "id": "market_id",
  "question": "short question text",
  "research_impact": [
    {
      "question": "The uncertainty question that was researched",
      "resolved": "yes" | "partial" | "no",
      "finding": "What the research revealed",
      "probability_shift": "+5pp" | "-3pp" | "0pp"
    }
  ],
  "bayesian_update": {
    "prior": 0.38,
    "prior_source": "Previous audit",
    "updates": [
      {
        "evidence": "New evidence from research",
        "why": "Why this shifts the estimate",
        "direction": "up" | "down",
        "magnitude": "small" | "medium" | "large",
        "likelihood_ratio": 1.3
      }
    ],
    "posterior": 0.45
  },
  "final_probability": 0.45,
  "confidence": "high" | "medium" | "low",
  "edge_pct": 7.0,
  "side": "YES" | "NO" | "SKIP",
  "reasoning": "2-3 sentence synthesis of how research changed (or confirmed) the picture",
  "uncertainty_sources": [
    {
      "question": "Any REMAINING uncertainty that still needs resolution",
      "why_it_matters": "How this gap still affects the estimate",
      "expected_impact": "large" | "medium" | "small",
      "search_queries": ["query1", "query2"],
      "domain": "polls" | "legal" | "regulatory" | "medical" | "financial" | "sports_stats" | "geopolitical" | "technical" | "other"
    }
  ]
}
`)
	sb.WriteString("```\n\n")

	sb.WriteString("**Rules:**\n")
	sb.WriteString("- If research CONFIRMS your previous estimate, say so — confirmation reduces uncertainty even if the number doesn't move.\n")
	sb.WriteString("- If research CONTRADICTS your previous estimate, be honest — update aggressively.\n")
	sb.WriteString("- Confidence should improve if uncertainties were resolved, even if probability stays similar.\n")
	sb.WriteString("- Only include remaining uncertainty_sources if there are STILL significant unresolved gaps.\n")

	return sb.String()
}

// --- Claude Auditor API ---

// handleRunAudit generates the auditor prompt and sends it directly to Claude Opus,
// then calculates edge from the response — no manual copy-paste needed.
func (s *Server) handleRunAudit(w http.ResponseWriter, r *http.Request) {
	if s.claudeAPIKey == "" {
		writeError(w, http.StatusServiceUnavailable, "Claude API not configured — set CLAUDE_API_KEY")
		return
	}

	marketID := r.PathValue("id")
	sig, ok := s.session.GetSignalByMarketID(marketID)
	if !ok {
		writeError(w, http.StatusNotFound, "Signal not found — run a scan first")
		return
	}

	tmpl, err := os.ReadFile(auditorTemplatePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load auditor template: "+err.Error())
		return
	}

	// Build the same context as handleAuditorPrompt
	var tavilyContext string
	if sc := s.session.GetTavily(marketID); sc != nil {
		tavilyContext = sc.FormatForClaude()
	}
	if news := s.session.GetNewsSummary(marketID); news != "" {
		tavilyContext = "=== KEY FACTS (AI-summarised from Tavily + Exa) ===\n" + news + "\n\n" + tavilyContext
	}
	perplexity := s.session.GetPaste("perplexity", marketID)
	pillarlab := ""
	if s.cfg.Analysis.PillarlabEnabled {
		pillarlab = s.session.GetPaste("pillarlab", marketID)
	}
	cond := s.session.GetCondition(marketID)

	prompt := analysis.GenerateAuditorPromptWithConditions(string(tmpl), sig, tavilyContext, perplexity, pillarlab, cond)
	s.session.SetPrompt(marketID, "auditor", prompt)

	s.logger.Info("Running Claude Opus audit via API",
		slog.String("market_id", marketID),
		slog.String("question", sig.Market.Question),
	)

	ctx, cancel := context.WithTimeout(r.Context(), analysis.AuditorTimeout)
	defer cancel()

	responseText, err := analysis.CallClaude(ctx, s.claudeAPIKey, analysis.ClaudeOpusModel, prompt, 4096)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Claude API call failed: "+err.Error())
		return
	}

	predictions, err := analysis.ParsePredictions(responseText)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to parse Claude response: "+err.Error())
		return
	}

	signals := []scanner.Signal{sig}
	importResult := analysis.MatchPredictions(predictions, signals)

	var resp importResponse
	for _, p := range predictions {
		resp.Warnings = append(resp.Warnings, analysis.ValidatePrediction(p)...)
	}
	for _, m := range importResult.Matched {
		pred := m.Prediction
		s.session.SetImport(m.Signal.Market.ID, &pred)
		resp.Matched = append(resp.Matched, matchedResponse{
			MarketID:           m.Signal.Market.ID,
			Question:           m.Signal.Market.Question,
			OurSide:            m.OurSide,
			OurPrice:           m.OurPrice,
			TrueProb:           m.TrueProb,
			Edge:               m.Edge,
			EdgePct:            m.Edge * 100,
			Confidence:         m.Prediction.Confidence,
			Reasoning:          m.Prediction.Reasoning,
			UncertaintySources: m.Prediction.UncertaintySources,
		})
	}
	for _, u := range importResult.Unmatched {
		resp.Unmatched = append(resp.Unmatched, u.ID+": "+u.Question)
	}

	writeJSON(w, resp)
}

// handleRunReaudit generates the re-audit prompt and sends it directly to Claude Opus,
// then calculates the updated edge — no manual copy-paste needed.
func (s *Server) handleRunReaudit(w http.ResponseWriter, r *http.Request) {
	if s.claudeAPIKey == "" {
		writeError(w, http.StatusServiceUnavailable, "Claude API not configured — set CLAUDE_API_KEY")
		return
	}

	marketID := r.PathValue("id")
	sig, ok := s.session.GetSignalByMarketID(marketID)
	if !ok {
		writeError(w, http.StatusNotFound, "Signal not found")
		return
	}

	pred := s.session.GetImport(marketID)
	if pred == nil {
		writeError(w, http.StatusBadRequest, "No import data — run edge calculation first")
		return
	}

	research := s.session.GetResearch(marketID)
	if len(research) == 0 {
		writeError(w, http.StatusBadRequest, "No research data — run deep research first")
		return
	}

	cond := s.session.GetCondition(marketID)
	perplexityOutput := s.session.GetPaste("perplexity", marketID)
	pillarlabOutput := ""
	if s.cfg.Analysis.PillarlabEnabled {
		pillarlabOutput = s.session.GetPaste("pillarlab", marketID)
	}

	prompt := generateReauditPrompt(sig, pred, research, cond, perplexityOutput, pillarlabOutput)
	s.session.SetPrompt(marketID, "reaudit", prompt)

	s.logger.Info("Running Claude Opus re-audit via API",
		slog.String("market_id", marketID),
		slog.String("question", sig.Market.Question),
	)

	ctx, cancel := context.WithTimeout(r.Context(), analysis.AuditorTimeout)
	defer cancel()

	responseText, err := analysis.CallClaude(ctx, s.claudeAPIKey, analysis.ClaudeOpusModel, prompt, 4096)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Claude API call failed: "+err.Error())
		return
	}

	predictions, err := analysis.ParsePredictions(responseText)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to parse Claude re-audit response: "+err.Error())
		return
	}

	signals := []scanner.Signal{sig}
	importResult := analysis.MatchPredictions(predictions, signals)

	var resp importResponse
	for _, m := range importResult.Matched {
		updatedPred := m.Prediction
		// Store in both caches: importCache for re-audit chaining, reauditCache for debug tracking
		s.session.SetImport(m.Signal.Market.ID, &updatedPred)
		s.session.SetReaudit(m.Signal.Market.ID, &updatedPred)
		resp.Matched = append(resp.Matched, matchedResponse{
			MarketID:           m.Signal.Market.ID,
			Question:           m.Signal.Market.Question,
			OurSide:            m.OurSide,
			OurPrice:           m.OurPrice,
			TrueProb:           m.TrueProb,
			Edge:               m.Edge,
			EdgePct:            m.Edge * 100,
			Confidence:         m.Prediction.Confidence,
			Reasoning:          m.Prediction.Reasoning,
			UncertaintySources: m.Prediction.UncertaintySources,
		})
	}
	for _, u := range importResult.Unmatched {
		resp.Unmatched = append(resp.Unmatched, u.ID+": "+u.Question)
	}

	writeJSON(w, resp)
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// --- Debug / Pipeline Audit ---

// handleDebugPrompt assembles the full analysis pipeline for a signal into a
// single structured prompt for Claude Opus to audit. Covers everything collected
// in the session: market details, resolution conditions, all LLM outputs,
// Tavily results, edge calculation, and deep research.
func (s *Server) handleDebugPrompt(w http.ResponseWriter, r *http.Request) {
	marketID := r.PathValue("id")
	sig, ok := s.session.GetSignalByMarketID(marketID)
	if !ok {
		writeError(w, http.StatusNotFound, "Signal not found")
		return
	}

	cond := s.session.GetCondition(marketID)
	tavilyResult := s.session.GetTavily(marketID)
	exaResult := s.session.GetExa(marketID)
	newsSummary := s.session.GetNewsSummary(marketID)
	perplexityOutput := s.session.GetPaste("perplexity", marketID)
	pillarlabOutput := ""
	if s.cfg.Analysis.PillarlabEnabled {
		pillarlabOutput = s.session.GetPaste("pillarlab", marketID)
	}
	pred := s.session.GetImport(marketID)
	reauditPred := s.session.GetReaudit(marketID)
	research := s.session.GetResearch(marketID)
	prompts := s.session.GetPrompts(marketID)

	prompt := generateDebugPrompt(sig, cond, tavilyResult, exaResult, newsSummary, perplexityOutput, pillarlabOutput, pred, reauditPred, research, prompts)
	writeJSON(w, map[string]string{"prompt": prompt})
}

func generateDebugPrompt(
	sig scanner.Signal,
	cond *analysis.ParsedCondition,
	tavily *analysis.SearchContext,
	exaResult *exa.SearchResponse,
	newsSummary string,
	perplexityOutput string,
	pillarlabOutput string,
	pred *analysis.Prediction,
	reauditPred *analysis.Prediction,
	research []ResearchResult,
	prompts map[string]string,
) string {
	var sb strings.Builder

	price := sig.YesPrice
	if sig.TargetSide == "NO" {
		price = sig.NoPrice
	}

	sb.WriteString("You are auditing the Golden Rain prediction market analysis pipeline. Below is the COMPLETE analysis chain for a single market — every piece of data collected. Your job is to find root causes of any errors, not symptoms.\n\n")
	sb.WriteString("Evaluate:\n")
	sb.WriteString("1. **Internal consistency** — do the sources agree? where do they conflict and who is right?\n")
	sb.WriteString("2. **Resolution trap coverage** — were the parsed resolution conditions properly addressed?\n")
	sb.WriteString("3. **Probability calibration** — is the final probability well-supported, or over/under-confident?\n")
	sb.WriteString("4. **Pipeline gaps** — what important information was missing or not used?\n")
	sb.WriteString("5. **Final verdict** — GO / NO-GO / REDUCE. If GO: what Kelly fraction and why?\n\n")
	sb.WriteString("---\n\n")

	// 1. Market details
	sb.WriteString("## 1. MARKET\n\n")
	sb.WriteString("Question: " + sig.Market.Question + "\n")
	sb.WriteString(fmt.Sprintf("Market ID: %s\n", sig.Market.ID))
	sb.WriteString(fmt.Sprintf("URL: https://polymarket.com/market/%s\n", sig.Market.Slug))
	sb.WriteString(fmt.Sprintf("Price: YES $%.3f / NO $%.3f\n", sig.YesPrice, sig.NoPrice))
	sb.WriteString(fmt.Sprintf("Target: %s @ $%.3f\n", sig.TargetSide, price))
	sb.WriteString(fmt.Sprintf("Resolves: %s (%d days)\n", sig.Market.EndDate.Format("2006-01-02"), sig.DaysToResolve))
	sb.WriteString(fmt.Sprintf("Tier: %s | Score: %.1f | Theta: %.2f | Spread: %.2f%%\n", sig.Tier, sig.Score, sig.ThetaMultiplier, sig.SpreadPercent))
	desc := strings.TrimSpace(sig.Market.Description)
	if desc != "" {
		sb.WriteString("\nResolution criteria (raw):\n" + desc + "\n")
	}
	sb.WriteString("\n")

	// 2. Parsed resolution conditions
	if cond != nil && cond.Error == "" {
		sb.WriteString("## 2. PARSED RESOLUTION CONDITIONS\n\n")
		if cond.TriggerConditions != "" {
			sb.WriteString("Trigger conditions:\n" + cond.TriggerConditions + "\n\n")
		}
		if cond.ResolutionSource != "" {
			sb.WriteString("Resolution authority: " + cond.ResolutionSource + "\n\n")
		}
		if cond.EdgeCases != "" {
			sb.WriteString("Edge cases / traps:\n" + cond.EdgeCases + "\n\n")
		}
		if cond.KeyDates != "" {
			sb.WriteString("Key dates:\n" + cond.KeyDates + "\n\n")
		}
		if cond.AmbiguityRisk != "" {
			sb.WriteString("Ambiguity risk: " + strings.ToUpper(cond.AmbiguityRisk) + "\n\n")
		}
	} else {
		sb.WriteString("## 2. PARSED RESOLUTION CONDITIONS\n\n(not run)\n\n")
	}

	// 3. AI news summary
	sb.WriteString("## 3. AI NEWS SUMMARY (Tavily + Exa distilled)\n\n")
	if newsSummary != "" {
		sb.WriteString(newsSummary + "\n\n")
	} else {
		sb.WriteString("(not generated)\n\n")
	}

	// 4. Tavily news search — full results with content
	sb.WriteString("## 4. TAVILY NEWS SEARCH (raw results)\n\n")
	if tavily != nil && len(tavily.Results) > 0 {
		sb.WriteString(fmt.Sprintf("Query: %s\n", tavily.Query))
		if tavily.Answer != "" {
			sb.WriteString("AI answer: " + tavily.Answer + "\n\n")
		}
		for i, r := range tavily.Results {
			sb.WriteString(fmt.Sprintf("[%d] %s\n", i+1, r.Title))
			sb.WriteString(fmt.Sprintf("    URL: %s\n", r.URL))
			if r.PublishedDate != "" {
				sb.WriteString(fmt.Sprintf("    Date: %s\n", r.PublishedDate))
			}
			sb.WriteString(fmt.Sprintf("    Score: %.2f\n", r.Score))
			if r.Content != "" {
				sb.WriteString(fmt.Sprintf("    %s\n", r.Content))
			}
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString("(not collected)\n\n")
	}

	// 5. Exa semantic search — full results with content
	sb.WriteString("## 5. EXA SEMANTIC SEARCH (raw results)\n\n")
	if exaResult != nil && len(exaResult.Results) > 0 {
		for i, r := range exaResult.Results {
			sb.WriteString(fmt.Sprintf("[%d] %s\n", i+1, r.Title))
			sb.WriteString(fmt.Sprintf("    URL: %s\n", r.URL))
			if r.PublishedDate != "" {
				sb.WriteString(fmt.Sprintf("    Date: %s\n", r.PublishedDate))
			}
			sb.WriteString(fmt.Sprintf("    Score: %.3f\n", r.Score))
			if r.Text != "" {
				sb.WriteString(fmt.Sprintf("    %s\n", r.Text))
			}
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString("(not collected)\n\n")
	}

	// 6. Perplexity output
	sb.WriteString("## 6. PERPLEXITY RESEARCH OUTPUT\n\n")
	if perplexityOutput != "" {
		sb.WriteString(perplexityOutput + "\n\n")
	} else {
		sb.WriteString("(not collected)\n\n")
	}

	// 7. PillarLab output
	sb.WriteString("## 7. PILLARLAB OUTPUT\n\n")
	if pillarlabOutput != "" {
		sb.WriteString(pillarlabOutput + "\n\n")
	} else {
		sb.WriteString("(not collected)\n\n")
	}

	// 8. Claude auditor output
	sb.WriteString("## 8. CLAUDE AUDITOR OUTPUT (initial)\n\n")
	if pred != nil {
		sb.WriteString(fmt.Sprintf("Final probability: %.1f%%\n", pred.Probability*100))
		sb.WriteString(fmt.Sprintf("Confidence: %s\n", pred.Confidence))
		if pred.EdgePct != 0 {
			sb.WriteString(fmt.Sprintf("Edge declared: %.1f%%\n", pred.EdgePct))
		}
		sb.WriteString(fmt.Sprintf("Reasoning: %s\n", pred.Reasoning))
		if pred.Adversarial != "" {
			sb.WriteString(fmt.Sprintf("Adversarial case: %s\n", pred.Adversarial))
		}
		if pred.ResolutionRisk != "" {
			sb.WriteString(fmt.Sprintf("Resolution risk: %s\n", pred.ResolutionRisk))
		}
		if pred.CurrentState != "" {
			sb.WriteString(fmt.Sprintf("Current state: %s\n", pred.CurrentState))
		}
		if len(pred.KeyFindings) > 0 {
			sb.WriteString("\nKey findings:\n")
			for i, f := range pred.KeyFindings {
				sb.WriteString(fmt.Sprintf("  [%d] %s (source: %s, %s)\n", i+1, f.Fact, f.Source, f.Date))
			}
		}
		if len(pred.UncertaintySources) > 0 {
			sb.WriteString("\nIdentified uncertainty sources:\n")
			for i, us := range pred.UncertaintySources {
				sb.WriteString(fmt.Sprintf("  [%d] [%s] %s\n      Why: %s\n      Impact: %s\n      Queries: %s\n",
					i+1, us.Domain, us.Question, us.WhyItMatters, us.ExpectedImpact, strings.Join(us.SearchQueries, " | ")))
			}
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("(not run)\n\n")
	}

	// 9. Deep research on uncertainty gaps (second Tavily pass)
	sb.WriteString("## 9. DEEP RESEARCH ON UNCERTAINTY GAPS (second Tavily pass)\n\n")
	if len(research) > 0 {
		for i, rr := range research {
			sb.WriteString(fmt.Sprintf("### Gap %d: %s\n", i+1, rr.Question))
			sb.WriteString(fmt.Sprintf("Why it matters: %s\n", rr.WhyItMatters))
			sb.WriteString(fmt.Sprintf("Expected impact: %s | Domain: %s\n", rr.ExpectedImpact, rr.Domain))
			if rr.Answer != "" {
				sb.WriteString(fmt.Sprintf("AI answer: %s\n", rr.Answer))
			}
			if len(rr.SearchResults) > 0 {
				sb.WriteString(fmt.Sprintf("Sources (%d found):\n", len(rr.SearchResults)))
				for j, sr := range rr.SearchResults {
					sb.WriteString(fmt.Sprintf("  [%d] %s\n      URL: %s\n", j+1, sr.Title, sr.URL))
					if sr.PublishedDate != "" {
						sb.WriteString(fmt.Sprintf("      Date: %s\n", sr.PublishedDate))
					}
					if sr.Content != "" {
						sb.WriteString(fmt.Sprintf("      %s\n", sr.Content))
					}
				}
			} else {
				sb.WriteString("Sources: none found\n")
			}
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString("(not run)\n\n")
	}

	// 10. Re-audit output (if run)
	sb.WriteString("## 10. CLAUDE RE-AUDIT OUTPUT (post-research)\n\n")
	if reauditPred != nil {
		sb.WriteString(fmt.Sprintf("Updated probability: %.1f%%\n", reauditPred.Probability*100))
		sb.WriteString(fmt.Sprintf("Confidence: %s\n", reauditPred.Confidence))
		if reauditPred.EdgePct != 0 {
			sb.WriteString(fmt.Sprintf("Edge declared: %.1f%%\n", reauditPred.EdgePct))
		}
		sb.WriteString(fmt.Sprintf("Reasoning: %s\n", reauditPred.Reasoning))
		if len(reauditPred.UncertaintySources) > 0 {
			sb.WriteString("\nRemaining uncertainties:\n")
			for i, us := range reauditPred.UncertaintySources {
				sb.WriteString(fmt.Sprintf("  [%d] [%s] %s (impact: %s)\n", i+1, us.Domain, us.Question, us.ExpectedImpact))
			}
		}
		if pred != nil {
			delta := (reauditPred.Probability - pred.Probability) * 100
			sb.WriteString(fmt.Sprintf("\nProbability shift from initial audit: %+.1fpp\n", delta))
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("(not run)\n\n")
	}

	// 11. Models used
	sb.WriteString("## 11. MODELS USED\n\n")
	sb.WriteString("- Condition parser: claude-haiku-4-5-20251001\n")
	sb.WriteString("- News summariser: claude-haiku-4-5-20251001\n")
	sb.WriteString("- Query generator: claude-haiku-4-5-20251001\n")
	sb.WriteString("- Claude Auditor: claude-opus-4-6\n")
	sb.WriteString("- Claude Re-Auditor: claude-opus-4-6\n\n")

	// 12. Prompts sent to LLMs
	promptOrder := []struct{ key, label string }{
		{"perplexity", "Perplexity prompt (generated for manual use)"},
		{"pillarlab", "PillarLab prompt (generated for manual use)"},
		{"auditor", "Claude Auditor prompt (sent to claude-opus-4-6)"},
		{"reaudit", "Claude Re-Audit prompt (sent to claude-opus-4-6)"},
	}
	sb.WriteString("## 12. PROMPTS SENT TO LLMs\n\n")
	if len(prompts) == 0 {
		sb.WriteString("(no prompts generated yet in this session)\n\n")
	} else {
		for _, p := range promptOrder {
			text, ok := prompts[p.key]
			if !ok || text == "" {
				sb.WriteString(fmt.Sprintf("### %s\n\n(not generated)\n\n", p.label))
				continue
			}
			sb.WriteString(fmt.Sprintf("### %s\n\n", p.label))
			sb.WriteString(text + "\n\n")
		}
	}

	// Final ask
	sb.WriteString("---\n\n")
	sb.WriteString("## YOUR ANALYSIS\n\n")
	sb.WriteString("Review the full pipeline above. Focus on root causes, not symptoms.\n\n")
	sb.WriteString("1. **Consistency check** — where do Perplexity, PillarLab, Auditor, and Re-Audit agree or disagree? Who is right and why?\n")
	sb.WriteString("2. **Resolution trap check** — do the parsed conditions reveal traps that weren't properly addressed?\n")
	sb.WriteString("3. **Search quality check** — did Tavily / Exa / deep research find the right sources? What's missing?\n")
	sb.WriteString("4. **Probability assessment** — is the final probability well-calibrated? By how much should it shift?\n")
	sb.WriteString("5. **Pipeline gaps** — what key information was missing at each stage?\n")
	sb.WriteString("6. **Trade verdict** — GO / NO-GO / REDUCE. If GO: Kelly fraction and reasoning.\n")

	return sb.String()
}
