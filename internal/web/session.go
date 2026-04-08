package web

import (
	"sync"

	"github.com/badgateway/poly/internal/analysis"
	"github.com/badgateway/poly/internal/exa"
	"github.com/badgateway/poly/internal/scanner"
)

// ResearchResult holds the Tavily search results for a single uncertainty source.
type ResearchResult struct {
	Question       string                  `json:"question"`
	WhyItMatters   string                  `json:"why_it_matters"`
	ExpectedImpact string                  `json:"expected_impact"`
	Domain         string                  `json:"domain"`
	SearchResults  []analysis.TavilyResult `json:"search_results"`
	Answer         string                  `json:"answer,omitempty"`
}

// Session holds in-memory state for the web UI.
// Same pattern as the Telegram bot's Bot struct fields.
type Session struct {
	mu             sync.RWMutex
	lastResult     *scanner.PipelineResult
	tavilyCache    map[string]*analysis.SearchContext  // marketID → tavily results
	exaCache       map[string]*exa.SearchResponse      // marketID → exa semantic search results
	newsSummary    map[string]string                   // marketID → AI-generated key facts from Tavily+Exa
	perplexityData map[string]string                   // marketID → pasted Perplexity output
	pillarlabData  map[string]string                   // marketID → pasted PillarLab output
	researchCache  map[string][]ResearchResult          // marketID → deep research results
	importCache    map[string]*analysis.Prediction      // marketID → initial audit prediction (for re-audit chaining)
	reauditCache   map[string]*analysis.Prediction      // marketID → re-audit prediction (separate from initial)
	conditionCache map[string]*analysis.ParsedCondition // marketID → parsed resolution conditions
	promptCache    map[string]map[string]string         // marketID → {promptName → promptText} for debug
}

// NewSession creates an empty session.
func NewSession() *Session {
	return &Session{
		tavilyCache:    make(map[string]*analysis.SearchContext),
		exaCache:       make(map[string]*exa.SearchResponse),
		newsSummary:    make(map[string]string),
		perplexityData: make(map[string]string),
		pillarlabData:  make(map[string]string),
		researchCache:  make(map[string][]ResearchResult),
		importCache:    make(map[string]*analysis.Prediction),
		reauditCache:   make(map[string]*analysis.Prediction),
		conditionCache: make(map[string]*analysis.ParsedCondition),
		promptCache:    make(map[string]map[string]string),
	}
}

// SetResult stores a pipeline result from a scan.
func (s *Session) SetResult(result *scanner.PipelineResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastResult = result
	// Clear caches from previous scan
	s.tavilyCache = make(map[string]*analysis.SearchContext)
	s.exaCache = make(map[string]*exa.SearchResponse)
	s.newsSummary = make(map[string]string)
	s.perplexityData = make(map[string]string)
	s.pillarlabData = make(map[string]string)
	s.researchCache = make(map[string][]ResearchResult)
	s.importCache = make(map[string]*analysis.Prediction)
	s.reauditCache = make(map[string]*analysis.Prediction)
	s.conditionCache = make(map[string]*analysis.ParsedCondition)
	s.promptCache = make(map[string]map[string]string)
}

// GetResult returns the last pipeline result.
func (s *Session) GetResult() *scanner.PipelineResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastResult
}

// GetAlphas returns alpha signals from the last scan.
func (s *Session) GetAlphas() []scanner.Signal {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastResult == nil {
		return nil
	}
	var alphas []scanner.Signal
	for _, sig := range s.lastResult.Signals {
		if sig.IsAlpha() {
			alphas = append(alphas, sig)
		}
	}
	return alphas
}

// GetShadows returns shadow signals from the last scan.
func (s *Session) GetShadows() []scanner.Signal {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastResult == nil {
		return nil
	}
	var shadows []scanner.Signal
	for _, sig := range s.lastResult.Signals {
		if !sig.IsAlpha() {
			shadows = append(shadows, sig)
		}
	}
	return shadows
}

// GetSignalByMarketID finds a signal by its market ID.
func (s *Session) GetSignalByMarketID(marketID string) (scanner.Signal, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastResult == nil {
		return scanner.Signal{}, false
	}
	for _, sig := range s.lastResult.Signals {
		if sig.Market.ID == marketID {
			return sig, true
		}
	}
	return scanner.Signal{}, false
}

// SetTavily caches Tavily search results for a market.
func (s *Session) SetTavily(marketID string, ctx *analysis.SearchContext) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tavilyCache[marketID] = ctx
}

// GetTavily returns cached Tavily results for a market.
func (s *Session) GetTavily(marketID string) *analysis.SearchContext {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tavilyCache[marketID]
}

// SetExa caches Exa semantic search results for a market.
func (s *Session) SetExa(marketID string, resp *exa.SearchResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exaCache[marketID] = resp
}

// GetExa returns cached Exa results for a market.
func (s *Session) GetExa(marketID string) *exa.SearchResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.exaCache[marketID]
}

// SetNewsSummary stores the AI-generated key facts summary for a market.
func (s *Session) SetNewsSummary(marketID string, summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.newsSummary[marketID] = summary
}

// GetNewsSummary returns the cached news summary for a market.
func (s *Session) GetNewsSummary(marketID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.newsSummary[marketID]
}

// SetPaste stores a pasted LLM output for a market.
func (s *Session) SetPaste(source, marketID, data string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch source {
	case "perplexity":
		s.perplexityData[marketID] = data
	case "pillarlab":
		s.pillarlabData[marketID] = data
	}
}

// GetPaste returns a pasted LLM output for a market.
func (s *Session) GetPaste(source, marketID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch source {
	case "perplexity":
		return s.perplexityData[marketID]
	case "pillarlab":
		return s.pillarlabData[marketID]
	}
	return ""
}

// SetResearch caches deep research results for a market.
func (s *Session) SetResearch(marketID string, results []ResearchResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.researchCache[marketID] = results
}

// GetResearch returns cached deep research results for a market.
func (s *Session) GetResearch(marketID string) []ResearchResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.researchCache[marketID]
}

// SetImport caches the last imported prediction for a market (used for re-audit).
func (s *Session) SetImport(marketID string, pred *analysis.Prediction) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.importCache[marketID] = pred
}

// GetImport returns the last imported prediction for a market.
func (s *Session) GetImport(marketID string) *analysis.Prediction {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.importCache[marketID]
}

// SetPrompt stores a prompt that was sent to an LLM, keyed by name (e.g. "auditor", "reaudit", "perplexity", "pillarlab").
func (s *Session) SetPrompt(marketID, name, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.promptCache[marketID] == nil {
		s.promptCache[marketID] = make(map[string]string)
	}
	s.promptCache[marketID][name] = text
}

// GetPrompts returns all stored prompts for a market.
func (s *Session) GetPrompts(marketID string) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.promptCache[marketID]
}

// SetReaudit caches the re-audit prediction for a market (separate from the initial audit).
func (s *Session) SetReaudit(marketID string, pred *analysis.Prediction) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reauditCache[marketID] = pred
}

// GetReaudit returns the re-audit prediction for a market, or nil if no re-audit was run.
func (s *Session) GetReaudit(marketID string) *analysis.Prediction {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.reauditCache[marketID]
}

// SetCondition caches parsed condition analysis for a market.
func (s *Session) SetCondition(marketID string, condition *analysis.ParsedCondition) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conditionCache[marketID] = condition
}

// GetCondition returns cached condition analysis for a market.
func (s *Session) GetCondition(marketID string) *analysis.ParsedCondition {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.conditionCache[marketID]
}
