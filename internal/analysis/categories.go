package analysis

import "strings"

// MarketCategory represents the detected category of a prediction market
type MarketCategory string

const (
	CategorySportsLeague   MarketCategory = "SPORTS_LEAGUE"   // Relegation, top 4, standings
	CategorySportsQualify  MarketCategory = "SPORTS_QUALIFY"  // World Cup, tournament qualification
	CategoryPolitics       MarketCategory = "POLITICS"        // Elections, appointments, legislation
	CategoryCrypto         MarketCategory = "CRYPTO"          // Price targets, ETF approvals, halvings
	CategoryMacro          MarketCategory = "MACRO"           // Fed rates, inflation, economic indicators
	CategoryCorporate      MarketCategory = "CORPORATE"       // M&A, IPO, token launches, SEC filings
	CategoryGeneral        MarketCategory = "GENERAL"         // Anything that doesn't match above
)

// categoryRule maps keywords to categories, checked in order (first match wins)
type categoryRule struct {
	category MarketCategory
	keywords []string // ALL keywords in a group must appear (AND logic)
	anyOf    []string // ANY keyword in this list triggers (OR logic)
}

var categoryRules = []categoryRule{
	// Sports — League standings (relegation, top N, champion)
	{
		category: CategorySportsLeague,
		anyOf: []string{
			"relegated", "relegation", "top 4", "top 6", "top four",
			"finish in the top", "epl", "premier league", "la liga",
			"serie a", "bundesliga", "ligue 1", "champion of",
			"win the league", "league standings",
		},
	},
	// Sports — Qualification (World Cup, Euro, Olympics)
	{
		category: CategorySportsQualify,
		anyOf: []string{
			"qualify for", "qualification", "world cup", "euro 2",
			"olympics", "copa america", "champions league qualify",
			"fifa", "uefa",
		},
	},
	// Politics — Elections, appointments, legislation
	{
		category: CategoryPolitics,
		anyOf: []string{
			"prime minister", "president", "election", "elected",
			"appointed", "governor", "mayor", "parliament",
			"congress", "senate", "legislation", "bill pass",
			"impeach", "resign", "vote of confidence",
			"referendum", "coalition",
		},
	},
	// Macro — Central banks, economic indicators
	{
		category: CategoryMacro,
		anyOf: []string{
			"fed ", "federal reserve", "interest rate", "rate cut",
			"rate hike", "inflation", "cpi", "gdp", "payrolls",
			"unemployment", "ecb", "bank of england", "boj",
			"recession", "tariff",
		},
	},
	// Crypto — Price, ETF, protocol events
	{
		category: CategoryCrypto,
		anyOf: []string{
			"bitcoin", "btc", "ethereum", "eth", "solana", "sol",
			"crypto", "token", "halving", "etf approv",
			"defi", "nft", "blockchain", "stablecoin",
		},
	},
	// Corporate — M&A, IPO, SEC, earnings
	{
		category: CategoryCorporate,
		anyOf: []string{
			"acquire", "acquisition", "merger", "ipo", "sec filing",
			"earnings", "revenue", "stock price", "market cap",
			"ceo", "board of directors", "antitrust", "ftc",
			"launch product", "token launch",
		},
	},
}

// DetectCategory determines the market category from the question text.
func DetectCategory(question string) MarketCategory {
	lower := strings.ToLower(question)

	for _, rule := range categoryRules {
		for _, kw := range rule.anyOf {
			if strings.Contains(lower, kw) {
				return rule.category
			}
		}
	}

	return CategoryGeneral
}

// CategoryLabel returns a human-readable label for the category
func (c MarketCategory) Label() string {
	switch c {
	case CategorySportsLeague:
		return "Sports (League)"
	case CategorySportsQualify:
		return "Sports (Qualification)"
	case CategoryPolitics:
		return "Politics"
	case CategoryCrypto:
		return "Crypto"
	case CategoryMacro:
		return "Macroeconomics"
	case CategoryCorporate:
		return "Corporate"
	default:
		return "General"
	}
}
