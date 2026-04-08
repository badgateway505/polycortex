package analysis

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/badgateway/poly/internal/scanner"
)

// Prediction represents a single prediction from any AI source (PillarLab, Perplexity, Claude, Grok).
// Uses a superset of fields — each source populates what it can.
type Prediction struct {
	ID          string  `json:"id"`
	Question    string  `json:"question"`
	Probability float64 `json:"probability"`       // True probability of YES (0.0-1.0)
	FinalProb   float64 `json:"final_probability"` // Alias used by Claude Auditor output
	Confidence  string  `json:"confidence"`        // "high", "medium", "low"
	Reasoning   string  `json:"reasoning"`

	// PillarLab-specific
	EdgePct     float64 `json:"edge_pct,omitempty"`      // |true_prob - market_price| as %
	EVAfterFees float64 `json:"ev_after_fees,omitempty"` // EV per $1 after fees, in cents

	// Perplexity-specific (research output)
	CurrentState   string     `json:"current_state,omitempty"`   // Factual summary with timestamps
	KeyFindings    []Finding  `json:"key_findings,omitempty"`    // Cited evidence
	BookmakerOdds  string     `json:"bookmaker_odds,omitempty"`  // What bookmakers price this at
	Adversarial    string     `json:"adversarial,omitempty"`     // Counter-argument
	DeepFactors    string     `json:"deep_factors,omitempty"`    // Institutional interference, hidden factors
	ResolutionRisk string     `json:"resolution_risk,omitempty"` // "low"/"medium"/"high"
	BreakingNews   bool       `json:"breaking_news,omitempty"`   // Recent news flag
	DataFreshness  string     `json:"data_freshness,omitempty"`  // "high"/"medium"/"low"

	// Auditor-specific: information gaps that would reduce uncertainty
	UncertaintySources []UncertaintySource `json:"uncertainty_sources,omitempty"`

	// Bayesian update posterior — another alias LLMs sometimes use
	BayesianUpdate *struct {
		Posterior float64 `json:"posterior"`
	} `json:"bayesian_update,omitempty"`
}

// Finding is a single cited fact from research
type Finding struct {
	Fact   string `json:"fact"`
	Source string `json:"source"`
	Date   string `json:"date"`
}

// UncertaintySource represents an information gap identified by the auditor
// that, if resolved, would most reduce uncertainty in the probability estimate.
type UncertaintySource struct {
	Question       string   `json:"question"`
	WhyItMatters   string   `json:"why_it_matters"`
	ExpectedImpact string   `json:"expected_impact"` // "large", "medium", "small"
	SearchQueries  []string `json:"search_queries"`
	Domain         string   `json:"domain"` // "polls", "legal", "regulatory", "medical", "financial", "sports_stats", "geopolitical", "technical", "other"
}

// PillarLabPrediction is an alias for backward compatibility
type PillarLabPrediction = Prediction

// ImportResult is the output of matching predictions to scanned signals
type ImportResult struct {
	Source    string          // "pillarlab", "perplexity", "claude", "grok"
	Matched   []MatchedSignal
	Unmatched []Prediction    // Predictions that didn't match any signal
}

// MatchedSignal pairs a prediction with its scanner signal
type MatchedSignal struct {
	Signal     scanner.Signal
	Prediction Prediction
	Edge       float64 // Our edge: true_prob - market_price for the target side
	OurSide    string  // "YES" or "NO" — which side we'd buy
	OurPrice   float64 // Current market price for our side
	TrueProb   float64 // Source's probability for our side
}

// ParsePredictions parses AI output (PillarLab, Perplexity, Claude, Grok).
// Handles: JSON array, single JSON object, markdown code blocks, extra text.
func ParsePredictions(raw string) ([]Prediction, error) {
	cleaned := cleanJSON(raw)
	if cleaned == "" {
		return nil, fmt.Errorf("no JSON found in input")
	}

	var predictions []Prediction

	// Try array first
	if err := json.Unmarshal([]byte(cleaned), &predictions); err != nil {
		// Try single object (Perplexity returns one at a time)
		var single Prediction
		if err2 := json.Unmarshal([]byte(cleaned), &single); err2 != nil {
			return nil, fmt.Errorf("parse JSON: %w", err)
		}
		predictions = []Prediction{single}
	}

	if len(predictions) == 0 {
		return nil, fmt.Errorf("empty predictions")
	}

	// Normalize and validate
	for i := range predictions {
		p := &predictions[i]

		// Resolve probability from whichever field the LLM used
		if p.Probability == 0 {
			if p.FinalProb > 0 {
				p.Probability = p.FinalProb
			} else if p.BayesianUpdate != nil && p.BayesianUpdate.Posterior > 0 {
				p.Probability = p.BayesianUpdate.Posterior
			}
		}

		// If still zero, try extracting from raw JSON as last resort
		if p.Probability == 0 {
			p.Probability = extractProbability(cleaned, p.ID)
		}

		// Normalize confidence to lowercase
		p.Confidence = strings.ToLower(strings.TrimSpace(p.Confidence))
		if p.Confidence != "high" && p.Confidence != "medium" && p.Confidence != "low" {
			p.Confidence = "low" // Default to conservative
		}

		// Validate probability
		if p.Probability < 0 || p.Probability > 1 {
			return nil, fmt.Errorf("prediction #%d (%s): probability %.2f out of range [0,1]", i+1, p.ID, p.Probability)
		}
		if p.Probability == 0 {
			return nil, fmt.Errorf("prediction #%d (%s): no probability found — expected 'probability', 'final_probability', or 'bayesian_update.posterior' field", i+1, p.ID)
		}

		// Validate ID exists
		if p.ID == "" {
			return nil, fmt.Errorf("prediction #%d: missing 'id' field", i+1)
		}
	}

	return predictions, nil
}

// ParsePillarLabOutput is an alias for backward compatibility.
func ParsePillarLabOutput(raw string) ([]PillarLabPrediction, error) {
	return ParsePredictions(raw)
}

// MatchPredictions matches PillarLab predictions to scanner signals and calculates edge.
func MatchPredictions(predictions []PillarLabPrediction, signals []scanner.Signal) ImportResult {
	// Build lookup by market ID
	sigByID := make(map[string]scanner.Signal)
	for _, sig := range signals {
		sigByID[sig.Market.ID] = sig
	}

	var result ImportResult
	for _, pred := range predictions {
		sig, ok := sigByID[pred.ID]
		if !ok {
			result.Unmatched = append(result.Unmatched, pred)
			continue
		}

		matched := MatchedSignal{
			Signal:     sig,
			Prediction: pred,
			OurSide:    sig.TargetSide,
		}

		// Calculate edge based on which side we'd buy
		if sig.TargetSide == "YES" {
			matched.OurPrice = sig.YesPrice
			matched.TrueProb = pred.Probability
		} else {
			matched.OurPrice = sig.NoPrice
			matched.TrueProb = 1 - pred.Probability // P(NO) = 1 - P(YES)
		}

		// Edge = true probability - market price (positive = we have edge)
		matched.Edge = matched.TrueProb - matched.OurPrice

		result.Matched = append(result.Matched, matched)
	}

	return result
}

// GeneratePrompt builds the PillarLab prompt from a template and Alpha signals.
func GeneratePrompt(template string, signals []scanner.Signal) string {
	var markets strings.Builder
	idx := 0
	for _, sig := range signals {
		if !sig.IsAlpha() {
			continue
		}
		idx++

		price := sig.YesPrice
		if sig.TargetSide == "NO" {
			price = sig.NoPrice
		}

		desc := strings.TrimSpace(sig.Market.Description)
		if desc == "" {
			desc = "(no resolution criteria provided)"
		}

		markets.WriteString(fmt.Sprintf(
			"%d. %s\n   ID: %s\n   Prices: YES $%.3f / NO $%.3f\n   Target: %s @ $%.3f | Resolves in %d days\n   Rules: %s\n\n",
			idx,
			sig.Market.Question,
			sig.Market.ID,
			sig.YesPrice,
			sig.NoPrice,
			sig.TargetSide,
			price,
			sig.DaysToResolve,
			desc,
		))
	}

	if idx == 0 {
		return "No Alpha signals to analyze."
	}

	return strings.Replace(template, "{{MARKETS}}", markets.String(), 1)
}

// GenerateSinglePrompt builds a PillarLab prompt for a single signal.
func GenerateSinglePrompt(template string, sig scanner.Signal) string {
	return GenerateSinglePromptWithConditions(template, sig, nil, "")
}

// GenerateSinglePromptWithConditions builds a PillarLab prompt for a single signal,
// optionally appending pre-parsed resolution conditions and news context.
func GenerateSinglePromptWithConditions(template string, sig scanner.Signal, cond *ParsedCondition, newsContext string) string {
	if !sig.IsAlpha() {
		return "No Alpha signals to analyze."
	}

	price := sig.YesPrice
	if sig.TargetSide == "NO" {
		price = sig.NoPrice
	}

	desc := strings.TrimSpace(sig.Market.Description)
	if desc == "" {
		desc = "(no resolution criteria provided)"
	}

	var market strings.Builder
	market.WriteString(fmt.Sprintf(
		"1. %s\n   ID: %s\n   Prices: YES $%.3f / NO $%.3f\n   Target: %s @ $%.3f | Resolves in %d days\n   Rules: %s\n",
		sig.Market.Question,
		sig.Market.ID,
		sig.YesPrice,
		sig.NoPrice,
		sig.TargetSide,
		price,
		sig.DaysToResolve,
		desc,
	))

	// Append resolution trap analysis if available
	if cond != nil && cond.Error == "" {
		market.WriteString("   ⚠️ Resolution traps (pre-parsed):\n")
		if cond.TriggerConditions != "" {
			market.WriteString("      Trigger: " + strings.ReplaceAll(cond.TriggerConditions, "\n", "\n      ") + "\n")
		}
		if cond.EdgeCases != "" {
			market.WriteString("      Traps: " + strings.ReplaceAll(cond.EdgeCases, "\n", "\n             ") + "\n")
		}
		if cond.KeyDates != "" {
			market.WriteString("      Key dates: " + strings.ReplaceAll(cond.KeyDates, "\n", "\n               ") + "\n")
		}
		if cond.AmbiguityRisk != "" {
			market.WriteString("      Ambiguity risk: " + cond.AmbiguityRisk + "\n")
		}
	}

	// Inject news context if available
	if newsContext != "" {
		market.WriteString("   📰 Key facts from news search:\n")
		for _, line := range strings.Split(newsContext, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				market.WriteString("      " + line + "\n")
			}
		}
	}
	market.WriteString("\n")

	return strings.Replace(template, "{{MARKETS}}", market.String(), 1)
}

// ValidatePrediction checks a parsed prediction for common LLM output issues
// and returns warnings (non-fatal) that the UI should display.
func ValidatePrediction(p Prediction) []string {
	var warnings []string

	// Check if probability came from a fallback field
	if p.FinalProb > 0 && p.FinalProb != p.Probability {
		// FinalProb was used but didn't match — shouldn't happen after normalization
	}
	if p.FinalProb == 0 && p.Probability > 0 {
		warnings = append(warnings, "Used fallback probability field — LLM didn't output 'final_probability' as expected")
	}

	// Extreme probabilities
	if p.Probability == 1.0 {
		warnings = append(warnings, "Probability is exactly 1.0 — nothing is certain, this may be a parsing artifact")
	}
	if p.Probability > 0 && p.Probability < 0.02 {
		warnings = append(warnings, fmt.Sprintf("Probability %.3f is near-zero — verify this isn't a parsing issue", p.Probability))
	}
	if p.Probability > 0.98 && p.Probability < 1.0 {
		warnings = append(warnings, fmt.Sprintf("Probability %.3f is near-certain — verify the LLM isn't overconfident", p.Probability))
	}

	// Missing reasoning
	if strings.TrimSpace(p.Reasoning) == "" {
		warnings = append(warnings, "No reasoning provided — LLM may have omitted this field")
	}

	// Missing uncertainty sources
	if len(p.UncertaintySources) == 0 {
		warnings = append(warnings, "No uncertainty_sources — deep research unavailable. LLM may have omitted this field")
	} else {
		for i, us := range p.UncertaintySources {
			if len(us.SearchQueries) == 0 {
				warnings = append(warnings, fmt.Sprintf("Uncertainty source #%d has no search_queries", i+1))
			}
			if us.ExpectedImpact != "large" && us.ExpectedImpact != "medium" && us.ExpectedImpact != "small" {
				warnings = append(warnings, fmt.Sprintf("Uncertainty source #%d has invalid expected_impact: %q", i+1, us.ExpectedImpact))
			}
		}
	}

	// Confidence validation (already normalized in ParsePredictions, but check anyway for direct callers)
	conf := strings.ToLower(strings.TrimSpace(p.Confidence))
	if conf != "high" && conf != "medium" && conf != "low" {
		warnings = append(warnings, fmt.Sprintf("Invalid confidence %q — defaulting to 'low'", p.Confidence))
	}

	return warnings
}

// extractProbability is a last-resort fallback that searches raw JSON for common
// probability field names that the LLM might use instead of the expected ones.
// Uses a generic JSON map to find the value without needing struct tags.
func extractProbability(rawJSON string, id string) float64 {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawJSON), &raw); err != nil {
		return 0
	}

	// Try common field names LLMs might use
	candidates := []string{
		"final_probability", "probability", "true_probability",
		"posterior", "final_prob", "true_prob", "adjusted_probability",
	}

	for _, key := range candidates {
		if val, ok := raw[key]; ok {
			var f float64
			if json.Unmarshal(val, &f) == nil && f > 0 && f <= 1 {
				return f
			}
		}
	}

	return 0
}

// cleanJSON extracts JSON from potentially messy input (markdown code blocks, extra text).
// Handles both arrays ([...]) and single objects ({...}).
func cleanJSON(raw string) string {
	s := strings.TrimSpace(raw)

	// Strip markdown code blocks: ```json ... ``` or ``` ... ```
	if idx := strings.Index(s, "```json"); idx != -1 {
		s = s[idx+7:]
	} else if idx := strings.Index(s, "```"); idx != -1 {
		s = s[idx+3:]
	}
	if idx := strings.LastIndex(s, "```"); idx != -1 {
		s = s[:idx]
	}

	s = strings.TrimSpace(s)

	// Determine whether the top-level structure is an array or object
	// by finding whichever delimiter comes first
	arrStart := strings.Index(s, "[")
	objStart := strings.Index(s, "{")

	// If array starts first (or no object found), treat as array
	if arrStart != -1 && (objStart == -1 || arrStart < objStart) {
		arrEnd := strings.LastIndex(s, "]")
		if arrEnd > arrStart {
			return s[arrStart : arrEnd+1]
		}
	}

	// Otherwise treat as single object
	if objStart != -1 {
		objEnd := strings.LastIndex(s, "}")
		if objEnd > objStart {
			return s[objStart : objEnd+1]
		}
	}

	return ""
}
