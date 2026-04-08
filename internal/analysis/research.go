package analysis

import (
	"strings"
	"strconv"

	"github.com/badgateway/poly/internal/scanner"
)

// ResearchPrompt generates a deep-research prompt for a single market signal.
// Designed for Perplexity Sonar Deep Research (128K context).
// Uses strings.Builder (no fmt.Sprintf on user data) to avoid Go format string bugs.
func ResearchPrompt(sig scanner.Signal) string {
	return ResearchPromptWithConditions(sig, nil, "")
}

// ResearchPromptWithConditions generates a research prompt, optionally enriched
// with pre-parsed resolution conditions and news context from prior Tavily+Exa search.
func ResearchPromptWithConditions(sig scanner.Signal, cond *ParsedCondition, newsContext string) string {
	category := DetectCategory(sig.Market.Question)
	entity := extractEntity(sig.Market.Question, category)

	price := sig.YesPrice
	if sig.TargetSide == "NO" {
		price = sig.NoPrice
	}

	desc := strings.TrimSpace(sig.Market.Description)
	if desc == "" {
		desc = "(no resolution criteria provided)"
	}

	var sb strings.Builder

	// Header — no fmt.Sprintf, avoids % in user data being interpreted as format verbs
	sb.WriteString("Research the following event for a high-stakes prediction market trade.\n\n")
	sb.WriteString("**Event:** " + sig.Market.Question + "\n")
	sb.WriteString("**Category:** " + category.Label() + "\n")
	sb.WriteString("**Current Prices:** YES $" + formatFloat(sig.YesPrice, 3) + " / NO $" + formatFloat(sig.NoPrice, 3) + "\n")
	sb.WriteString("**Our Target:** Buy " + sig.TargetSide + " @ $" + formatFloat(price, 3) + "\n")
	sb.WriteString("**Resolves in:** " + strconv.Itoa(sig.DaysToResolve) + " days\n\n")
	sb.WriteString("**Resolution Criteria (from Polymarket):**\n")
	sb.WriteString(desc + "\n\n")

	// Inject parsed condition analysis if available — helps Perplexity ask the right questions
	if cond != nil && cond.Error == "" {
		sb.WriteString("**⚠️ RESOLUTION TRAP ANALYSIS (pre-parsed — research must address these):**\n\n")
		if cond.TriggerConditions != "" {
			sb.WriteString("Exact trigger: " + cond.TriggerConditions + "\n")
		}
		if cond.ResolutionSource != "" {
			sb.WriteString("Resolution authority: " + cond.ResolutionSource + "\n")
		}
		if cond.EdgeCases != "" {
			sb.WriteString("Known traps to verify:\n" + cond.EdgeCases + "\n")
		}
		if cond.KeyDates != "" {
			sb.WriteString("Key dates/cutoffs:\n" + cond.KeyDates + "\n")
		}
		if cond.AmbiguityRisk != "" {
			sb.WriteString("Ambiguity risk: " + cond.AmbiguityRisk + "\n")
		}
		sb.WriteString("\n**Your research MUST verify each trap above with current facts.**\n")
	}

	// Inject news context if available — tells Perplexity what we already know
	if newsContext != "" {
		sb.WriteString("\n**KNOWN FACTS (from prior news search — do NOT re-discover these, focus on gaps and deeper analysis):**\n\n")
		sb.WriteString(newsContext + "\n")
	}

	sb.WriteString("\n---\n\n")

	// Category-specific questions
	questions := categoryQuestions(category, entity, sig)
	sb.WriteString("**FACTUAL RESEARCH (find specific data with dates, times, and sources):**\n\n")
	qNum := 1
	for _, q := range questions {
		sb.WriteString(strconv.Itoa(qNum) + ". " + q + "\n")
		qNum++
	}

	// Adversarial block
	sb.WriteString("\n**ADVERSARIAL CHECK (search for refutations):**\n\n")
	sb.WriteString(strconv.Itoa(qNum) + ". What events or data in the last 24-48 hours could make the outcome " + sig.TargetSide + " physically impossible or extremely unlikely? Search for breaking news, official announcements, or sudden developments.\n")
	qNum++
	sb.WriteString(strconv.Itoa(qNum) + ". What is the single strongest argument AGAINST the current market price being correct?\n")
	qNum++

	// Deep layer — institutional interference, hidden factors
	sb.WriteString("\n**DEEP RESEARCH (hidden factors the market may not have priced):**\n\n")
	sb.WriteString(strconv.Itoa(qNum) + ". Search for reports on institutional interference, covert influence operations, or regulatory/rule changes that could affect this outcome but aren't reflected in mainstream coverage.\n")
	qNum++
	sb.WriteString(strconv.Itoa(qNum) + ". Are there any insider signals (unusual betting patterns, leaked documents, whistleblower reports) related to this event?\n")
	qNum++

	// Oracle / Resolution block
	sb.WriteString("\n**RESOLUTION RISK CHECK:**\n\n")
	sb.WriteString(strconv.Itoa(qNum) + ". Based on the resolution criteria above, are there any alternative interpretations that could lead to a dispute? Could this market resolve ambiguously?\n")
	qNum++
	sb.WriteString(strconv.Itoa(qNum) + ". Has Polymarket or UMA had any disputed resolutions on similar markets in the past?\n")

	// Timestamp requirement — fix #2: require time (UTC), not just date
	sb.WriteString(`

**CRITICAL TIMESTAMP RULE:** For every fact you cite, include the DATE and TIME (UTC) it was published or last updated. In 2026 markets, news from 12 hours ago may already be priced in by HFT bots. Format: "2026-03-28 14:30 UTC". Prioritize the most recent data available.

`)

	// Output format — factor weights, not probability
	sb.WriteString("**Output format (Strict JSON only):**\n\n")
	sb.WriteString("Do NOT estimate an overall probability. Instead, return FACTOR WEIGHTS — individual scored dimensions that our scoring engine will aggregate.\n\n")
	sb.WriteString("Each factor value is -1.0 to +1.0 where:\n")
	sb.WriteString("- +1.0 = strongly supports YES outcome\n")
	sb.WriteString("- 0.0 = neutral / no data\n")
	sb.WriteString("- -1.0 = strongly supports NO outcome\n\n")
	sb.WriteString("```json\n")
	sb.WriteString("{\n")
	sb.WriteString(`  "id": "` + sig.Market.ID + "\",\n")
	sb.WriteString(`  "question": "` + truncateStr(sig.Market.Question, 60) + "\",\n")

	// Write category-specific factor template
	for _, f := range categoryFactors(category) {
		sb.WriteString(`  "` + f.key + `": {"value": 0.0, "detail": "` + f.hint + `"},` + "\n")
	}

	// Common fields across all categories
	sb.WriteString(`  "key_findings": [` + "\n")
	sb.WriteString(`    {"fact": "Finding with numbers/dates", "source": "Source name + URL", "date": "2026-03-28 14:30 UTC"},` + "\n")
	sb.WriteString(`    {"fact": "Another finding", "source": "Source name + URL", "date": "2026-03-27 09:00 UTC"}` + "\n")
	sb.WriteString("  ],\n")
	sb.WriteString(`  "adversarial": "The strongest evidence or scenario AGAINST the expected outcome",` + "\n")
	sb.WriteString(`  "deep_factors": "Any hidden institutional factors, interference, or rule changes discovered",` + "\n")
	sb.WriteString(`  "resolution_risk": "low/medium/high",` + "\n")
	sb.WriteString(`  "breaking_news": false,` + "\n")
	sb.WriteString(`  "data_freshness": "high/medium/low"` + "\n")
	sb.WriteString("}\n")
	sb.WriteString("```\n\n")

	sb.WriteString("**Factor scoring rules:**\n")
	sb.WriteString("- Each factor `value` MUST be derived from a specific cited finding — not intuition\n")
	sb.WriteString("- The `detail` field MUST contain the raw data point: numbers, dates, percentages, source name\n")
	sb.WriteString("- Example: `\"current_position\": {\"value\": -0.6, \"detail\": \"17th place, 28pts from 30 games — 3pts above relegation zone (BBC Sport, 2026-03-28 09:00 UTC)\"}`\n")
	sb.WriteString("- If you cannot find data for a factor, set value to 0.0 and detail to \"NO DATA FOUND\"\n")
	sb.WriteString("- Include 5-10 key_findings minimum — every finding needs source + UTC timestamp\n")
	sb.WriteString("- breaking_news: true ONLY if you found significant developments from the last 24 hours\n")
	sb.WriteString("- data_freshness: \"high\" = sources from today/yesterday, \"medium\" = this week, \"low\" = older than 7 days\n")
	sb.WriteString("- Do NOT compute an overall probability — that is our scoring engine's job\n\n")

	// Cross-verification rule — prevents the inverted-numbers problem
	sb.WriteString("**CROSS-VERIFICATION RULE (critical):**\n")
	sb.WriteString("- For EVERY numeric claim (poll numbers, standings, percentages, prices), verify it against at least 2 independent sources\n")
	sb.WriteString("- If two sources give different numbers, report BOTH and note the discrepancy: \"Source A says X, Source B says Y\"\n")
	sb.WriteString("- NEVER report a single number as fact — always include the source name and date alongside it\n")
	sb.WriteString("- Watch for inverted/swapped numbers (e.g., reporting Party A at 46% when it's actually Party B at 46%)\n\n")

	// Historical calibration rule — prevents uncalibrated factor scores
	sb.WriteString("**HISTORICAL CALIBRATION RULE:**\n")
	sb.WriteString("- For any data source you cite (polls, forecasts, models, odds), search for its HISTORICAL ACCURACY in similar past events\n")
	sb.WriteString("- Example: \"Polls showed X, but in 2022 polls underestimated the incumbent by 8-12pp\" or \"FedWatch was correct in 7/10 recent meetings\"\n")
	sb.WriteString("- If a data source has a known systematic bias, note it explicitly in the detail field\n")
	sb.WriteString("- This calibration data is critical — our scoring engine uses it to weight your factors\n")

	return sb.String()
}

// factorDef describes a single factor in the output JSON template
type factorDef struct {
	key  string // JSON field name
	hint string // Placeholder hint for what to fill in
}

// categoryFactors returns the factor definitions for a given category.
func categoryFactors(cat MarketCategory) []factorDef {
	switch cat {
	case CategorySportsLeague:
		return []factorDef{
			{"current_position", "League position and points relative to target zone"},
			{"mathematical_path", "Points needed vs games remaining, PPG analysis"},
			{"form_momentum", "Recent W/D/L trend, goals, xG — improving or declining"},
			{"injury_impact", "Key players missing and estimated impact on results"},
			{"managerial_stability", "Manager tenure, recent changes, tactical shifts"},
			{"bookmaker_consensus", "Implied probability from major bookmakers with date"},
			{"breaking_catalyst", "Any news from last 24h that shifts the picture"},
		}

	case CategorySportsQualify:
		return []factorDef{
			{"group_position", "Current standing in group/table, points, GD"},
			{"remaining_path", "Fixtures left, difficulty, home/away split"},
			{"mathematical_scenarios", "Paths to qualification — what combinations work"},
			{"form_momentum", "Recent competitive results, ELO trajectory"},
			{"squad_availability", "Injuries, suspensions, key player fitness"},
			{"bookmaker_consensus", "Implied probability from bookmakers and models"},
			{"breaking_catalyst", "Any news from last 24h that shifts the picture"},
		}

	case CategoryPolitics:
		return []factorDef{
			{"polling_position", "Current aggregate polling numbers with dates and firms"},
			{"polling_trend", "Direction of polls over last 30 days — improving or declining"},
			{"institutional_advantage", "Media control, state apparatus, admin resources, gerrymandering"},
			{"electoral_system_bias", "How the voting system favors/disfavors this candidate"},
			{"interference_risk", "Foreign influence, covert operations, voter suppression evidence"},
			{"expert_consensus", "What credible analysts and forecasters predict"},
			{"event_calendar", "Upcoming debates, rulings, endorsements before the vote"},
			{"breaking_catalyst", "Any news from last 24h that shifts the picture"},
		}

	case CategoryMacro:
		return []factorDef{
			{"fedwatch_implied", "CME FedWatch probability with exact timestamp"},
			{"data_calendar", "Upcoming economic releases before resolution date"},
			{"yield_curve_signal", "What Treasury yields imply about expectations"},
			{"bank_consensus", "Major bank economist forecasts"},
			{"futures_pricing", "What rate futures and swaps markets price"},
			{"geopolitical_pressure", "Trade wars, sanctions, policy shocks affecting the outcome"},
			{"breaking_catalyst", "Any news from last 24h that shifts the picture"},
		}

	case CategoryCrypto:
		return []factorDef{
			{"price_trend", "Current price, 7d/30d trend, key support/resistance levels"},
			{"catalyst_proximity", "Upcoming events: halvings, ETFs, upgrades, unlocks with dates"},
			{"derivatives_signal", "Futures funding, options skew, open interest direction"},
			{"on_chain_activity", "Whale movements, exchange flows, large transfers"},
			{"regulatory_environment", "Recent or pending regulatory actions"},
			{"macro_correlation", "Dollar strength, risk appetite, equity correlation"},
			{"breaking_catalyst", "Any news from last 24h that shifts the picture"},
		}

	case CategoryCorporate:
		return []factorDef{
			{"official_filings", "Recent SEC filings, press releases, board actions"},
			{"analyst_consensus", "Ratings, price targets, recent upgrades/downgrades"},
			{"regulatory_risk", "FTC/DOJ/EU proceedings, antitrust status"},
			{"insider_signals", "Insider trading filings, executive changes"},
			{"market_sentiment", "News/social sentiment, viral developments"},
			{"deal_progress", "For M&A: regulatory approvals, shareholder votes, timeline"},
			{"breaking_catalyst", "Any news from last 24h that shifts the picture"},
		}

	default:
		return []factorDef{
			{"current_state", "Where things stand factually right now"},
			{"trend_direction", "Is the situation moving toward or away from the outcome"},
			{"expert_consensus", "What domain experts and official sources say"},
			{"bookmaker_odds", "What other markets/bookmakers price this at"},
			{"event_calendar", "Key upcoming dates that could determine the outcome"},
			{"base_rate", "Historical frequency of this type of event"},
			{"breaking_catalyst", "Any news from last 24h that shifts the picture"},
		}
	}
}

// categoryQuestions returns the factual research questions for a category.
// All strings are built with concatenation, not fmt.Sprintf, to avoid format string bugs
// when entity names contain special characters (e.g., "Péter Magyar").
func categoryQuestions(cat MarketCategory, entity string, sig scanner.Signal) []string {
	switch cat {
	case CategorySportsLeague:
		return []string{
			"What is " + entity + "'s current league position, points total, and goal difference as of today?",
			"How many games does " + entity + " have remaining this season, and who are the opponents (with dates)?",
			"What is " + entity + "'s form over the last 5-10 matches? (W/D/L, goals scored/conceded, xG if available)",
			"What is the mathematical scenario — how many points does " + entity + " need from remaining games to achieve/avoid the outcome? What is the historical points cutoff?",
			"Are there any key injuries, suspensions, or managerial changes at " + entity + "?",
			"What do major bookmakers (bet365, Betfair, William Hill) currently price this outcome at? Include the date of the odds.",
			"Is there any breaking news about " + entity + " from the last 48 hours? (transfers, boardroom changes, sanctions, off-field issues)",
		}

	case CategorySportsQualify:
		return []string{
			"What is " + entity + "'s current position in the qualification standings? (points, group position, remaining matches)",
			"What are " + entity + "'s remaining qualification fixtures and when are they played?",
			"What are the qualification scenarios — what results does " + entity + " need to qualify? Include all mathematical paths.",
			"What is " + entity + "'s recent competitive form? (last 5-10 matches, goals, ELO rating, FIFA ranking)",
			"Are there any key player injuries or suspensions ahead of the next qualifier for " + entity + "?",
			"What do bookmakers and qualification models (FiveThirtyEight, ELO-based, Opta) project for " + entity + "?",
			"Are there any off-field issues (federation disputes, bans, stadium issues, doping) affecting " + entity + "?",
		}

	case CategoryPolitics:
		return []string{
			"What are the latest election polls for " + entity + "? Include the polling firm name, exact date, sample size, and margin of error for each poll.",
			"What does the polling AGGREGATE show (not single polls)? Is there a clear trend in the last 30 days? Check multiple aggregators (Medián, Nézőpont, IDEA, 538, RCP, etc.).",
			"How does the electoral system work in this context — what does " + entity + " need to win? (majority type, district vs proportional, coalition mechanics, threshold rules)",
			"What is the historical accuracy of polls for this type of election in this country? Do polls systematically over/underestimate incumbents or opposition?",
			"What institutional advantages or disadvantages does " + entity + " have? (media control, state apparatus, ground game, campaign finance, administrative resources)",
			"Are there recent reports of changes to electoral boundaries (gerrymandering), new campaign finance laws, or voter suppression that could impact the outcome?",
			"Are there any upcoming events before the vote that could shift the race? (debates, court rulings, endorsements, scandals)",
			"What are the most credible political analysts, forecasters, and academic experts predicting for this race?",
			"Search for reports on foreign influence operations, intelligence agency involvement (e.g., SVR, NED), or covert institutional interference in this election.",
		}

	case CategoryMacro:
		return []string{
			"What is the current CME FedWatch tool probability for this outcome? Include the exact date and time of the data.",
			"What economic data releases (CPI, payrolls, GDP, PPI) are scheduled before the resolution date? List exact dates.",
			"What is the current consensus among major bank economists (Goldman, JPMorgan, Morgan Stanley, Barclays)? Include their specific forecasts.",
			"What did the most recent Fed minutes, FOMC statement, or Fed official speech signal? (cite date and speaker)",
			"What are the current Treasury yields (2Y, 10Y) and what do they imply about market expectations?",
			"Are there any geopolitical or trade policy developments (tariffs, sanctions, trade wars) that could force the Fed's hand?",
			"What do interest rate futures, fed funds futures, and swaps markets currently price for this date?",
		}

	case CategoryCrypto:
		return []string{
			"What is the current price of " + entity + " and its 7-day / 30-day price trend? Include exact price and timestamp.",
			"What are the major upcoming catalysts for " + entity + "? (halvings, ETF decisions, protocol upgrades, token unlocks — with dates)",
			"What is the current analyst consensus and price targets from major crypto research firms? (Messari, Delphi, Glassnode)",
			"Are there any significant on-chain movements? (whale wallets, exchange inflows/outflows, large transfers in last 24h)",
			"What are the macro factors currently driving crypto? (dollar strength, risk appetite, regulatory news, correlation with equities)",
			"What do derivatives markets (futures funding rates, options skew, open interest) imply about " + entity + "'s trajectory?",
			"Is there any breaking regulatory or legal news affecting " + entity + " in the last 48 hours?",
		}

	case CategoryCorporate:
		return []string{
			"Have there been any SEC filings, press releases, or official statements from " + entity + " in the last 48 hours?",
			"What is the current analyst consensus on " + entity + "? (ratings, price targets, recent upgrades/downgrades — with dates)",
			"Are there any regulatory proceedings (FTC, DOJ, EU Commission) that could affect this outcome?",
			"What are insiders doing? (insider trading filings, executive departures, board changes — from SEC EDGAR or equivalent)",
			"What is the social media and news sentiment around " + entity + " in the last week? Any viral developments?",
			"Are there any upcoming earnings, shareholder votes, or regulatory deadlines relevant to this outcome?",
			"What do M&A analysts or deal trackers say about this transaction/event completing?",
		}

	default:
		return []string{
			"What is the current factual state of affairs regarding: " + sig.Market.Question,
			"What are the most recent news articles about this topic? (cite dates and times)",
			"What do domain experts or official sources say about the likelihood of this outcome?",
			"What are the key upcoming dates or events that could determine the outcome?",
			"Is there a historical base rate for this type of event?",
			"What do bookmakers or other prediction platforms price this outcome at?",
			"Are there any recent developments (last 48 hours) that could shift the probability?",
		}
	}
}

// extractEntity pulls the primary subject from a market question.
// Uses simple heuristics — works for ~80% of Polymarket questions.
func extractEntity(question string, cat MarketCategory) string {
	q := question

	switch cat {
	case CategorySportsLeague, CategorySportsQualify:
		// "Will Tottenham be relegated..." → "Tottenham"
		// "Will Sweden qualify for..." → "Sweden"
		q = strings.TrimPrefix(q, "Will ")
		q = strings.TrimPrefix(q, "will ")
		words := strings.Fields(q)
		var entity []string
		stopWords := map[string]bool{
			"be": true, "win": true, "finish": true, "qualify": true,
			"make": true, "reach": true, "get": true, "avoid": true,
			"secure": true, "clinch": true, "earn": true,
		}
		for _, w := range words {
			if stopWords[strings.ToLower(w)] {
				break
			}
			entity = append(entity, w)
		}
		if len(entity) > 0 {
			return strings.Join(entity, " ")
		}

	case CategoryPolitics:
		// "Will the next Prime Minister of Hungary be Viktor Orbán?" → "Viktor Orbán"
		if idx := strings.LastIndex(q, " be "); idx != -1 {
			after := strings.TrimRight(q[idx+4:], "?. ")
			if after != "" {
				return after
			}
		}
		// Fallback: "Will X win the election" → "X"
		q = strings.TrimPrefix(q, "Will ")
		q = strings.TrimPrefix(q, "will ")
		words := strings.Fields(q)
		var entity []string
		stopWords := map[string]bool{
			"win": true, "be": true, "become": true, "serve": true,
			"remain": true, "pass": true, "get": true, "lose": true,
		}
		for _, w := range words {
			if stopWords[strings.ToLower(w)] {
				break
			}
			entity = append(entity, w)
		}
		if len(entity) > 0 {
			return strings.Join(entity, " ")
		}

	case CategoryCrypto:
		q = strings.TrimPrefix(q, "Will ")
		q = strings.TrimPrefix(q, "will ")
		words := strings.Fields(q)
		if len(words) > 0 {
			return words[0]
		}

	case CategoryCorporate:
		q = strings.TrimPrefix(q, "Will ")
		q = strings.TrimPrefix(q, "will ")
		words := strings.Fields(q)
		if len(words) > 0 {
			return words[0]
		}
	}

	// Fallback: first few words
	words := strings.Fields(question)
	if len(words) > 5 {
		return strings.Join(words[:5], " ")
	}
	return question
}

// formatFloat converts a float to a string with specified decimal places.
func formatFloat(f float64, decimals int) string {
	return strconv.FormatFloat(f, 'f', decimals, 64)
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
