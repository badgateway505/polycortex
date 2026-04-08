package analysis

import (
	"strconv"
	"strings"

	"github.com/badgateway/poly/internal/scanner"
)

const auditorTemplatePath = "templates/claude_auditor_prompt.md"

// GenerateAuditorPrompt builds a Claude Auditor prompt for a single signal,
// filling in market details and optionally Tavily context.
// perplexityOutput and pillarlabOutput are left as placeholders if empty —
// the user pastes those manually after getting them from the other two engines.
func GenerateAuditorPrompt(template string, sig scanner.Signal, tavilyContext string, perplexityOutput string, pillarlabOutput string) string {
	return GenerateAuditorPromptWithConditions(template, sig, tavilyContext, perplexityOutput, pillarlabOutput, nil)
}

// GenerateAuditorPromptWithConditions is the same as GenerateAuditorPrompt but also
// injects pre-parsed resolution conditions so the Auditor can reason about resolution traps.
func GenerateAuditorPromptWithConditions(template string, sig scanner.Signal, tavilyContext string, perplexityOutput string, pillarlabOutput string, cond *ParsedCondition) string {
	price := sig.YesPrice
	if sig.TargetSide == "NO" {
		price = sig.NoPrice
	}

	desc := strings.TrimSpace(sig.Market.Description)
	if desc == "" {
		desc = "(no resolution criteria provided)"
	}

	r := strings.NewReplacer(
		"{{MARKET_QUESTION}}", sig.Market.Question,
		"{{MARKET_ID}}", sig.Market.ID,
		"{{CURRENT_PRICE}}", "YES $"+formatFloat(sig.YesPrice, 3)+" / NO $"+formatFloat(sig.NoPrice, 3)+" (Target: "+sig.TargetSide+" @ $"+formatFloat(price, 3)+")",
		"{{RESOLUTION_CRITERIA}}", desc,
		"{{END_DATE}}", sig.Market.EndDate.Format("2006-01-02")+" ("+strconv.Itoa(sig.DaysToResolve)+" days)",
	)
	result := r.Replace(template)

	// Fill Perplexity output
	if perplexityOutput != "" {
		result = strings.Replace(result, "{{PERPLEXITY_OUTPUT}}", perplexityOutput, 1)
	} else {
		result = strings.Replace(result, "{{PERPLEXITY_OUTPUT}}", "[PASTE PERPLEXITY RESEARCH OUTPUT HERE]", 1)
	}

	// Fill PillarLab output
	if pillarlabOutput != "" {
		result = strings.Replace(result, "{{PILLARLAB_OUTPUT}}", pillarlabOutput, 1)
	} else {
		result = strings.Replace(result, "{{PILLARLAB_OUTPUT}}", "[PASTE PILLARLAB JSON OUTPUT HERE]", 1)
	}

	// Fill Tavily context
	if tavilyContext != "" {
		result = strings.Replace(result, "{{TAVILY_CONTEXT}}", tavilyContext, 1)
	} else {
		result = strings.Replace(result, "{{TAVILY_CONTEXT}}", "(no Tavily search performed — use /tavily N to fetch)", 1)
	}

	// Inject parsed resolution conditions if available
	var condBlock string
	if cond != nil && cond.Error == "" {
		var sb strings.Builder
		sb.WriteString("**⚠️ RESOLUTION TRAP ANALYSIS (pre-parsed — you MUST address each trap):**\n\n")
		if cond.TriggerConditions != "" {
			sb.WriteString("Exact trigger conditions:\n" + cond.TriggerConditions + "\n\n")
		}
		if cond.ResolutionSource != "" {
			sb.WriteString("Resolution authority: " + cond.ResolutionSource + "\n\n")
		}
		if cond.EdgeCases != "" {
			sb.WriteString("Known resolution traps:\n" + cond.EdgeCases + "\n\n")
		}
		if cond.KeyDates != "" {
			sb.WriteString("Key dates/cutoffs:\n" + cond.KeyDates + "\n\n")
		}
		if cond.AmbiguityRisk != "" {
			sb.WriteString("Ambiguity risk: " + strings.ToUpper(cond.AmbiguityRisk) + "\n\n")
		}
		sb.WriteString("For each trap listed above, determine whether current evidence confirms or refutes it.\n")
		condBlock = sb.String()
	} else {
		condBlock = "(resolution conditions not parsed — click Parse in the UI first for trap detection)"
	}
	result = strings.Replace(result, "{{CONDITION_ANALYSIS}}", condBlock, 1)

	return result
}
